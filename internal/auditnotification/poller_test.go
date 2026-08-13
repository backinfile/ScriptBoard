package auditnotification

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/auditlog"
)

func TestPollerBootstrapsWithoutReplayingAndDurablyAdvances(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:poller-test?mode=memory&cache=shared&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', authentication_assurance TEXT NOT NULL DEFAULT '', resource_revision TEXT NOT NULL DEFAULT '', resource_digest_sha256 TEXT NOT NULL DEFAULT '', previous_hash TEXT NOT NULL DEFAULT '', event_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY CHECK(id = 1), anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state VALUES (1, '', '')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	audit := auditlog.New(db)
	if _, err := audit.Append(context.Background(), auditlog.Event{OccurredAt: "1", Action: "old", Result: "success"}); err != nil {
		t.Fatal(err)
	}
	seen := make(chan auditlog.CommittedEvent, 2)
	poller, err := New(Options{DB: db, StateRoot: root, Interval: 5 * time.Millisecond, Observe: func(event auditlog.CommittedEvent) error {
		seen <- event
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); poller.Wait() }()
	if err := poller.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Append(context.Background(), auditlog.Event{OccurredAt: "2", Action: "website_monitor_down", Target: "safe-monitor", Result: "failed"}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-seen:
		if event.ID != 2 || event.Event.Action != "website_monitor_down" {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("new audit event was not observed")
	}
	cancel()
	poller.Wait()
	last, exists, err := poller.readCursor()
	if err != nil {
		t.Fatal(err)
	}
	if !exists || last != 2 {
		t.Fatalf("cursor exists=%v last=%d, want exists=true last=2", exists, last)
	}

	replayed := make(chan auditlog.CommittedEvent, 1)
	restarted, err := New(Options{DB: db, StateRoot: root, Interval: 5 * time.Millisecond, Observe: func(event auditlog.CommittedEvent) error {
		replayed <- event
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	restartCtx, stop := context.WithCancel(context.Background())
	if err := restarted.Start(restartCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-replayed:
		t.Fatalf("durably consumed event replayed: %#v", event)
	case <-time.After(30 * time.Millisecond):
	}
	stop()
	restarted.Wait()
}
