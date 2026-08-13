package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/buildinfo"
	"scriptboard/internal/diskspace"
	"scriptboard/internal/installation"
	"scriptboard/internal/platformservice"
	"scriptboard/internal/secretredaction"
)

type ManagerConfig struct {
	StateRoot       string
	CheckEnabled    bool
	CheckInterval   time.Duration
	Source          ReleaseSource
	Sources         map[string]ReleaseSource
	Now             func() time.Time
	RequestShutdown func()
	Build           *buildinfo.Info
}

type Snapshot struct {
	Build            buildinfo.Info `json:"build"`
	InstallMode      string         `json:"install_mode"`
	SourceID         string         `json:"source_id"`
	CanApply         bool           `json:"can_apply"`
	Capability       string         `json:"capability"`
	CheckEnabled     bool           `json:"check_enabled"`
	CheckedAt        time.Time      `json:"checked_at"`
	NextCheckAt      time.Time      `json:"next_check_at"`
	ReleaseURL       string         `json:"release_url"`
	ReleaseNotes     string         `json:"release_notes"`
	Latest           *Manifest      `json:"latest,omitempty"`
	ManifestVerified bool           `json:"manifest_verified"`
	UpdateAvailable  bool           `json:"update_available"`
	LocalNewer       bool           `json:"local_newer"`
	LastError        string         `json:"last_error"`
	Operation        *Operation     `json:"operation,omitempty"`
}

type Manager struct {
	stateRoot        string
	build            buildinfo.Info
	checkEnabled     bool
	checkInterval    time.Duration
	source           ReleaseSource
	sources          map[string]ReleaseSource
	selectedSource   string
	now              func() time.Time
	requestShutdown  func()
	mu               sync.Mutex
	lastError        string
	lastForcedCheck  time.Time
	lastForcedSource string
}

func NewManager(config ManagerConfig) *Manager {
	if config.CheckInterval <= 0 {
		config.CheckInterval = DefaultCheckInterval
	}
	sources := config.Sources
	if len(sources) == 0 {
		if config.Source != nil {
			sources = map[string]ReleaseSource{SourceGitHub: config.Source}
		} else {
			sources = defaultSources()
		}
	}
	if config.Source == nil {
		config.Source = sources[SourceGitHub]
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	build := buildinfo.Current()
	if config.Build != nil {
		build = *config.Build
	}
	manager := &Manager{
		stateRoot: config.StateRoot, build: build,
		checkEnabled: config.CheckEnabled, checkInterval: config.CheckInterval,
		source: config.Source, sources: sources, selectedSource: SourceGitHub,
		now: config.Now, requestShutdown: config.RequestShutdown,
	}
	if cache, err := loadCache(config.StateRoot); err == nil {
		if _, ok := sources[cache.SourceID]; ok {
			manager.selectedSource = cache.SourceID
		}
	}
	return manager
}

func (manager *Manager) Start(ctx context.Context) {
	if !manager.checkEnabled || !manager.build.ValidRelease() {
		return
	}
	go func() {
		delay := time.Duration(manager.now().UnixNano() % int64(10*time.Minute+1))
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _ = manager.Check(ctx, false)
		}
		ticker := time.NewTicker(manager.checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = manager.Check(ctx, false)
			}
		}
	}()
}

func (manager *Manager) Snapshot() Snapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.snapshotLocked()
}

func (manager *Manager) Check(ctx context.Context, force bool) (Snapshot, error) {
	return manager.CheckFrom(ctx, force, "")
}

func (manager *Manager) CheckFrom(ctx context.Context, force bool, requestedSource string) (Snapshot, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !manager.build.ValidRelease() {
		return manager.snapshotLocked(), errors.New("development builds do not check for updates")
	}
	sourceID := manager.selectedSource
	if requestedSource != "" {
		var err error
		sourceID, err = normalizeSourceID(requestedSource)
		if err != nil {
			return manager.snapshotLocked(), err
		}
		if _, ok := manager.sources[sourceID]; !ok {
			return manager.snapshotLocked(), errors.New("update source is unavailable")
		}
	}
	source := manager.sources[sourceID]
	if source == nil {
		sourceID = SourceGitHub
		source = manager.source
		manager.selectedSource = sourceID
	}
	attemptedAt := manager.now()
	if force && sourceID == manager.lastForcedSource && !manager.lastForcedCheck.IsZero() {
		elapsed := attemptedAt.Sub(manager.lastForcedCheck)
		if elapsed >= 0 && elapsed < time.Minute {
			return manager.snapshotLocked(), fmt.Errorf("wait %s before checking again", (time.Minute - elapsed).Round(time.Second))
		}
	}
	if force {
		manager.lastForcedCheck = attemptedAt
		manager.lastForcedSource = sourceID
	}
	cache, cacheErr := loadCache(manager.stateRoot)
	if cacheErr == nil && cache.SourceID == sourceID && !force {
		checked, _ := time.Parse(time.RFC3339Nano, cache.CheckedAt)
		if attemptedAt.Sub(checked) < manager.checkInterval {
			return manager.snapshotLocked(), nil
		}
	}
	etag := ""
	if cacheErr == nil && cache.SourceID == sourceID {
		etag = cache.ETag
	}
	remote, err := source.Check(ctx, etag)
	now := attemptedAt.UTC().Format(time.RFC3339Nano)
	if err != nil {
		manager.lastError = secretredaction.String(err.Error())
		_ = saveCheckState(manager.stateRoot, now, err.Error())
		if cacheErr == nil && cache.SourceID == sourceID {
			cache.CheckedAt = now
			cache.LastError = secretredaction.String(err.Error())
			_ = saveCache(manager.stateRoot, cache)
		}
		return manager.snapshotLocked(), err
	}
	if remote.NotModified {
		if cacheErr != nil {
			err := errors.New("GitHub returned not modified without a valid local cache")
			manager.lastError = secretredaction.String(err.Error())
			_ = saveCheckState(manager.stateRoot, now, err.Error())
			return manager.snapshotLocked(), err
		}
		cache.CheckedAt = now
		cache.LastError = ""
		if remote.ETag != "" {
			cache.ETag = remote.ETag
		}
		if err := saveCache(manager.stateRoot, cache); err != nil {
			return manager.snapshotLocked(), err
		}
		manager.lastError = ""
		manager.selectedSource = sourceID
		_ = saveCheckState(manager.stateRoot, now, "")
		return manager.snapshotLocked(), nil
	}
	cache = Cache{
		Schema: ManifestSchema, SourceID: sourceID, ETag: remote.ETag, CheckedAt: now,
		ReleaseURL: remote.ReleaseURL, ReleaseNotes: remote.ReleaseNotes,
		ManifestRaw: remote.ManifestRaw, SignatureRaw: remote.SignatureRaw,
		Manifest: remote.Manifest, AssetURLs: remote.AssetURLs,
	}
	if err := saveCache(manager.stateRoot, cache); err != nil {
		return manager.snapshotLocked(), err
	}
	manager.lastError = ""
	manager.selectedSource = sourceID
	_ = saveCheckState(manager.stateRoot, now, "")
	return manager.snapshotLocked(), nil
}

func (manager *Manager) Prepare(ctx context.Context) (Operation, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !manager.build.ValidRelease() {
		return Operation{}, errors.New("development builds cannot prepare updates")
	}
	metadata, err := manager.managedInstallation()
	if err != nil {
		return Operation{}, errors.New("only a managed service installation can apply updates")
	}
	if active, err := LoadActive(manager.stateRoot); err == nil {
		if operation, err := LoadOperation(manager.stateRoot, active.OperationID); err == nil {
			switch operation.Phase {
			case PhaseCommitted, PhaseRolledBack, PhaseFailedSafe:
			default:
				return Operation{}, fmt.Errorf("update operation %s is already %s", operation.ID, operation.Phase)
			}
		}
	}
	cache, err := loadCache(manager.stateRoot)
	if err != nil {
		return Operation{}, errors.New("check for updates before preparing an update")
	}
	if compareVersions(cache.Manifest.Version, manager.build.Version) <= 0 {
		return Operation{}, errors.New("no newer stable release is available")
	}
	if cache.Manifest.MinimumUpdaterProtocol > manager.build.UpdaterProtocolVersion {
		return Operation{}, errors.New("the available release requires a newer updater; install it manually")
	}
	asset, ok := cache.Manifest.AssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return Operation{}, errors.New("release does not contain an asset for this platform")
	}
	downloadURL := cache.AssetURLs[asset.Name]
	if downloadURL == "" {
		return Operation{}, errors.New("validated release cache is missing the platform asset URL")
	}
	requiredBytes := uint64(asset.Size) + 2*uint64(asset.UnpackedSize) + diskspace.MinimumWritableBytes
	if database, statErr := os.Stat(filepath.Join(manager.stateRoot, "app.db")); statErr == nil {
		requiredBytes += uint64(database.Size())
	}
	if err := diskspace.Require(manager.stateRoot, requiredBytes); err != nil {
		return Operation{}, fmt.Errorf("update State Root: %w", err)
	}
	if err := diskspace.Require(metadata.InstallRoot, requiredBytes); err != nil {
		return Operation{}, fmt.Errorf("update Install Root: %w", err)
	}
	id, err := NewOperationID()
	if err != nil {
		return Operation{}, err
	}
	nonce, err := NewOperationID()
	if err != nil {
		return Operation{}, err
	}
	root, _ := OperationDirectory(manager.stateRoot, id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Operation{}, err
	}
	archivePath := filepath.Join(root, asset.Name)
	source := manager.sources[cache.SourceID]
	if source == nil {
		return Operation{}, errors.New("the selected update source is unavailable")
	}
	if err := source.Download(ctx, downloadURL, archivePath, asset); err != nil {
		return Operation{}, err
	}
	extractedPath := filepath.Join(root, "extracted")
	if err := ExtractArchive(archivePath, extractedPath, asset.UnpackedSize); err != nil {
		return Operation{}, err
	}
	if err := os.WriteFile(filepath.Join(root, ManifestFilename), cache.ManifestRaw, 0o600); err != nil {
		return Operation{}, err
	}
	if err := os.WriteFile(filepath.Join(root, SignatureFilename), cache.SignatureRaw, 0o600); err != nil {
		return Operation{}, err
	}
	targetBuild := buildinfo.Info{
		Version: cache.Manifest.Version, Tag: cache.Manifest.Tag, Commit: cache.Manifest.Commit,
		BuiltAt: cache.Manifest.PublishedAt, ReleaseBuild: true,
		DatabaseSchemaVersion:  cache.Manifest.DatabaseSchema,
		UpdaterProtocolVersion: cache.Manifest.UpdaterProtocol, Repository: cache.Manifest.Repository,
	}
	if err := installation.StageVersion(metadata, extractedPath, targetBuild); err != nil {
		return Operation{}, err
	}
	now := manager.now().UTC().Format(time.RFC3339Nano)
	operation := Operation{
		Schema: OperationSchema, ID: id, Nonce: nonce, Phase: PhasePrepared,
		PreviousVersion: manager.build.Version, TargetVersion: cache.Manifest.Version,
		PreviousCommit: manager.build.Commit, TargetCommit: cache.Manifest.Commit,
		InstallRoot: metadata.InstallRoot, StateRoot: manager.stateRoot, ConfigPath: metadata.ConfigPath,
		DatabasePath: filepath.Join(manager.stateRoot, "app.db"), ArchivePath: archivePath,
		ExtractedPath: extractedPath, SnapshotPath: filepath.Join(root, "database-before-update.db"),
		Manifest: cache.Manifest, CreatedAt: now, UpdatedAt: now,
	}
	if err := SaveOperation(operation); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func (manager *Manager) Handoff(operationID string) (Operation, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	operation, err := LoadOperation(manager.stateRoot, operationID)
	if err != nil {
		return Operation{}, err
	}
	if operation.Phase != PhasePrepared {
		return Operation{}, fmt.Errorf("operation cannot be applied from phase %s", operation.Phase)
	}
	metadata, err := manager.managedInstallation()
	if err != nil {
		return Operation{}, err
	}
	if manager.requestShutdown == nil {
		return Operation{}, errors.New("service host does not provide a shutdown callback")
	}
	updaterExecutable, err := prepareHandoffUpdater(metadata, operation)
	if err != nil {
		return Operation{}, err
	}
	operation.OldPID = os.Getpid()
	operation.Phase = PhaseHandoff
	operation.Error = ""
	if err := SaveOperation(operation); err != nil {
		return Operation{}, err
	}
	if err := platformservice.StartUpdater(updaterExecutable, manager.stateRoot, operation.ID); err != nil {
		operation.Phase = PhaseFailedSafe
		operation.Error = secretredaction.String(err.Error())
		_ = SaveOperation(operation)
		return Operation{}, err
	}
	time.AfterFunc(250*time.Millisecond, manager.requestShutdown)
	return operation, nil
}

func prepareHandoffUpdater(metadata installation.Metadata, operation Operation) (string, error) {
	source := installation.UpdaterExecutable(metadata)
	if runtime.GOOS != "windows" {
		target := installation.ServiceUpdaterExecutable(metadata)
		if err := copyExecutableAtomic(source, target); err != nil {
			return "", err
		}
		return target, nil
	}
	root, err := OperationDirectory(operation.StateRoot, operation.ID)
	if err != nil {
		return "", err
	}
	helperRoot := filepath.Join(root, "helper")
	if err := os.MkdirAll(helperRoot, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(helperRoot, "scriptboard-updater.exe")
	if err := copyExecutableAtomic(source, target); err != nil {
		return "", err
	}
	return target, nil
}

func copyExecutableAtomic(source, target string) error {
	temporary := target + ".tmp"
	_ = os.Remove(temporary)
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		_ = input.Close()
		return err
	}
	_, copyErr := io.Copy(output, input)
	inputErr := input.Close()
	syncErr := output.Sync()
	outputErr := output.Close()
	for _, candidate := range []error{copyErr, inputErr, syncErr, outputErr} {
		if candidate != nil {
			_ = os.Remove(temporary)
			return candidate
		}
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(target)
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func (manager *Manager) snapshotLocked() Snapshot {
	snapshot := Snapshot{Build: manager.build, CheckEnabled: manager.checkEnabled, InstallMode: "portable", SourceID: manager.selectedSource}
	snapshot.LastError = manager.lastError
	if !manager.build.ValidRelease() {
		snapshot.InstallMode = "development"
		snapshot.Capability = "Development builds do not participate in updates."
	} else if _, err := manager.managedInstallation(); err == nil {
		snapshot.InstallMode = "managed-service"
		snapshot.CanApply = true
	} else {
		snapshot.Capability = "Portable installations can check releases but cannot apply updates."
	}
	if cache, err := loadCache(manager.stateRoot); err == nil {
		snapshot.ReleaseURL = cache.ReleaseURL
		snapshot.ReleaseNotes = cache.ReleaseNotes
		snapshot.Latest = &cache.Manifest
		snapshot.ManifestVerified = true
		if snapshot.LastError == "" {
			snapshot.LastError = cache.LastError
		}
		snapshot.CheckedAt, _ = time.Parse(time.RFC3339Nano, cache.CheckedAt)
		comparison := compareVersions(cache.Manifest.Version, manager.build.Version)
		snapshot.UpdateAvailable = comparison > 0
		snapshot.LocalNewer = comparison < 0
	}
	if check, err := loadCheckState(manager.stateRoot); err == nil {
		snapshot.CheckedAt, _ = time.Parse(time.RFC3339Nano, check.CheckedAt)
		if snapshot.LastError == "" {
			snapshot.LastError = check.LastError
		}
	}
	if snapshot.CheckEnabled && !snapshot.CheckedAt.IsZero() {
		snapshot.NextCheckAt = snapshot.CheckedAt.Add(manager.checkInterval)
	}
	if active, err := LoadActive(manager.stateRoot); err == nil {
		if operation, err := LoadOperation(manager.stateRoot, active.OperationID); err == nil {
			snapshot.Operation = &operation
		}
	}
	return snapshot
}

func (manager *Manager) Sources() []SourceDescriptor {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	result := make([]SourceDescriptor, 0, len(sourceDescriptors))
	for _, descriptor := range sourceDescriptors {
		if manager.sources[descriptor.ID] != nil {
			result = append(result, descriptor)
		}
	}
	return result
}

func (manager *Manager) managedInstallation() (installation.Metadata, error) {
	metadata, err := installation.Detect(manager.stateRoot)
	if err != nil {
		return installation.Metadata{}, err
	}
	if err := installation.ValidateVersion(metadata, metadata.Current, manager.build); err != nil {
		return installation.Metadata{}, err
	}
	matches, err := platformservice.MatchesExecutable(installation.ServiceEntryExecutable(metadata), metadata.ConfigPath, metadata.StateRoot)
	if err != nil {
		return installation.Metadata{}, err
	}
	if !matches {
		return installation.Metadata{}, errors.New("managed service target does not match Install Root metadata")
	}
	return metadata, nil
}

func compareVersions(first, second string) int {
	left, right := strings.Split(first, "."), strings.Split(second, ".")
	if len(left) != 3 || len(right) != 3 {
		return 0
	}
	for index := range left {
		if len(left[index]) < len(right[index]) {
			return -1
		}
		if len(left[index]) > len(right[index]) {
			return 1
		}
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}
