package migrations

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchema62DefaultsExistingQuickRunsToNoConfirmation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE quick_runs (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, script_path TEXT NOT NULL,
		script_path_key TEXT NOT NULL, arguments_template TEXT NOT NULL,
		timeout_seconds INTEGER NOT NULL, source_run_id TEXT, sort_order INTEGER NOT NULL,
		created_at INTEGER NOT NULL, group_id TEXT, locked INTEGER NOT NULL DEFAULT 0,
		script_sha256 TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1,
		updated_at INTEGER NOT NULL DEFAULT 0
	); INSERT INTO quick_runs(id,name,script_path,script_path_key,arguments_template,timeout_seconds,sort_order,created_at)
		VALUES('legacy','Legacy','legacy.cmd','legacy.cmd','',0,1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 61, Options{CurrentVersion: 62, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(value string) string { return value }, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	var requireConfirmation bool
	if err := db.QueryRow(`SELECT require_confirmation FROM quick_runs WHERE id='legacy'`).Scan(&requireConfirmation); err != nil {
		t.Fatal(err)
	}
	if requireConfirmation {
		t.Fatal("migrated Quick Run unexpectedly requires confirmation")
	}
}
