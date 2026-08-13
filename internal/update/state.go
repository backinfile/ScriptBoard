package update

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"scriptboard/internal/secretredaction"
)

const OperationSchema = 1

type Phase string

const (
	PhasePrepared          Phase = "prepared"
	PhaseHandoff           Phase = "handoff"
	PhaseWaitingForOldExit Phase = "waiting_for_old_exit"
	PhaseSnapshotting      Phase = "snapshotting"
	PhaseSwitching         Phase = "switching"
	PhaseStartingTarget    Phase = "starting_target"
	PhaseValidating        Phase = "validating"
	PhaseCommitted         Phase = "committed"
	PhaseRollingBack       Phase = "rolling_back"
	PhaseRolledBack        Phase = "rolled_back"
	PhaseFailedSafe        Phase = "failed_safe"
	PhaseNeedsRecovery     Phase = "needs_recovery"
)

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,64}$`)

type Operation struct {
	Schema          int      `json:"schema"`
	ID              string   `json:"id"`
	Nonce           string   `json:"nonce"`
	Phase           Phase    `json:"phase"`
	PreviousVersion string   `json:"previous_version"`
	TargetVersion   string   `json:"target_version"`
	PreviousCommit  string   `json:"previous_commit"`
	TargetCommit    string   `json:"target_commit"`
	InstallRoot     string   `json:"install_root"`
	StateRoot       string   `json:"state_root"`
	ConfigPath      string   `json:"config_path"`
	DatabasePath    string   `json:"database_path"`
	ArchivePath     string   `json:"archive_path"`
	ExtractedPath   string   `json:"extracted_path"`
	SnapshotPath    string   `json:"snapshot_path"`
	OldPID          int      `json:"old_pid"`
	Manifest        Manifest `json:"manifest"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	Error           string   `json:"error,omitempty"`
}

type Cache struct {
	Schema       int               `json:"schema"`
	SourceID     string            `json:"source_id,omitempty"`
	ETag         string            `json:"etag"`
	CheckedAt    string            `json:"checked_at"`
	ReleaseURL   string            `json:"release_url"`
	ReleaseNotes string            `json:"release_notes"`
	ManifestRaw  []byte            `json:"manifest_raw"`
	SignatureRaw []byte            `json:"signature_raw"`
	Manifest     Manifest          `json:"manifest"`
	AssetURLs    map[string]string `json:"asset_urls"`
	LastError    string            `json:"last_error,omitempty"`
}

type CheckState struct {
	Schema    int    `json:"schema"`
	CheckedAt string `json:"checked_at"`
	LastError string `json:"last_error,omitempty"`
}

type ActiveOperation struct {
	Schema      int    `json:"schema"`
	OperationID string `json:"operation_id"`
	Phase       Phase  `json:"phase"`
}

func NewOperationID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func OperationDirectory(stateRoot, id string) (string, error) {
	if !operationIDPattern.MatchString(id) {
		return "", errors.New("invalid update operation ID")
	}
	return filepath.Join(stateRoot, "updates", "operations", id), nil
}

func LoadOperation(stateRoot, id string) (Operation, error) {
	root, err := OperationDirectory(stateRoot, id)
	if err != nil {
		return Operation{}, err
	}
	var operation Operation
	if err := readStrictJSON(filepath.Join(root, "operation.json"), &operation, 1<<20); err != nil {
		return Operation{}, err
	}
	if err := operation.Validate(stateRoot); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func SaveOperation(operation Operation) error {
	if err := operation.Validate(operation.StateRoot); err != nil {
		return err
	}
	operation.Error = secretredaction.String(operation.Error)
	root, err := OperationDirectory(operation.StateRoot, operation.ID)
	if err != nil {
		return err
	}
	operation.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeAtomicJSON(filepath.Join(root, "operation.json"), operation, 0o600); err != nil {
		return err
	}
	return writeAtomicJSON(filepath.Join(operation.StateRoot, "updates", "active.json"), ActiveOperation{
		Schema: OperationSchema, OperationID: operation.ID, Phase: operation.Phase,
	}, 0o600)
}

func (o Operation) Validate(stateRoot string) error {
	if o.Schema != OperationSchema || !operationIDPattern.MatchString(o.ID) || !operationIDPattern.MatchString(o.Nonce) {
		return errors.New("unsupported update operation")
	}
	if o.StateRoot == "" || o.StateRoot != stateRoot || !filepath.IsAbs(o.StateRoot) || !filepath.IsAbs(o.InstallRoot) || !filepath.IsAbs(o.ConfigPath) {
		return errors.New("update operation contains invalid root paths")
	}
	root, err := OperationDirectory(stateRoot, o.ID)
	if err != nil {
		return err
	}
	for _, path := range []string{o.ArchivePath, o.ExtractedPath, o.SnapshotPath} {
		if path == "" || !pathInside(root, path) {
			return fmt.Errorf("update operation path %q escapes its operation directory", path)
		}
	}
	if o.DatabasePath != filepath.Join(stateRoot, "app.db") {
		return errors.New("update operation database path does not match State Root")
	}
	if o.PreviousVersion == "" || o.TargetVersion == "" || o.PreviousVersion == o.TargetVersion {
		return errors.New("update operation versions are invalid")
	}
	if !stableVersionPattern.MatchString(o.PreviousVersion) || !stableVersionPattern.MatchString(o.TargetVersion) {
		return errors.New("update operation versions must be stable releases")
	}
	for _, commit := range []string{o.PreviousCommit, o.TargetCommit} {
		if len(commit) != 40 {
			return errors.New("update operation commits must be full hashes")
		}
		if _, err := hex.DecodeString(commit); err != nil {
			return errors.New("update operation commits must be hexadecimal")
		}
	}
	switch o.Phase {
	case PhasePrepared, PhaseHandoff, PhaseWaitingForOldExit, PhaseSnapshotting, PhaseSwitching,
		PhaseStartingTarget, PhaseValidating, PhaseCommitted, PhaseRollingBack, PhaseRolledBack,
		PhaseFailedSafe, PhaseNeedsRecovery:
	default:
		return errors.New("update operation contains an unknown phase")
	}
	if err := o.Manifest.Validate(); err != nil {
		return err
	}
	if o.Manifest.Version != o.TargetVersion || o.Manifest.Commit != o.TargetCommit {
		return errors.New("operation target does not match manifest")
	}
	return nil
}

func LoadActive(stateRoot string) (ActiveOperation, error) {
	var active ActiveOperation
	if err := readStrictJSON(filepath.Join(stateRoot, "updates", "active.json"), &active, 64<<10); err != nil {
		return ActiveOperation{}, err
	}
	if active.Schema != OperationSchema || !operationIDPattern.MatchString(active.OperationID) {
		return ActiveOperation{}, errors.New("unsupported active update operation")
	}
	return active, nil
}

func loadCache(stateRoot string) (Cache, error) {
	var cache Cache
	if err := readStrictJSON(filepath.Join(stateRoot, "updates", "cache.json"), &cache, 1<<20); err != nil {
		return Cache{}, err
	}
	if cache.Schema != ManifestSchema {
		return Cache{}, errors.New("unsupported update cache")
	}
	return validateCache(cache)
}

func validateCache(cache Cache) (Cache, error) {
	if cache.SourceID == "" {
		cache.SourceID = SourceGitHub
	}
	if !validSourceID(cache.SourceID) {
		return Cache{}, errors.New("update cache contains an unknown source")
	}
	verified, err := VerifyTrustedManifest(cache.ManifestRaw, cache.SignatureRaw)
	if err != nil {
		return Cache{}, err
	}
	if err := validateReleasePageURL(cache.ReleaseURL, verified.Tag); err != nil {
		return Cache{}, err
	}
	for _, asset := range verified.Assets {
		downloadURL := cache.AssetURLs[asset.Name]
		if downloadURL == "" {
			return Cache{}, fmt.Errorf("update cache is missing asset URL %q", asset.Name)
		}
		if err := validateReleaseAssetURL(downloadURL, verified.Tag, asset.Name); err != nil {
			return Cache{}, err
		}
	}
	cache.Manifest = verified
	return cache, nil
}

func saveCache(stateRoot string, cache Cache) error {
	cache.LastError = secretredaction.String(cache.LastError)
	cache.Schema = ManifestSchema
	validated, err := validateCache(cache)
	if err != nil {
		return err
	}
	return writeAtomicJSON(filepath.Join(stateRoot, "updates", "cache.json"), validated, 0o600)
}

func loadCheckState(stateRoot string) (CheckState, error) {
	var state CheckState
	if err := readStrictJSON(filepath.Join(stateRoot, "updates", "check.json"), &state, 64<<10); err != nil {
		return CheckState{}, err
	}
	if state.Schema != ManifestSchema {
		return CheckState{}, errors.New("unsupported update check state")
	}
	if _, err := time.Parse(time.RFC3339Nano, state.CheckedAt); err != nil {
		return CheckState{}, errors.New("update check state has an invalid timestamp")
	}
	return state, nil
}

func saveCheckState(stateRoot, checkedAt, lastError string) error {
	return writeAtomicJSON(filepath.Join(stateRoot, "updates", "check.json"), CheckState{
		Schema: ManifestSchema, CheckedAt: checkedAt, LastError: secretredaction.String(lastError),
	}, 0o600)
}

func writeAtomicJSON(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func readStrictJSON(path string, target any, maximum int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maximum {
		return errors.New("JSON state file exceeds size limit")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func pathInside(parent, candidate string) bool {
	parent, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) &&
		(relative == "." || !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
