package auditnotification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/auditlog"
)

const maxCursorBytes = 4096

type Options struct {
	DB        *sql.DB
	StateRoot string
	Observe   func(auditlog.CommittedEvent) error
	Interval  time.Duration
}

type Poller struct {
	db       *sql.DB
	observe  func(auditlog.CommittedEvent) error
	interval time.Duration
	cursor   string
	wg       sync.WaitGroup
}

type cursorFile struct {
	Type        string `json:"type"`
	LastEventID int64  `json:"last_event_id"`
}

func New(options Options) (*Poller, error) {
	if options.DB == nil || options.Observe == nil {
		return nil, errors.New("audit notification poller requires a database and observer")
	}
	root, err := filepath.Abs(strings.TrimSpace(options.StateRoot))
	if err != nil || strings.TrimSpace(options.StateRoot) == "" || !filepath.IsAbs(options.StateRoot) {
		return nil, errors.New("audit notification poller requires an absolute State Root")
	}
	directory := filepath.Join(root, "security-events")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(directory, 0o700)
	interval := options.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Poller{db: options.DB, observe: options.Observe, interval: interval, cursor: filepath.Join(directory, "email-cursor.json")}, nil
}

func (poller *Poller) Start(ctx context.Context) error {
	last, exists, err := poller.readCursor()
	if err != nil {
		return err
	}
	if !exists {
		if err := poller.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM audit_events").Scan(&last); err != nil {
			return fmt.Errorf("bootstrap audit notification cursor: %w", err)
		}
		if err := poller.writeCursor(last); err != nil {
			return err
		}
	}
	poller.wg.Add(1)
	go poller.run(ctx, last)
	return nil
}

func (poller *Poller) Wait() { poller.wg.Wait() }

func (poller *Poller) run(ctx context.Context, last int64) {
	defer poller.wg.Done()
	ticker := time.NewTicker(poller.interval)
	defer ticker.Stop()
	for {
		for {
			next, found, err := poller.next(ctx, last)
			if err != nil || !found {
				break
			}
			if err := poller.observe(next); err != nil {
				break
			}
			if err := poller.writeCursor(next.ID); err != nil {
				break
			}
			last = next.ID
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (poller *Poller) next(ctx context.Context, after int64) (auditlog.CommittedEvent, bool, error) {
	var result auditlog.CommittedEvent
	err := poller.db.QueryRowContext(ctx, `SELECT id, occurred_at, action, target, result, source_address,
		actor_user_id, actor_username, actor_role, request_id, authentication_assurance,
		resource_revision, resource_digest_sha256, event_hash
		FROM audit_events WHERE id > ? ORDER BY id LIMIT 1`, after).Scan(
		&result.ID, &result.Event.OccurredAt, &result.Event.Action, &result.Event.Target, &result.Event.Result,
		&result.Event.SourceAddress, &result.Event.ActorUserID, &result.Event.ActorUsername, &result.Event.ActorRole,
		&result.Event.RequestID, &result.Event.AuthenticationAssurance, &result.Event.ResourceRevision,
		&result.Event.ResourceDigestSHA256, &result.EventSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return auditlog.CommittedEvent{}, false, nil
	}
	if err != nil {
		return auditlog.CommittedEvent{}, false, err
	}
	return result, true, nil
}

func (poller *Poller) readCursor() (int64, bool, error) {
	info, err := os.Lstat(poller.cursor)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCursorBytes {
		return 0, false, errors.New("audit notification cursor is not a bounded regular file")
	}
	body, err := os.ReadFile(poller.cursor)
	if err != nil {
		return 0, false, err
	}
	var cursor cursorFile
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.Type != "scriptboard.audit-notification-cursor-v1" || cursor.LastEventID < 0 {
		return 0, false, errors.New("audit notification cursor is invalid")
	}
	return cursor.LastEventID, true, nil
}

func (poller *Poller) writeCursor(last int64) error {
	body, err := json.Marshal(cursorFile{Type: "scriptboard.audit-notification-cursor-v1", LastEventID: last})
	if err != nil {
		return err
	}
	directory := filepath.Dir(poller.cursor)
	temporary, err := os.CreateTemp(directory, ".email-cursor-*.tmp")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
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
	return replaceFile(path, poller.cursor)
}
