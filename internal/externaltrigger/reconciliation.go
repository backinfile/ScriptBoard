package externaltrigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxQueuedCompletions = 10_000
	maxCompletionBytes   = 16 << 10
)

func (manager *Manager) queueCompletion(invocation Invocation) error {
	if invocation.ID == "" || strings.ContainsAny(invocation.ID, "\r\n\x00") {
		return ErrInvalidInput
	}
	body, err := json.Marshal(invocation)
	if err != nil || len(body) > maxCompletionBytes {
		return errors.New("invocation completion is too large")
	}
	if err := os.MkdirAll(manager.reconciliationDirectory, 0o700); err != nil {
		return err
	}
	target := manager.completionPath(invocation.ID)
	if existing, readErr := os.ReadFile(target); readErr == nil {
		if string(existing) == string(body) {
			return nil
		}
		return errors.New("queued invocation completion conflicts with an existing result")
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	entries, err := os.ReadDir(manager.reconciliationDirectory)
	if err != nil {
		return err
	}
	if len(entries) >= maxQueuedCompletions {
		return errors.New("invocation completion queue is full")
	}
	temporary, err := os.CreateTemp(manager.reconciliationDirectory, ".completion-*.tmp")
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
	return os.Rename(temporaryPath, target)
}

func (manager *Manager) removeQueuedCompletion(id string) error {
	err := os.Remove(manager.completionPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (manager *Manager) completionPath(id string) string {
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(manager.reconciliationDirectory, hex.EncodeToString(digest[:])+".json")
}

// ReconcileInvocations replays durable completion results before marking old
// processing records unknown. It is safe to call repeatedly.
func (manager *Manager) ReconcileInvocations(ctx context.Context, staleBefore time.Time) error {
	entries, err := os.ReadDir(manager.reconciliationDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(manager.reconciliationDirectory, entry.Name())
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			var invocation Invocation
			if len(body) > maxCompletionBytes || json.Unmarshal(body, &invocation) != nil || manager.completionPath(invocation.ID) != path {
				return fmt.Errorf("invalid queued invocation completion %q", entry.Name())
			}
			if err := manager.completeInvocation(ctx, invocation); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if staleBefore.IsZero() {
		return nil
	}
	_, err = manager.db.ExecContext(ctx, `UPDATE external_trigger_requests
		SET result='unknown', message=CASE WHEN message='' THEN 'completion was not recorded before recovery deadline' ELSE message END
		WHERE result='processing' AND occurred_at < ?`, staleBefore.UTC().Unix())
	return err
}
