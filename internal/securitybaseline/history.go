package securitybaseline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	historyVersion      = 1
	maxHistorySnapshots = 90
	maxHistoryBytes     = 1 << 20
)

type Snapshot struct {
	CapturedAt time.Time         `json:"captured_at"`
	Score      int               `json:"score"`
	Checks     map[string]Status `json:"checks"`
}

type Change struct {
	ID       string
	Previous Status
	Current  Status
}

const statusMissing Status = "missing"

type Drift struct {
	HasSnapshot bool
	CapturedAt  time.Time
	Score       int
	Changes     []Change
}

type historyFile struct {
	Version   int        `json:"version"`
	Snapshots []Snapshot `json:"snapshots"`
}

type HistoryStore struct {
	path string
	mu   sync.Mutex
}

func NewHistoryStore(stateRoot string) (*HistoryStore, error) {
	root, err := filepath.Abs(stateRoot)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(root, "security-baseline")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(directory, 0o700)
	return &HistoryStore{path: filepath.Join(directory, "history.json")}, nil
}

func (store *HistoryStore) Capture(report Report, now time.Time) (Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	history, err := store.load()
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := snapshotOf(report, now)
	history.Snapshots = append(history.Snapshots, snapshot)
	if len(history.Snapshots) > maxHistorySnapshots {
		history.Snapshots = append([]Snapshot(nil), history.Snapshots[len(history.Snapshots)-maxHistorySnapshots:]...)
	}
	if err := store.write(history); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (store *HistoryStore) Compare(report Report) (Drift, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	history, err := store.load()
	if err != nil || len(history.Snapshots) == 0 {
		return Drift{}, err
	}
	previous := history.Snapshots[len(history.Snapshots)-1]
	current := snapshotOf(report, time.Time{})
	ids := make(map[string]struct{}, len(previous.Checks)+len(current.Checks))
	for id := range previous.Checks {
		ids[id] = struct{}{}
	}
	for id := range current.Checks {
		ids[id] = struct{}{}
	}
	changes := make([]Change, 0)
	for id := range ids {
		before, after := previous.Checks[id], current.Checks[id]
		if before != after {
			if before == "" {
				before = statusMissing
			}
			if after == "" {
				after = statusMissing
			}
			changes = append(changes, Change{ID: id, Previous: before, Current: after})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	return Drift{HasSnapshot: true, CapturedAt: previous.CapturedAt, Score: previous.Score, Changes: changes}, nil
}

func snapshotOf(report Report, now time.Time) Snapshot {
	checks := make(map[string]Status, len(report.Checks))
	for _, check := range report.Checks {
		id := strings.TrimSpace(check.ID)
		if id != "" && len(id) <= 128 {
			checks[id] = check.Status
		}
	}
	return Snapshot{CapturedAt: now.UTC(), Score: report.Score, Checks: checks}
}

func (store *HistoryStore) load() (historyFile, error) {
	linkInfo, linkErr := os.Lstat(store.path)
	if linkErr == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
		return historyFile{}, errors.New("security baseline history must not be a symbolic link")
	}
	if linkErr != nil && !errors.Is(linkErr, os.ErrNotExist) {
		return historyFile{}, linkErr
	}
	file, err := os.Open(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return historyFile{Version: historyVersion}, nil
	}
	if err != nil {
		return historyFile{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxHistoryBytes {
		return historyFile{}, errors.New("security baseline history is invalid or too large")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxHistoryBytes+1))
	decoder.DisallowUnknownFields()
	var history historyFile
	if err := decoder.Decode(&history); err != nil {
		return historyFile{}, fmt.Errorf("decode security baseline history: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return historyFile{}, errors.New("security baseline history contains trailing data")
	}
	if history.Version != historyVersion || len(history.Snapshots) > maxHistorySnapshots {
		return historyFile{}, errors.New("security baseline history has an unsupported format")
	}
	for _, snapshot := range history.Snapshots {
		if snapshot.CapturedAt.IsZero() || snapshot.Score < 0 || snapshot.Score > 100 || len(snapshot.Checks) > 256 {
			return historyFile{}, errors.New("security baseline history contains invalid values")
		}
		for id, status := range snapshot.Checks {
			if strings.TrimSpace(id) == "" || len(id) > 128 || status != StatusPass && status != StatusAttention && status != StatusUnavailable {
				return historyFile{}, errors.New("security baseline history contains an invalid check")
			}
		}
	}
	return history, nil
}

func (store *HistoryStore) write(history historyFile) error {
	body, err := json.Marshal(history)
	if err != nil {
		return err
	}
	if len(body) > maxHistoryBytes {
		return errors.New("security baseline history exceeds its size limit")
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".history-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return err
	}
	_ = os.Chmod(store.path, 0o600)
	return nil
}
