package app

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSchema35QuickRunsMigrateFailClosedUntilRepublished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema35.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE quick_runs (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, script_path TEXT NOT NULL, script_path_key TEXT NOT NULL,
			arguments_template TEXT NOT NULL, timeout_seconds INTEGER NOT NULL, source_run_id TEXT,
			sort_order INTEGER NOT NULL, created_at INTEGER NOT NULL, group_id TEXT, locked INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO quick_runs (id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at, locked)
			VALUES ('legacy-quick', 'Legacy', 'C:\\jobs\\legacy.cmd', 'c:\\jobs\\legacy.cmd', '', 30, 1, 1, 1)`,
		`PRAGMA user_version=35`,
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
	var version int
	var digest string
	var revision int64
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT script_sha256, revision FROM quick_runs WHERE id = 'legacy-quick'").Scan(&digest, &revision); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || digest != "" || revision != 1 {
		t.Fatalf("migrated Quick Run: schema=%d digest=%q revision=%d", version, digest, revision)
	}
}
