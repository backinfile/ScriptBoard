package auditlog

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', previous_hash TEXT NOT NULL DEFAULT '', event_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY CHECK(id = 1), anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state VALUES (1, '', '')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return New(db), db
}

func TestConcurrentAppendsRemainOneLinearChain(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	var wait sync.WaitGroup
	errors := make(chan error, 32)
	for index := 0; index < cap(errors); index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.Append(ctx, Event{OccurredAt: "1786410000", Action: "concurrent", Target: "resource", Result: "succeeded", SourceAddress: "local"})
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if verification, err := store.Verify(ctx); err != nil || verification.Count != 32 {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
}

func TestVerifyDetectsModifiedDeletedAndTruncatedEvents(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	for _, action := range []string{"first", "second", "third"} {
		if _, err := store.Append(ctx, Event{OccurredAt: "1786410000", Action: action, Target: "resource", Result: "succeeded", SourceAddress: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	if verification, err := store.Verify(ctx); err != nil || verification.Count != 3 {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
	if _, err := db.Exec("UPDATE audit_events SET target = 'tampered' WHERE action = 'second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx); err == nil {
		t.Fatal("modified audit event was not detected")
	}
	if _, err := db.Exec("UPDATE audit_events SET target = 'resource' WHERE action = 'second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM audit_events WHERE action = 'second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx); err == nil {
		t.Fatal("deleted intermediate audit event was not detected")
	}
}

func TestVerifyAcceptsRetentionAnchorAndDetectsTailDeletion(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	for _, action := range []string{"first", "second", "third"} {
		if _, err := store.Append(ctx, Event{OccurredAt: "1786410000", Action: action, Target: "resource", Result: "succeeded", SourceAddress: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	var firstHash string
	if err := db.QueryRow("SELECT event_hash FROM audit_events WHERE action = 'first'").Scan(&firstHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE audit_chain_state SET anchor_hash = ? WHERE id = 1", firstHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM audit_events WHERE action = 'first'"); err != nil {
		t.Fatal(err)
	}
	if verification, err := store.Verify(ctx); err != nil || verification.Count != 2 {
		t.Fatalf("retained verification=%#v err=%v", verification, err)
	}
	if _, err := db.Exec("DELETE FROM audit_events WHERE action = 'third'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx); err == nil {
		t.Fatal("deleted audit tail was not detected")
	}
}
