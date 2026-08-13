package statebackup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	stageRecordName    = "STAGE.json"
	maximumStages      = 4
	stageLifetime      = 24 * time.Hour
	maximumStageRecord = 1 << 20
)

type StageRequest struct {
	StateRoot            string
	ArchivePath          string
	Passphrase           []byte
	ConfirmBackupID      string
	MinimumSchemaVersion int
	MaximumSchemaVersion int
	ValidateStaged       func(context.Context, string, Manifest) error
	Now                  time.Time
	Random               io.Reader
}

type Stage struct {
	ID           string         `json:"id"`
	CreatedAt    time.Time      `json:"created_at"`
	ExpiresAt    time.Time      `json:"expires_at"`
	Manifest     Manifest       `json:"manifest"`
	PayloadFiles []FileManifest `json:"payload_files"`
}

func StageRestore(ctx context.Context, request StageRequest) (Stage, error) {
	stateRoot, archivePath, manifest, err := validateStageRequest(ctx, request)
	if err != nil {
		return Stage{}, err
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stagesRoot := stageRootFor(stateRoot)
	if err := os.MkdirAll(stagesRoot, 0o700); err != nil {
		return Stage{}, fmt.Errorf("create state restore staging root: %w", err)
	}
	_ = os.Chmod(stagesRoot, 0o700)
	stages, err := listStages(stagesRoot, now, true)
	if err != nil {
		return Stage{}, err
	}
	if len(stages) >= maximumStages {
		return Stage{}, errors.New("state restore staging capacity is full; discard an existing stage first")
	}
	random := request.Random
	if random == nil {
		random = rand.Reader
	}
	identifier, err := randomID(random)
	if err != nil {
		return Stage{}, fmt.Errorf("create state restore stage ID: %w", err)
	}
	directory := filepath.Join(stagesRoot, identifier)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return Stage{}, fmt.Errorf("create state restore stage: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(directory)
		}
	}()
	payload := filepath.Join(directory, "payload")
	if err := os.Mkdir(payload, 0o700); err != nil {
		return Stage{}, err
	}
	extractedManifest, err := extractArchive(ctx, archivePath, request.Passphrase, payload)
	if err != nil {
		return Stage{}, err
	}
	if extractedManifest.ID != manifest.ID || extractedManifest.SchemaVersion != manifest.SchemaVersion {
		return Stage{}, errors.New("state backup changed between validation and staging")
	}
	databasePath := filepath.Join(payload, "app.db")
	if err := verifyRestoredDatabase(databasePath, manifest.SchemaVersion); err != nil {
		return Stage{}, err
	}
	if err := revokeRestoredSessions(databasePath); err != nil {
		return Stage{}, err
	}
	if request.ValidateStaged != nil {
		if err := request.ValidateStaged(ctx, databasePath, manifest); err != nil {
			return Stage{}, fmt.Errorf("validate staged state backup: %w", err)
		}
	}
	payloadFiles, err := snapshotStagePayload(payload)
	if err != nil {
		return Stage{}, err
	}
	stage := Stage{ID: identifier, CreatedAt: now, ExpiresAt: now.Add(stageLifetime), Manifest: manifest, PayloadFiles: payloadFiles}
	if err := writeStageRecord(filepath.Join(directory, stageRecordName), stage); err != nil {
		return Stage{}, err
	}
	committed = true
	return stage, nil
}

func ListStages(stateRoot string, now time.Time) ([]Stage, error) {
	root, err := absoluteStateRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stagesRoot := stageRootFor(root)
	if _, err := os.Stat(stagesRoot); errors.Is(err, os.ErrNotExist) {
		return []Stage{}, nil
	} else if err != nil {
		return nil, err
	}
	return listStages(stagesRoot, now.UTC(), true)
}

// StagingRoot returns the protected sibling directory used for durable restore
// stages so other privileged services can exclude it from general file access.
func StagingRoot(stateRoot string) (string, error) {
	root, err := absoluteStateRoot(stateRoot)
	if err != nil {
		return "", err
	}
	return stageRootFor(root), nil
}

func DiscardStage(stateRoot, stageID string) error {
	root, err := absoluteStateRoot(stateRoot)
	if err != nil {
		return err
	}
	if !validStageID(stageID) {
		return errors.New("state restore stage ID is invalid")
	}
	target := filepath.Join(stageRootFor(root), stageID)
	if !withinPath(stageRootFor(root), target) {
		return errors.New("state restore stage path escapes staging root")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("state restore stage is not a regular directory")
	}
	return os.RemoveAll(target)
}

// VerifyStage authenticates the durable staging record against the exact payload
// that would later be committed by an offline restore operation.
func VerifyStage(stateRoot, stageID string, now time.Time) (Stage, error) {
	root, err := absoluteStateRoot(stateRoot)
	if err != nil {
		return Stage{}, err
	}
	if !validStageID(stageID) {
		return Stage{}, errors.New("state restore stage ID is invalid")
	}
	directory := filepath.Join(stageRootFor(root), stageID)
	stage, err := readStageRecord(filepath.Join(directory, stageRecordName))
	if err != nil || stage.ID != stageID {
		return Stage{}, errors.New("state restore stage record is invalid")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !now.UTC().Before(stage.ExpiresAt) {
		return Stage{}, errors.New("state restore stage has expired")
	}
	actual, err := snapshotStagePayload(filepath.Join(directory, "payload"))
	if err != nil || !equalFileManifests(actual, stage.PayloadFiles) {
		return Stage{}, errors.New("state restore stage payload failed verification")
	}
	return stage, nil
}

func validateStageRequest(ctx context.Context, request StageRequest) (string, string, Manifest, error) {
	stateRoot, err := absoluteStateRoot(request.StateRoot)
	if err != nil {
		return "", "", Manifest{}, err
	}
	archivePath := strings.TrimSpace(request.ArchivePath)
	if archivePath == "" || !filepath.IsAbs(archivePath) {
		return "", "", Manifest{}, errors.New("state restore archive path must be absolute")
	}
	archivePath, err = filepath.Abs(archivePath)
	if err != nil {
		return "", "", Manifest{}, err
	}
	archivePath, err = canonicalRegularFile(archivePath)
	if err != nil {
		return "", "", Manifest{}, err
	}
	if withinPath(stateRoot, archivePath) || withinPath(stageRootFor(stateRoot), archivePath) {
		return "", "", Manifest{}, errors.New("state restore archive must be outside State Root and staging")
	}
	manifest, err := Inspect(ctx, archivePath, request.Passphrase)
	if err != nil {
		return "", "", Manifest{}, err
	}
	if request.ConfirmBackupID == "" || request.ConfirmBackupID != manifest.ID {
		return "", "", Manifest{}, errors.New("state restore confirmation must exactly match the backup ID")
	}
	if request.MinimumSchemaVersion <= 0 || request.MaximumSchemaVersion < request.MinimumSchemaVersion || manifest.SchemaVersion < request.MinimumSchemaVersion || manifest.SchemaVersion > request.MaximumSchemaVersion {
		return "", "", Manifest{}, fmt.Errorf("state backup schema %d is outside supported range %d..%d", manifest.SchemaVersion, request.MinimumSchemaVersion, request.MaximumSchemaVersion)
	}
	return stateRoot, filepath.Clean(archivePath), manifest, nil
}

func absoluteStateRoot(raw string) (string, error) {
	root := strings.TrimSpace(raw)
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("state restore staging requires an absolute State Root")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("state restore staging requires an existing State Root")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("state restore staging cannot resolve State Root links")
	}
	return filepath.Clean(root), nil
}

func stageRootFor(stateRoot string) string {
	return filepath.Join(filepath.Dir(stateRoot), "."+filepath.Base(stateRoot)+".restore-stages")
}

func listStages(root string, now time.Time, cleanExpired bool) ([]Stage, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	stages := make([]Stage, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validStageID(entry.Name()) {
			return nil, fmt.Errorf("state restore staging contains unexpected entry %q", entry.Name())
		}
		directory := filepath.Join(root, entry.Name())
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("state restore stage directory is invalid")
		}
		stage, err := readStageRecord(filepath.Join(directory, stageRecordName))
		if err != nil || stage.ID != entry.Name() {
			return nil, fmt.Errorf("read state restore stage %q: %w", entry.Name(), err)
		}
		if !now.Before(stage.ExpiresAt) {
			if cleanExpired {
				if err := os.RemoveAll(directory); err != nil {
					return nil, err
				}
			}
			continue
		}
		stages = append(stages, stage)
	}
	sort.Slice(stages, func(i, j int) bool { return stages[i].CreatedAt.After(stages[j].CreatedAt) })
	return stages, nil
}

func writeStageRecord(path string, stage Stage) error {
	body, err := json.Marshal(stage)
	if err != nil || len(body) > maximumStageRecord {
		return errors.New("encode state restore stage record")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func readStageRecord(path string) (Stage, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumStageRecord {
		return Stage{}, errors.New("state restore stage record is invalid")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Stage{}, err
	}
	var stage Stage
	if err := decodeStrictJSON(body, &stage); err != nil {
		return Stage{}, err
	}
	if !validStageID(stage.ID) || stage.CreatedAt.IsZero() || !stage.ExpiresAt.After(stage.CreatedAt) || stage.ExpiresAt.Sub(stage.CreatedAt) != stageLifetime || stage.Manifest.ID == "" || !validStageFileManifests(stage.PayloadFiles) {
		return Stage{}, errors.New("state restore stage record contains invalid values")
	}
	return stage, nil
}

func snapshotStagePayload(root string) ([]FileManifest, error) {
	files := make([]FileManifest, 0)
	var total int64
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("state restore stage payload contains a symbolic link")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("state restore stage payload contains a non-regular file")
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		name, err := validateArchivePath(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		if len(files) >= maximumArchiveFile || info.Size() < 0 || info.Size() > maximumArchiveSize-total {
			return errors.New("state restore stage payload exceeds safety limits")
		}
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != info.Size() {
			return errors.New("read complete state restore stage payload")
		}
		total += info.Size()
		files = append(files, FileManifest{Path: name, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if !validStageFileManifests(files) {
		return nil, errors.New("state restore stage payload file set is invalid")
	}
	return files, nil
}

func validStageFileManifests(files []FileManifest) bool {
	if len(files) == 0 {
		return false
	}
	seenDatabase := false
	previous := ""
	for _, file := range files {
		name, err := validateArchivePath(file.Path)
		if err != nil || name <= previous || file.Size < 0 || len(file.SHA256) != sha256.Size*2 || file.SHA256 != strings.ToLower(file.SHA256) {
			return false
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return false
		}
		seenDatabase = seenDatabase || name == "app.db"
		previous = name
	}
	return seenDatabase
}

func equalFileManifests(left, right []FileManifest) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validStageID(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 18 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func decodeStrictJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON record contains trailing data")
	}
	return nil
}
