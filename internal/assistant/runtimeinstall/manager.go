package runtimeinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/assistant/pirpc"
	"scriptboard/internal/diskspace"
	updatepkg "scriptboard/internal/update"
)

var (
	ErrRuntimeBusy      = errors.New("assistant runtime cannot switch while AI work is active")
	ErrOperationActive  = errors.New("another assistant runtime operation is active")
	ErrRollbackMissing  = errors.New("no assistant runtime rollback version is available")
	ErrArchiveUntrusted = errors.New("assistant runtime archive does not match its signed manifest")
)

type RuntimeMetadata struct {
	Schema         int    `json:"schema"`
	Product        string `json:"product"`
	PiVersion      string `json:"piVersion"`
	RPCContract    int    `json:"rpcContract"`
	BrokerContract int    `json:"brokerContract"`
	Executable     string `json:"executable"`
	Extension      string `json:"extension"`
	Upstream       string `json:"upstream"`
	UpstreamCommit string `json:"upstreamCommit"`
}

type Candidate struct {
	StateRoot, Root, Version, Executable, Extension string
}

type Version struct {
	Version  string `json:"version"`
	Active   bool   `json:"active"`
	Rollback bool   `json:"rollback"`
}

type Snapshot struct {
	Installed       bool      `json:"installed"`
	ActiveVersion   string    `json:"activeVersion"`
	RollbackVersion string    `json:"rollbackVersion"`
	Versions        []Version `json:"versions"`
	Operation       string    `json:"operation"`
	LastErrorCode   string    `json:"lastErrorCode"`
	LastOperationAt time.Time `json:"lastOperationAt"`
	CheckedAt       time.Time `json:"checkedAt"`
	OnlineVersion   string    `json:"onlineVersion"`
}

type Config struct {
	StateRoot     string
	Compatibility Compatibility
	Verify        func([]byte, []byte, Compatibility) (Manifest, error)
	HealthCheck   func(context.Context, Candidate) error
	DiskCheck     func(string, uint64) error
	SwitchGuard   func(context.Context) error
	Protected     func(context.Context) ([]string, error)
	Now           func() time.Time
	GOOS, GOARCH  string
	Source        Source
}

type Manager struct {
	stateRoot, runtimeRoot string
	compatibility          Compatibility
	verify                 func([]byte, []byte, Compatibility) (Manifest, error)
	healthCheck            func(context.Context, Candidate) error
	diskCheck              func(string, uint64) error
	switchGuard            func(context.Context) error
	protected              func(context.Context) ([]string, error)
	now                    func() time.Time
	goos, goarch           string
	source                 Source

	mu              sync.Mutex
	operation       string
	lastErrorCode   string
	lastOperationAt time.Time
	checkedAt       time.Time
	online          *Remote
}

func NewManager(config Config) *Manager {
	verify := config.Verify
	if verify == nil {
		verify = VerifyTrustedManifest
	}
	health := config.HealthCheck
	if health == nil {
		health = healthCheckCandidate
	}
	diskCheck := config.DiskCheck
	if diskCheck == nil {
		diskCheck = diskspace.Require
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	goos, goarch := config.GOOS, config.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	stateRoot := filepath.Clean(config.StateRoot)
	return &Manager{
		stateRoot: stateRoot, runtimeRoot: filepath.Join(stateRoot, "assistant", "runtime"),
		compatibility: config.Compatibility, verify: verify, healthCheck: health, diskCheck: diskCheck,
		switchGuard: config.SwitchGuard, protected: config.Protected, now: now, goos: goos, goarch: goarch,
		source: config.Source,
	}
}

func (manager *Manager) Snapshot() Snapshot {
	manager.mu.Lock()
	snapshot := Snapshot{
		Operation: manager.operation, LastErrorCode: manager.lastErrorCode, LastOperationAt: manager.lastOperationAt,
		CheckedAt: manager.checkedAt,
	}
	if manager.online != nil {
		snapshot.OnlineVersion = manager.online.Manifest.PiVersion
	}
	manager.mu.Unlock()
	active, err := pirpc.ResolveActiveRuntime(manager.stateRoot)
	if err == nil {
		snapshot.Installed = true
		snapshot.ActiveVersion = active.Version
		snapshot.RollbackVersion = active.RollbackVersion
	}
	entries, _ := os.ReadDir(filepath.Join(manager.runtimeRoot, "versions"))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, err := manager.validateCandidate(entry.Name()); err != nil {
			continue
		}
		snapshot.Versions = append(snapshot.Versions, Version{
			Version: entry.Name(), Active: entry.Name() == snapshot.ActiveVersion, Rollback: entry.Name() == snapshot.RollbackVersion,
		})
	}
	sort.Slice(snapshot.Versions, func(i, j int) bool { return snapshot.Versions[i].Version > snapshot.Versions[j].Version })
	return snapshot
}

func (manager *Manager) InstallOffline(ctx context.Context, manifestRaw, signatureRaw []byte, archive io.Reader) error {
	if archive == nil {
		return fmt.Errorf("%w: archive is required", ErrArchiveUntrusted)
	}
	if err := manager.begin("installing"); err != nil {
		return err
	}
	var resultErr error
	defer func() { manager.finish(resultErr) }()
	manifest, err := manager.verify(manifestRaw, signatureRaw, manager.compatibility)
	if err != nil {
		resultErr = err
		return err
	}
	asset, ok := manifest.AssetFor(manager.goos, manager.goarch)
	if !ok {
		resultErr = errors.New("signed runtime manifest has no asset for this platform")
		return resultErr
	}
	if err := manager.guard(ctx); err != nil {
		resultErr = err
		return err
	}
	required := uint64(asset.Size) + uint64(asset.UnpackedSize) + diskspace.MinimumWritableBytes
	if err := manager.diskCheck(manager.stateRoot, required); err != nil {
		resultErr = err
		return err
	}
	operationID, err := randomOperationID()
	if err != nil {
		resultErr = err
		return err
	}
	downloadRoot := filepath.Join(manager.stateRoot, "assistant", "downloads", operationID)
	if err := os.MkdirAll(downloadRoot, 0o700); err != nil {
		resultErr = err
		return err
	}
	defer os.RemoveAll(downloadRoot)
	archivePath := filepath.Join(downloadRoot, asset.Name)
	if err := copyVerifiedArchive(ctx, archivePath, archive, asset); err != nil {
		resultErr = err
		return err
	}
	if err := manager.installVerifiedArchive(ctx, manifest, asset, archivePath, operationID); err != nil {
		resultErr = err
		return err
	}
	return nil
}

func (manager *Manager) CheckOnline(ctx context.Context) (Snapshot, error) {
	if manager.source == nil {
		return manager.Snapshot(), errors.New("assistant runtime online source is unavailable")
	}
	if err := manager.begin("checking"); err != nil {
		return manager.Snapshot(), err
	}
	remote, err := manager.source.Fetch(ctx, manager.compatibility)
	if err != nil {
		manager.finish(err)
		return manager.Snapshot(), err
	}
	manifest, err := manager.verify(remote.ManifestRaw, remote.SignatureRaw, manager.compatibility)
	if err != nil {
		manager.finish(err)
		return manager.Snapshot(), err
	}
	asset, ok := manifest.AssetFor(manager.goos, manager.goarch)
	if !ok || remote.AssetURLs[asset.Name] == "" || remote.AssetSizes[asset.Name] != asset.Size {
		err = errors.New("GitHub runtime assets do not match the signed manifest")
		manager.finish(err)
		return manager.Snapshot(), err
	}
	remote.Manifest = manifest
	manager.mu.Lock()
	manager.online = &remote
	manager.checkedAt = manager.now().UTC()
	manager.mu.Unlock()
	manager.finish(nil)
	return manager.Snapshot(), nil
}

func (manager *Manager) InstallOnline(ctx context.Context) error {
	if manager.source == nil {
		return errors.New("assistant runtime online source is unavailable")
	}
	manager.mu.Lock()
	var remote *Remote
	if manager.online != nil {
		copy := *manager.online
		remote = &copy
	}
	manager.mu.Unlock()
	if remote == nil {
		if _, err := manager.CheckOnline(ctx); err != nil {
			return err
		}
		manager.mu.Lock()
		copy := *manager.online
		remote = &copy
		manager.mu.Unlock()
	}
	asset, ok := remote.Manifest.AssetFor(manager.goos, manager.goarch)
	if !ok {
		return errors.New("signed runtime manifest has no asset for this platform")
	}
	reader, err := manager.source.Open(ctx, remote.AssetURLs[asset.Name], remote.ReleaseTag, asset)
	if err != nil {
		return err
	}
	defer reader.Close()
	return manager.InstallOffline(ctx, remote.ManifestRaw, remote.SignatureRaw, reader)
}

func (manager *Manager) installVerifiedArchive(ctx context.Context, manifest Manifest, asset Asset, archivePath, operationID string) error {
	staging := filepath.Join(manager.runtimeRoot, "staging", operationID)
	if err := updatepkg.ExtractArchive(archivePath, staging, asset.UnpackedSize); err != nil {
		return fmt.Errorf("extract assistant runtime: %w", err)
	}
	defer os.RemoveAll(staging)
	candidate, err := validateRuntimeDirectory(manager.stateRoot, staging, manifest.PiVersion)
	if err != nil {
		return err
	}
	if err := manager.healthCheck(ctx, candidate); err != nil {
		return fmt.Errorf("assistant runtime health check failed: %w", err)
	}
	if err := manager.guard(ctx); err != nil {
		return err
	}
	versionsRoot := filepath.Join(manager.runtimeRoot, "versions")
	if err := os.MkdirAll(versionsRoot, 0o700); err != nil {
		return err
	}
	versionRoot := filepath.Join(versionsRoot, manifest.PiVersion)
	if _, err := os.Lstat(versionRoot); err == nil {
		return fmt.Errorf("assistant runtime %s is already installed", manifest.PiVersion)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staging, versionRoot); err != nil {
		return fmt.Errorf("publish assistant runtime: %w", err)
	}
	active, activeErr := pirpc.ResolveActiveRuntime(manager.stateRoot)
	rollback := ""
	if activeErr == nil && active.Version != manifest.PiVersion {
		rollback = active.Version
	}
	metadata, err := readRuntimeMetadata(versionRoot)
	if err != nil {
		return err
	}
	if err := pirpc.WriteActiveRuntime(manager.stateRoot, pirpc.ActivePointer{
		Version: manifest.PiVersion, RollbackVersion: rollback, RPCContract: metadata.RPCContract,
		Executable: metadata.Executable, Extension: metadata.Extension,
	}); err != nil {
		return fmt.Errorf("activate assistant runtime: %w", err)
	}
	_ = manager.cleanup(ctx)
	return nil
}

func (manager *Manager) Rollback(ctx context.Context) error {
	if err := manager.begin("rolling_back"); err != nil {
		return err
	}
	var resultErr error
	defer func() { manager.finish(resultErr) }()
	if err := manager.guard(ctx); err != nil {
		resultErr = err
		return err
	}
	active, err := pirpc.ResolveActiveRuntime(manager.stateRoot)
	if err != nil {
		resultErr = err
		return err
	}
	if active.RollbackVersion == "" {
		resultErr = ErrRollbackMissing
		return resultErr
	}
	candidate, err := manager.validateCandidate(active.RollbackVersion)
	if err != nil {
		resultErr = err
		return err
	}
	if err := manager.healthCheck(ctx, candidate); err != nil {
		resultErr = err
		return err
	}
	if err := manager.guard(ctx); err != nil {
		resultErr = err
		return err
	}
	metadata, err := readRuntimeMetadata(candidate.Root)
	if err != nil {
		resultErr = err
		return err
	}
	resultErr = pirpc.WriteActiveRuntime(manager.stateRoot, pirpc.ActivePointer{
		Version: active.RollbackVersion, RollbackVersion: active.Version, RPCContract: metadata.RPCContract,
		Executable: metadata.Executable, Extension: metadata.Extension,
	})
	return resultErr
}

func (manager *Manager) Cleanup(ctx context.Context) error {
	if err := manager.begin("cleaning"); err != nil {
		return err
	}
	var resultErr error
	defer func() { manager.finish(resultErr) }()
	resultErr = manager.cleanup(ctx)
	return resultErr
}

func (manager *Manager) cleanup(ctx context.Context) error {
	active, _ := pirpc.ResolveActiveRuntime(manager.stateRoot)
	keep := map[string]struct{}{active.Version: {}, active.RollbackVersion: {}}
	delete(keep, "")
	if manager.protected != nil {
		versions, err := manager.protected(ctx)
		if err != nil {
			return err
		}
		for _, version := range versions {
			keep[version] = struct{}{}
		}
	}
	entries, err := os.ReadDir(filepath.Join(manager.runtimeRoot, "versions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, retained := keep[entry.Name()]; retained {
			continue
		}
		if _, err := manager.validateCandidate(entry.Name()); err != nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(manager.runtimeRoot, "versions", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (manager *Manager) validateCandidate(version string) (Candidate, error) {
	root := filepath.Join(manager.runtimeRoot, "versions", version)
	return validateRuntimeDirectory(manager.stateRoot, root, version)
}

func validateRuntimeDirectory(stateRoot, root, expectedVersion string) (Candidate, error) {
	metadata, err := readRuntimeMetadata(root)
	if err != nil {
		return Candidate{}, err
	}
	if metadata.Schema != 1 || metadata.Product != Product || metadata.PiVersion != expectedVersion ||
		metadata.RPCContract != RPCContract || metadata.BrokerContract != BrokerContract ||
		metadata.Executable != runtimeExecutableName() || metadata.Extension != "scriptboard-extension.ts" ||
		metadata.Upstream != "https://github.com/earendil-works/pi/releases/tag/v"+expectedVersion ||
		len(metadata.UpstreamCommit) != 40 || !validLowerHex(metadata.UpstreamCommit) {
		return Candidate{}, errors.New("assistant runtime metadata is incompatible")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Candidate{}, errors.New("assistant runtime root is unsafe")
	}
	required := []string{metadata.Executable, metadata.Extension, "LICENSE", "runtime.json"}
	for _, name := range required {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 {
			return Candidate{}, fmt.Errorf("assistant runtime file %q is missing or unsafe", name)
		}
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("assistant runtime contains unsafe entry %q", entry.Name())
		}
		if path == filepath.Join(root, metadata.Executable) {
			return nil
		}
		lower := strings.ToLower(entry.Name())
		if !info.IsDir() && (info.Mode().Perm()&0o111 != 0 || strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".ps1")) {
			return fmt.Errorf("assistant runtime contains unexpected executable %q", entry.Name())
		}
		return nil
	})
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		StateRoot: stateRoot, Root: root, Version: metadata.PiVersion,
		Executable: filepath.Join(root, metadata.Executable), Extension: filepath.Join(root, metadata.Extension),
	}, nil
}

func validLowerHex(value string) bool {
	if value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func readRuntimeMetadata(root string) (RuntimeMetadata, error) {
	path := filepath.Join(root, "runtime.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 16<<10 {
		return RuntimeMetadata{}, errors.New("assistant runtime metadata is missing or unsafe")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return RuntimeMetadata{}, err
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return RuntimeMetadata{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var metadata RuntimeMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return RuntimeMetadata{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return RuntimeMetadata{}, err
	}
	return metadata, nil
}

func copyVerifiedArchive(ctx context.Context, destination string, source io.Reader, asset Asset) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := copyWithContext(ctx, io.MultiWriter(file, hash), io.LimitReader(source, asset.Size+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return ErrArchiveUntrusted
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func (manager *Manager) guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if manager.switchGuard != nil {
		return manager.switchGuard(ctx)
	}
	return nil
}

func (manager *Manager) begin(operation string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.operation != "" {
		return ErrOperationActive
	}
	manager.operation = operation
	manager.lastErrorCode = ""
	return nil
}

func (manager *Manager) finish(err error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.operation = ""
	manager.lastOperationAt = manager.now().UTC()
	if err != nil {
		manager.lastErrorCode = classifyRuntimeError(err)
	}
}

func classifyRuntimeError(err error) string {
	switch {
	case errors.Is(err, ErrRuntimeBusy):
		return "runtime_busy"
	case errors.Is(err, ErrArchiveUntrusted):
		return "runtime_archive_untrusted"
	case errors.Is(err, ErrRollbackMissing):
		return "runtime_rollback_unavailable"
	default:
		return "runtime_install_failed"
	}
}

func randomOperationID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func runtimeExecutableName() string {
	if runtime.GOOS == "windows" {
		return "pi.exe"
	}
	return "pi"
}
