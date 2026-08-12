package statebackup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scriptboard/internal/instancelock"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	archiveMagic          = "SBSB\x00\x01"
	manifestPath          = "MANIFEST.json"
	archiveChunkSize      = 64 << 10
	maximumHeaderSize     = 16 << 10
	maximumManifestSize   = 32 << 20
	maximumCheckpointSize = 64 << 10
	maximumArchiveFile    = 100_000
	maximumArchiveSize    = int64(64 << 30)
	kdfMemoryKiB          = 64 * 1024
	kdfIterations         = 3
	kdfParallelism        = 2
)

var privateStateDirectories = []string{"runs", "secrets", "security-events"}

type Options struct {
	StateRoot string
	Database  *sql.DB
	Now       func() time.Time
	Random    io.Reader
}

type CreateRequest struct {
	Destination     string
	Passphrase      []byte
	AuditCheckpoint json.RawMessage
}

type Artifact struct {
	Path     string
	Manifest Manifest
}

type RestoreRequest struct {
	StateRoot            string
	ArchivePath          string
	Passphrase           []byte
	ConfirmBackupID      string
	MinimumSchemaVersion int
	MaximumSchemaVersion int
	ValidateStaged       func(context.Context, string, Manifest) error
	Finalize             func(context.Context, RestoreResult) error
}

type RestoreResult struct {
	Manifest           Manifest
	PreservedStatePath string
}

type Manifest struct {
	FormatVersion        int                  `json:"format_version"`
	ID                   string               `json:"id"`
	CreatedAt            string               `json:"created_at"`
	SchemaVersion        int                  `json:"schema_version"`
	Files                []FileManifest       `json:"files"`
	Excluded             []string             `json:"excluded"`
	ExternalDependencies []ExternalDependency `json:"external_dependencies"`
	AuditCheckpoint      json.RawMessage      `json:"audit_checkpoint"`
}

type FileManifest struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ExternalDependency struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Included bool   `json:"included"`
}

type Manager struct {
	stateRoot string
	database  *sql.DB
	now       func() time.Time
	random    io.Reader
}

func New(options Options) (*Manager, error) {
	if options.Database == nil {
		return nil, errors.New("state backup requires an open SQLite database")
	}
	root := strings.TrimSpace(options.StateRoot)
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("state backup requires an absolute State Root")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve State Root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("inspect State Root: %w", err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve State Root links: %w", err)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	return &Manager{stateRoot: filepath.Clean(absolute), database: options.Database, now: now, random: random}, nil
}

func (manager *Manager) Create(ctx context.Context, request CreateRequest) (artifact Artifact, err error) {
	destination, err := manager.validateDestination(request.Destination)
	if err != nil {
		return Artifact{}, err
	}
	if len(request.Passphrase) < 16 {
		return Artifact{}, errors.New("state backup passphrase must contain at least 16 bytes")
	}
	if len(request.AuditCheckpoint) == 0 || len(request.AuditCheckpoint) > maximumCheckpointSize || !json.Valid(request.AuditCheckpoint) {
		return Artifact{}, errors.New("state backup requires a valid signed audit checkpoint document")
	}
	id, err := randomID(manager.random)
	if err != nil {
		return Artifact{}, fmt.Errorf("create state backup ID: %w", err)
	}
	snapshot := filepath.Join(manager.stateRoot, ".state-backup-"+id+".db")
	if err := manager.snapshotDatabase(ctx, snapshot); err != nil {
		return Artifact{}, err
	}
	defer os.Remove(snapshot)

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Artifact{}, fmt.Errorf("create state backup without overwrite: %w", err)
	}
	committed := false
	defer func() {
		_ = output.Close()
		if !committed {
			_ = os.Remove(destination)
		}
	}()

	encrypted, err := newEncryptedWriter(output, request.Passphrase, manager.random)
	if err != nil {
		return Artifact{}, err
	}
	archive := tar.NewWriter(encrypted)
	manifest := Manifest{
		FormatVersion: 1,
		ID:            id,
		CreatedAt:     manager.now().UTC().Format(time.RFC3339),
		Excluded:      []string{"database-backups/", "inbox/", "logs/", "runtime/", "app.db-wal", "app.db-shm", "instance.lock"},
		ExternalDependencies: []ExternalDependency{
			{Name: "recoverable-secret-master-key", Required: true, Included: false},
			{Name: "audit-checkpoint-signing-key", Required: true, Included: false},
			{Name: "startup-configuration-and-TLS-material", Required: false, Included: false},
		},
		AuditCheckpoint: append(json.RawMessage(nil), request.AuditCheckpoint...),
	}
	if err := manager.database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&manifest.SchemaVersion); err != nil {
		return Artifact{}, fmt.Errorf("read SQLite schema version for state backup: %w", err)
	}
	budget := archiveBudget{}
	if err := appendArchiveFile(archive, snapshot, "app.db", &manifest, &budget); err != nil {
		return Artifact{}, err
	}
	for _, directory := range privateStateDirectories {
		if err := appendPrivateDirectory(archive, manager.stateRoot, directory, &manifest, &budget); err != nil {
			return Artifact{}, err
		}
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return Artifact{}, fmt.Errorf("encode state backup manifest: %w", err)
	}
	if len(manifestBytes) > maximumManifestSize || int64(len(manifestBytes)) > maximumArchiveSize-budget.total {
		return Artifact{}, errors.New("state backup manifest exceeds archive limits")
	}
	if err := archive.WriteHeader(&tar.Header{Name: manifestPath, Mode: 0o600, Size: int64(len(manifestBytes)), ModTime: manager.now().UTC(), Typeflag: tar.TypeReg}); err != nil {
		return Artifact{}, fmt.Errorf("write state backup manifest header: %w", err)
	}
	if _, err := archive.Write(manifestBytes); err != nil {
		return Artifact{}, fmt.Errorf("write state backup manifest: %w", err)
	}
	if err := archive.Close(); err != nil {
		return Artifact{}, fmt.Errorf("finish state backup archive: %w", err)
	}
	if err := encrypted.Close(); err != nil {
		return Artifact{}, fmt.Errorf("finish state backup encryption: %w", err)
	}
	if err := output.Sync(); err != nil {
		return Artifact{}, fmt.Errorf("sync state backup: %w", err)
	}
	if err := output.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close state backup: %w", err)
	}
	committed = true
	return Artifact{Path: destination, Manifest: manifest}, nil
}

func Inspect(ctx context.Context, archivePath string, passphrase []byte) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if len(passphrase) < 16 {
		return Manifest{}, errors.New("state backup passphrase must contain at least 16 bytes")
	}
	input, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open state backup: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Manifest{}, errors.New("state backup must be a regular file")
	}
	decrypted, err := newEncryptedReader(input, passphrase)
	if err != nil {
		return Manifest{}, err
	}
	reader := tar.NewReader(decrypted)
	seen := make(map[string]FileManifest)
	var manifest Manifest
	foundManifest := false
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return Manifest{}, fmt.Errorf("read state backup archive: %w", nextErr)
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		if foundManifest {
			return Manifest{}, errors.New("state backup manifest must be the final archive entry")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return Manifest{}, errors.New("state backup contains a non-regular entry")
		}
		if header.Size < 0 || header.Size > maximumArchiveSize-total {
			return Manifest{}, errors.New("state backup exceeds the extraction size limit")
		}
		total += header.Size
		if header.Name == manifestPath {
			if header.Size > maximumManifestSize {
				return Manifest{}, errors.New("state backup manifest exceeds its size limit")
			}
			manifestBytes, readErr := io.ReadAll(io.LimitReader(reader, header.Size+1))
			if readErr != nil || int64(len(manifestBytes)) != header.Size {
				return Manifest{}, errors.New("read complete state backup manifest")
			}
			if err := decodeManifest(manifestBytes, &manifest); err != nil {
				return Manifest{}, fmt.Errorf("decode state backup manifest: %w", err)
			}
			foundManifest = true
			continue
		}
		name, err := validateArchivePath(header.Name)
		if err != nil {
			return Manifest{}, err
		}
		if len(seen) >= maximumArchiveFile {
			return Manifest{}, errors.New("state backup contains too many files")
		}
		if _, duplicate := seen[name]; duplicate {
			return Manifest{}, fmt.Errorf("state backup contains duplicate path %q", name)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, io.LimitReader(reader, header.Size))
		if copyErr != nil || written != header.Size {
			return Manifest{}, fmt.Errorf("read complete state backup entry %q", name)
		}
		seen[name] = FileManifest{Path: name, Size: header.Size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	if !foundManifest {
		return Manifest{}, errors.New("state backup manifest is missing")
	}
	if _, err := io.Copy(io.Discard, decrypted); err != nil {
		return Manifest{}, fmt.Errorf("authenticate complete state backup: %w", err)
	}
	if err := validateManifest(manifest, seen); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Restore(ctx context.Context, request RestoreRequest) (RestoreResult, error) {
	stateRoot := strings.TrimSpace(request.StateRoot)
	if stateRoot == "" || !filepath.IsAbs(stateRoot) {
		return RestoreResult{}, errors.New("state restore requires an absolute State Root")
	}
	stateRoot, err := filepath.Abs(stateRoot)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resolve restore State Root: %w", err)
	}
	stateRoot = filepath.Clean(stateRoot)
	if info, err := os.Stat(stateRoot); err != nil || !info.IsDir() {
		return RestoreResult{}, errors.New("restore State Root must be an existing directory")
	}
	stateRoot, err = filepath.EvalSymlinks(stateRoot)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resolve restore State Root links: %w", err)
	}
	archivePath := strings.TrimSpace(request.ArchivePath)
	if archivePath == "" || !filepath.IsAbs(archivePath) {
		return RestoreResult{}, errors.New("state restore archive path must be absolute")
	}
	archivePath, err = filepath.Abs(archivePath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resolve state restore archive: %w", err)
	}
	archivePath, err = canonicalRegularFile(archivePath)
	if err != nil {
		return RestoreResult{}, err
	}
	if withinPath(stateRoot, archivePath) || withinPath(stageRootFor(stateRoot), archivePath) {
		return RestoreResult{}, errors.New("state restore archive must be outside State Root")
	}
	manifest, err := Inspect(ctx, archivePath, request.Passphrase)
	if err != nil {
		return RestoreResult{}, err
	}
	if request.ConfirmBackupID == "" || request.ConfirmBackupID != manifest.ID {
		return RestoreResult{}, errors.New("state restore confirmation must exactly match the backup ID")
	}
	if request.MinimumSchemaVersion <= 0 || request.MaximumSchemaVersion < request.MinimumSchemaVersion || manifest.SchemaVersion < request.MinimumSchemaVersion || manifest.SchemaVersion > request.MaximumSchemaVersion {
		return RestoreResult{}, fmt.Errorf("state backup schema %d is outside supported range %d..%d", manifest.SchemaVersion, request.MinimumSchemaVersion, request.MaximumSchemaVersion)
	}
	lock, err := instancelock.Acquire(stateRoot)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("state restore requires the ScriptBoard service to be stopped: %w", err)
	}
	defer lock.Close()
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}
	stageRoot, err := os.MkdirTemp(filepath.Dir(stateRoot), "."+filepath.Base(stateRoot)+".restore-stage-")
	if err != nil {
		return RestoreResult{}, fmt.Errorf("create state restore staging directory: %w", err)
	}
	defer os.RemoveAll(stageRoot)
	extractedManifest, err := extractArchive(ctx, archivePath, request.Passphrase, stageRoot)
	if err != nil {
		return RestoreResult{}, err
	}
	if extractedManifest.ID != manifest.ID || extractedManifest.SchemaVersion != manifest.SchemaVersion {
		return RestoreResult{}, errors.New("state backup changed between validation and staging")
	}
	databasePath := filepath.Join(stageRoot, "app.db")
	if err := verifyRestoredDatabase(databasePath, manifest.SchemaVersion); err != nil {
		return RestoreResult{}, err
	}
	if err := revokeRestoredSessions(databasePath); err != nil {
		return RestoreResult{}, err
	}
	if request.ValidateStaged != nil {
		if err := request.ValidateStaged(ctx, databasePath, manifest); err != nil {
			return RestoreResult{}, fmt.Errorf("validate staged state backup: %w", err)
		}
	}
	preservedRoot := filepath.Join(filepath.Dir(stateRoot), filepath.Base(stateRoot)+".before-restore-"+manifest.ID)
	if _, err := os.Stat(preservedRoot); !os.IsNotExist(err) {
		return RestoreResult{}, errors.New("preserved pre-restore state path already exists")
	}
	if err := os.Mkdir(preservedRoot, 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("create preserved pre-restore state directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(preservedRoot)
		}
	}()
	rollback, err := replacePrivateState(stateRoot, stageRoot, preservedRoot)
	if err != nil {
		return RestoreResult{}, err
	}
	result := RestoreResult{Manifest: manifest, PreservedStatePath: preservedRoot}
	if request.Finalize != nil {
		if err := request.Finalize(ctx, result); err != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return RestoreResult{}, fmt.Errorf("finalize restored state: %v; rollback failed: %w", err, rollbackErr)
			}
			return RestoreResult{}, fmt.Errorf("finalize restored state: %w", err)
		}
	}
	committed = true
	return result, nil
}

func extractArchive(ctx context.Context, archivePath string, passphrase []byte, destination string) (Manifest, error) {
	input, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open state backup for restore: %w", err)
	}
	defer input.Close()
	decrypted, err := newEncryptedReader(input, passphrase)
	if err != nil {
		return Manifest{}, err
	}
	reader := tar.NewReader(decrypted)
	seen := make(map[string]FileManifest)
	var manifest Manifest
	foundManifest := false
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return Manifest{}, fmt.Errorf("read state backup for restore: %w", nextErr)
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		if foundManifest {
			return Manifest{}, errors.New("state backup manifest must be the final archive entry")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return Manifest{}, errors.New("state backup contains a non-regular entry")
		}
		if header.Size < 0 || header.Size > maximumArchiveSize-total {
			return Manifest{}, errors.New("state backup exceeds the extraction size limit")
		}
		total += header.Size
		if header.Name == manifestPath {
			if header.Size > maximumManifestSize {
				return Manifest{}, errors.New("state backup manifest exceeds its size limit")
			}
			manifestBytes, readErr := io.ReadAll(io.LimitReader(reader, header.Size+1))
			if readErr != nil || int64(len(manifestBytes)) != header.Size {
				return Manifest{}, errors.New("read complete state backup manifest")
			}
			if err := decodeManifest(manifestBytes, &manifest); err != nil {
				return Manifest{}, fmt.Errorf("decode state backup manifest: %w", err)
			}
			foundManifest = true
			continue
		}
		name, err := validateArchivePath(header.Name)
		if err != nil {
			return Manifest{}, err
		}
		if len(seen) >= maximumArchiveFile {
			return Manifest{}, errors.New("state backup contains too many files")
		}
		if _, duplicate := seen[name]; duplicate {
			return Manifest{}, fmt.Errorf("state backup contains duplicate path %q", name)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if !withinPath(destination, target) {
			return Manifest{}, fmt.Errorf("state backup path escapes restore staging: %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return Manifest{}, fmt.Errorf("create restore staging directory: %w", err)
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return Manifest{}, fmt.Errorf("create restore staging file %q: %w", name, err)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(reader, header.Size))
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || written != header.Size {
			return Manifest{}, fmt.Errorf("stage complete state backup entry %q", name)
		}
		seen[name] = FileManifest{Path: name, Size: header.Size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	if !foundManifest {
		return Manifest{}, errors.New("state backup manifest is missing")
	}
	if _, err := io.Copy(io.Discard, decrypted); err != nil {
		return Manifest{}, fmt.Errorf("authenticate complete state backup: %w", err)
	}
	if err := validateManifest(manifest, seen); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func verifyRestoredDatabase(databasePath string, schemaVersion int) error {
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open restored SQLite snapshot: %w", err)
	}
	defer database.Close()
	var integrity string
	if err := database.QueryRow("PRAGMA quick_check(1)").Scan(&integrity); err != nil || integrity != "ok" {
		return fmt.Errorf("restored SQLite snapshot failed integrity verification: result=%q error=%v", integrity, err)
	}
	var actualSchema int
	if err := database.QueryRow("PRAGMA user_version").Scan(&actualSchema); err != nil || actualSchema != schemaVersion {
		return fmt.Errorf("restored SQLite schema does not match manifest: got=%d want=%d error=%v", actualSchema, schemaVersion, err)
	}
	return nil
}

func revokeRestoredSessions(databasePath string) error {
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return fmt.Errorf("open restored SQLite state for session revocation: %w", err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = database.Close()
		return fmt.Errorf("configure restored SQLite state: %w", err)
	}
	if _, err := database.Exec("DELETE FROM sessions"); err != nil {
		_ = database.Close()
		return fmt.Errorf("revoke restored sessions: %w", err)
	}
	if _, err := database.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = database.Close()
		return fmt.Errorf("checkpoint restored SQLite state: %w", err)
	}
	if _, err := database.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		_ = database.Close()
		return fmt.Errorf("finalize restored SQLite state: %w", err)
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("close restored SQLite state: %w", err)
	}
	_ = os.Remove(databasePath + "-wal")
	_ = os.Remove(databasePath + "-shm")
	return verifyDatabaseAfterSessionRevocation(databasePath)
}

func verifyDatabaseAfterSessionRevocation(databasePath string) error {
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro")
	if err != nil {
		return err
	}
	defer database.Close()
	var result string
	if err := database.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil || result != "ok" {
		return fmt.Errorf("restored SQLite state failed post-revocation verification: result=%q error=%v", result, err)
	}
	return nil
}

func replacePrivateState(stateRoot, stageRoot, preservedRoot string) (func() error, error) {
	names := append([]string{"app.db", "app.db-wal", "app.db-shm", "app.db-journal"}, privateStateDirectories...)
	movedCurrent := make([]string, 0, len(names))
	movedStaged := make([]string, 0, len(names))
	rollback := func() error {
		var rollbackErrors []error
		for index := len(movedStaged) - 1; index >= 0; index-- {
			name := movedStaged[index]
			if err := os.Rename(filepath.Join(stateRoot, name), filepath.Join(stageRoot, name)); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove restored private state %q: %w", name, err))
			}
		}
		for index := len(movedCurrent) - 1; index >= 0; index-- {
			name := movedCurrent[index]
			if err := os.Rename(filepath.Join(preservedRoot, name), filepath.Join(stateRoot, name)); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore preserved private state %q: %w", name, err))
			}
		}
		return errors.Join(rollbackErrors...)
	}
	for _, name := range names {
		current := filepath.Join(stateRoot, name)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			_ = rollback()
			return nil, fmt.Errorf("inspect current private state %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			_ = rollback()
			return nil, fmt.Errorf("current private state %q must not be a symbolic link", name)
		}
		if err := os.Rename(current, filepath.Join(preservedRoot, name)); err != nil {
			_ = rollback()
			return nil, fmt.Errorf("preserve current private state %q: %w", name, err)
		}
		movedCurrent = append(movedCurrent, name)
	}
	for _, name := range names {
		staged := filepath.Join(stageRoot, name)
		if _, err := os.Lstat(staged); os.IsNotExist(err) {
			continue
		} else if err != nil {
			_ = rollback()
			return nil, fmt.Errorf("inspect staged private state %q: %w", name, err)
		}
		if err := os.Rename(staged, filepath.Join(stateRoot, name)); err != nil {
			_ = rollback()
			return nil, fmt.Errorf("commit restored private state %q: %w", name, err)
		}
		movedStaged = append(movedStaged, name)
	}
	return rollback, nil
}

func (manager *Manager) validateDestination(raw string) (string, error) {
	destination := strings.TrimSpace(raw)
	if destination == "" || !filepath.IsAbs(destination) {
		return "", errors.New("state backup destination must be absolute")
	}
	destination = filepath.Clean(destination)
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return "", errors.New("state backup destination parent must be an existing directory")
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if withinPath(manager.stateRoot, destination) || withinPath(stageRootFor(manager.stateRoot), destination) {
		return "", errors.New("state backup destination must be outside State Root and restore staging")
	}
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return "", errors.New("state backup destination parent must be an existing directory")
	}
	return destination, nil
}

func (manager *Manager) snapshotDatabase(ctx context.Context, destination string) error {
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		return errors.New("state backup snapshot destination already exists")
	}
	if _, err := manager.database.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("create consistent SQLite state snapshot: %w", err)
	}
	info, err := os.Stat(destination)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("SQLite state snapshot is not a non-empty regular file")
	}
	return nil
}

type archiveBudget struct {
	files int
	total int64
}

func (budget *archiveBudget) reserve(size int64) error {
	if size < 0 || budget.files >= maximumArchiveFile || size > maximumArchiveSize-budget.total {
		return errors.New("private state exceeds backup archive limits")
	}
	budget.files++
	budget.total += size
	return nil
}

func appendPrivateDirectory(archive *tar.Writer, stateRoot, directory string, manifest *Manifest, budget *archiveBudget) error {
	root := filepath.Join(stateRoot, directory)
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect private state directory %q: %w", directory, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private state path %q is not a real directory", directory)
	}
	return filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		entryInfo, err := os.Lstat(filePath)
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("private state contains a link at %q", filePath)
		}
		if entryInfo.IsDir() {
			return nil
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("private state contains a non-regular file at %q", filePath)
		}
		relative, err := filepath.Rel(stateRoot, filePath)
		if err != nil {
			return err
		}
		return appendArchiveFile(archive, filePath, filepath.ToSlash(relative), manifest, budget)
	})
}

func appendArchiveFile(archive *tar.Writer, source, name string, manifest *Manifest, budget *archiveBudget) error {
	name, err := validateArchivePath(name)
	if err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open private state file %q: %w", name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("private state file %q is not regular", name)
	}
	if err := budget.reserve(info.Size()); err != nil {
		return fmt.Errorf("archive private state file %q: %w", name, err)
	}
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime().UTC(), Typeflag: tar.TypeReg}); err != nil {
		return fmt.Errorf("write private state header %q: %w", name, err)
	}
	hash := sha256.New()
	written, err := io.Copy(archive, io.TeeReader(file, hash))
	if err != nil || written != info.Size() {
		return fmt.Errorf("archive complete private state file %q", name)
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("private state file %q changed during backup", name)
	}
	manifest.Files = append(manifest.Files, FileManifest{Path: name, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))})
	return nil
}

func validateArchivePath(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("state backup contains unsafe path %q", name)
	}
	if name == "app.db" {
		return name, nil
	}
	for _, directory := range privateStateDirectories {
		if strings.HasPrefix(name, directory+"/") {
			return name, nil
		}
	}
	return "", fmt.Errorf("state backup contains path outside the private-state allowlist: %q", name)
}

func validateManifest(manifest Manifest, seen map[string]FileManifest) error {
	id, idErr := base64.RawURLEncoding.DecodeString(manifest.ID)
	createdAt, timeErr := time.Parse(time.RFC3339, manifest.CreatedAt)
	if manifest.FormatVersion != 1 || idErr != nil || len(id) != 18 || timeErr != nil || createdAt.Format(time.RFC3339) != manifest.CreatedAt || manifest.SchemaVersion < 0 {
		return errors.New("state backup manifest metadata is invalid")
	}
	if len(manifest.AuditCheckpoint) == 0 || len(manifest.AuditCheckpoint) > maximumCheckpointSize || !json.Valid(manifest.AuditCheckpoint) || manifest.AuditCheckpoint[0] != '{' {
		return errors.New("state backup signed audit checkpoint is invalid")
	}
	if len(manifest.Files) != len(seen) {
		return errors.New("state backup manifest file set does not match archive")
	}
	manifestPaths := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		name, err := validateArchivePath(file.Path)
		if err != nil {
			return err
		}
		if _, duplicate := manifestPaths[name]; duplicate {
			return fmt.Errorf("state backup manifest contains duplicate path %q", name)
		}
		manifestPaths[name] = struct{}{}
		if file.Size < 0 || len(file.SHA256) != sha256.Size*2 || file.SHA256 != strings.ToLower(file.SHA256) {
			return fmt.Errorf("state backup manifest entry %q has invalid metadata", name)
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("state backup manifest entry %q has invalid digest", name)
		}
		actual, ok := seen[name]
		if !ok || actual.Size != file.Size || actual.SHA256 != file.SHA256 {
			return fmt.Errorf("state backup entry %q failed manifest verification", name)
		}
	}
	if _, ok := manifestPaths["app.db"]; !ok {
		return errors.New("state backup manifest is missing app.db")
	}
	return nil
}

func decodeManifest(body []byte, destination *Manifest) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode state backup manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("state backup manifest contains trailing data")
	}
	return nil
}

func randomID(random io.Reader) (string, error) {
	value := make([]byte, 18)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func withinPath(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func canonicalRegularFile(raw string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Clean(raw))
	if err != nil {
		return "", errors.New("state backup archive must be an existing regular file")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("state backup archive must be an existing regular file")
	}
	return resolved, nil
}

type encryptionHeader struct {
	Format      int    `json:"format"`
	KDF         string `json:"kdf"`
	Salt        string `json:"salt"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	NoncePrefix string `json:"nonce_prefix"`
	ChunkSize   int    `json:"chunk_size"`
}

type encryptedWriter struct {
	destination io.Writer
	aead        cipherAEAD
	header      []byte
	noncePrefix []byte
	buffer      []byte
	counter     uint64
	closed      bool
}

type cipherAEAD interface {
	NonceSize() int
	Overhead() int
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

func newEncryptedWriter(destination io.Writer, passphrase []byte, random io.Reader) (*encryptedWriter, error) {
	salt := make([]byte, 16)
	noncePrefix := make([]byte, chacha20poly1305.NonceSizeX-8)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, fmt.Errorf("create state backup salt: %w", err)
	}
	if _, err := io.ReadFull(random, noncePrefix); err != nil {
		return nil, fmt.Errorf("create state backup nonce: %w", err)
	}
	header := encryptionHeader{Format: 1, KDF: "argon2id", Salt: base64.RawStdEncoding.EncodeToString(salt), MemoryKiB: kdfMemoryKiB, Iterations: kdfIterations, Parallelism: kdfParallelism, NoncePrefix: base64.RawStdEncoding.EncodeToString(noncePrefix), ChunkSize: archiveChunkSize}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	key := argon2.IDKey(passphrase, salt, header.Iterations, header.MemoryKiB, header.Parallelism, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	for index := range key {
		key[index] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("initialize state backup encryption: %w", err)
	}
	if _, err := io.WriteString(destination, archiveMagic); err != nil {
		return nil, err
	}
	if err := binary.Write(destination, binary.BigEndian, uint32(len(headerBytes))); err != nil {
		return nil, err
	}
	if _, err := destination.Write(headerBytes); err != nil {
		return nil, err
	}
	return &encryptedWriter{destination: destination, aead: aead, header: headerBytes, noncePrefix: noncePrefix, buffer: make([]byte, 0, archiveChunkSize)}, nil
}

func (writer *encryptedWriter) Write(value []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("state backup encryption writer is closed")
	}
	written := len(value)
	for len(value) > 0 {
		remaining := archiveChunkSize - len(writer.buffer)
		if remaining > len(value) {
			remaining = len(value)
		}
		writer.buffer = append(writer.buffer, value[:remaining]...)
		value = value[remaining:]
		if len(writer.buffer) == archiveChunkSize {
			if err := writer.flush(false); err != nil {
				return written - len(value), err
			}
		}
	}
	return written, nil
}

func (writer *encryptedWriter) Close() error {
	if writer.closed {
		return nil
	}
	writer.closed = true
	if len(writer.buffer) > 0 {
		if err := writer.flush(false); err != nil {
			return err
		}
	}
	return writer.flush(true)
}

func (writer *encryptedWriter) flush(final bool) error {
	flag := byte(0)
	if final {
		flag = 1
	}
	nonce := makeNonce(writer.noncePrefix, writer.counter)
	aad := makeAAD(writer.header, flag, writer.counter)
	ciphertext := writer.aead.Seal(nil, nonce, writer.buffer, aad)
	if _, err := writer.destination.Write([]byte{flag}); err != nil {
		return err
	}
	if err := binary.Write(writer.destination, binary.BigEndian, uint32(len(ciphertext))); err != nil {
		return err
	}
	if _, err := writer.destination.Write(ciphertext); err != nil {
		return err
	}
	writer.counter++
	writer.buffer = writer.buffer[:0]
	return nil
}

type encryptedReader struct {
	source      io.Reader
	aead        cipherAEAD
	header      []byte
	noncePrefix []byte
	chunkSize   int
	counter     uint64
	current     *bytes.Reader
	final       bool
}

func newEncryptedReader(source io.Reader, passphrase []byte) (*encryptedReader, error) {
	magic := make([]byte, len(archiveMagic))
	if _, err := io.ReadFull(source, magic); err != nil || string(magic) != archiveMagic {
		return nil, errors.New("state backup format header is invalid")
	}
	var headerLength uint32
	if err := binary.Read(source, binary.BigEndian, &headerLength); err != nil || headerLength == 0 || headerLength > maximumHeaderSize {
		return nil, errors.New("state backup encryption header is invalid")
	}
	headerBytes := make([]byte, headerLength)
	if _, err := io.ReadFull(source, headerBytes); err != nil {
		return nil, errors.New("state backup encryption header is truncated")
	}
	var header encryptionHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Format != 1 || header.KDF != "argon2id" || header.MemoryKiB != kdfMemoryKiB || header.Iterations != kdfIterations || header.Parallelism != kdfParallelism || header.ChunkSize != archiveChunkSize {
		return nil, errors.New("state backup encryption parameters are unsupported")
	}
	salt, err := base64.RawStdEncoding.DecodeString(header.Salt)
	if err != nil || len(salt) != 16 {
		return nil, errors.New("state backup encryption salt is invalid")
	}
	noncePrefix, err := base64.RawStdEncoding.DecodeString(header.NoncePrefix)
	if err != nil || len(noncePrefix) != chacha20poly1305.NonceSizeX-8 {
		return nil, errors.New("state backup encryption nonce is invalid")
	}
	key := argon2.IDKey(passphrase, salt, header.Iterations, header.MemoryKiB, header.Parallelism, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	for index := range key {
		key[index] = 0
	}
	if err != nil {
		return nil, err
	}
	return &encryptedReader{source: source, aead: aead, header: headerBytes, noncePrefix: noncePrefix, chunkSize: header.ChunkSize}, nil
}

func (reader *encryptedReader) Read(destination []byte) (int, error) {
	for {
		if reader.current != nil && reader.current.Len() > 0 {
			return reader.current.Read(destination)
		}
		if reader.final {
			trailing := make([]byte, 1)
			if count, err := reader.source.Read(trailing); count != 0 || !errors.Is(err, io.EOF) {
				return 0, errors.New("state backup contains trailing encrypted data")
			}
			return 0, io.EOF
		}
		flag := make([]byte, 1)
		if _, err := io.ReadFull(reader.source, flag); err != nil {
			return 0, errors.New("state backup is truncated before final authentication")
		}
		if flag[0] != 0 && flag[0] != 1 {
			return 0, errors.New("state backup encrypted chunk flag is invalid")
		}
		var ciphertextLength uint32
		if err := binary.Read(reader.source, binary.BigEndian, &ciphertextLength); err != nil || ciphertextLength < uint32(reader.aead.Overhead()) || ciphertextLength > uint32(reader.chunkSize+reader.aead.Overhead()) {
			return 0, errors.New("state backup encrypted chunk length is invalid")
		}
		ciphertext := make([]byte, ciphertextLength)
		if _, err := io.ReadFull(reader.source, ciphertext); err != nil {
			return 0, errors.New("state backup encrypted chunk is truncated")
		}
		plaintext, err := reader.aead.Open(nil, makeNonce(reader.noncePrefix, reader.counter), ciphertext, makeAAD(reader.header, flag[0], reader.counter))
		if err != nil {
			return 0, errors.New("state backup authentication failed")
		}
		reader.counter++
		if flag[0] == 1 {
			if len(plaintext) != 0 {
				return 0, errors.New("state backup final authentication record is invalid")
			}
			reader.final = true
			continue
		}
		if len(plaintext) == 0 {
			return 0, errors.New("state backup contains an empty non-final chunk")
		}
		reader.current = bytes.NewReader(plaintext)
	}
}

func makeNonce(prefix []byte, counter uint64) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	copy(nonce, prefix)
	binary.BigEndian.PutUint64(nonce[len(prefix):], counter)
	return nonce
}

func makeAAD(header []byte, flag byte, counter uint64) []byte {
	aad := make([]byte, 0, len(header)+9)
	aad = append(aad, header...)
	aad = append(aad, flag)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], counter)
	return append(aad, encoded[:]...)
}
