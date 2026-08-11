package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"scriptboard/internal/auditlog"
)

func TestSchema34AuditEventsAreBackfilledIntoHashChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema34.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO audit_events (occurred_at, action, target, result, source_address) VALUES (1786410000, 'legacy', 'resource', 'succeeded', 'local')`,
		`PRAGMA user_version=34`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	verification, err := auditlog.New(database).Verify(context.Background())
	if err != nil || verification.Count != 1 || verification.LastHash == "" {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
	var version int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 35 {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}
