package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scriptboard/internal/auditlog"
)

func TestAuditRetentionRemovesLinkedOneTimeSourceButKeepsRunArtifacts(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at INTEGER NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			result TEXT NOT NULL,
			source_address TEXT NOT NULL
		)`,
		`CREATE TABLE runs (
			id TEXT PRIMARY KEY,
			script_kind TEXT NOT NULL,
			source_filename TEXT NOT NULL,
			source_expired INTEGER NOT NULL,
			source_audit_event_id INTEGER
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	result, err := db.Exec(`INSERT INTO audit_events
		(occurred_at, action, target, result, source_address)
		VALUES (?, 'start_one_time_run', 'run-1', 'accepted', 'test')`,
		time.Now().AddDate(-2, 0, 0).Unix())
	if err != nil {
		t.Fatal(err)
	}
	auditID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO runs
		(id, script_kind, source_filename, source_expired, source_audit_event_id)
		VALUES ('run-1', 'one_time', 'source.sh', 0, ?)`, auditID); err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	runRoot := filepath.Join(stateRoot, "runs", "run-1")
	if err := os.MkdirAll(runRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(runRoot, "source.sh")
	logPath := filepath.Join(runRoot, "events.jsonl")
	if err := os.WriteFile(sourcePath, []byte("echo retained metadata\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deleted, err := cleanupExpiredAuditEvents(db, stateRoot, time.Now().AddDate(-1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d, want 1", deleted)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("independent Run log was removed: %v", err)
	}
	var expired, audits int
	if err := db.QueryRow("SELECT source_expired FROM runs WHERE id = 'run-1'").Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if expired != 1 || audits != 0 {
		t.Fatalf("source_expired=%d audits=%d", expired, audits)
	}
}

func TestAuditRetentionAdvancesHashChainAnchor(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "chain-retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', previous_hash TEXT NOT NULL DEFAULT '', event_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY CHECK(id = 1), anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state VALUES (1, '', '')`,
		`CREATE TABLE runs (id TEXT PRIMARY KEY, script_kind TEXT NOT NULL, source_filename TEXT NOT NULL, source_expired INTEGER NOT NULL, source_audit_event_id INTEGER)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store := auditlog.New(db)
	old := time.Now().AddDate(-2, 0, 0).UTC().Unix()
	recent := time.Now().UTC().Unix()
	for _, occurred := range []int64{old, recent} {
		if _, err := store.Append(context.Background(), auditlog.Event{
			OccurredAt: fmt.Sprint(occurred), Action: "retention", Target: "event", Result: "succeeded", SourceAddress: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := cleanupExpiredAuditEvents(db, t.TempDir(), time.Now().AddDate(-1, 0, 0))
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if verification, err := store.Verify(context.Background()); err != nil || verification.Count != 1 {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
}

func TestAuditRetentionKeepsAuditWhenSourceCannotBeRemoved(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL)`,
		`CREATE TABLE runs (id TEXT PRIMARY KEY, script_kind TEXT NOT NULL, source_filename TEXT NOT NULL, source_expired INTEGER NOT NULL, source_audit_event_id INTEGER)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	result, err := db.Exec(`INSERT INTO audit_events (occurred_at, action, target, result, source_address)
		VALUES (?, 'start_one_time_run', 'run-locked', 'accepted', 'test')`, time.Now().AddDate(-2, 0, 0).Unix())
	if err != nil {
		t.Fatal(err)
	}
	auditID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO runs VALUES ('run-locked', 'one_time', 'source.sh', 0, ?)`, auditID); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "runs", "run-locked", "source.sh")
	if err := os.MkdirAll(sourcePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Dir(filepath.Dir(filepath.Dir(sourcePath)))

	if _, err := cleanupExpiredAuditEvents(db, stateRoot, time.Now().AddDate(-1, 0, 0)); err == nil {
		t.Fatal("cleanup unexpectedly succeeded")
	}
	var expired, audits int
	if err := db.QueryRow("SELECT source_expired FROM runs WHERE id = 'run-locked'").Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if expired != 0 || audits != 1 {
		t.Fatalf("source_expired=%d audits=%d; cleanup must retry later", expired, audits)
	}
}
