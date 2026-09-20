package migrations

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// schema 73 的 quick_runs 表结构（无 params_json）。
const legacyQuickRunSchema73 = `
CREATE TABLE quick_runs (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	script_path TEXT NOT NULL,
	script_path_key TEXT NOT NULL,
	arguments_template TEXT NOT NULL,
	memory_limit TEXT NOT NULL DEFAULT '',
	timeout_seconds INTEGER NOT NULL,
	source_run_id TEXT,
	sort_order INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	group_id TEXT,
	locked INTEGER NOT NULL DEFAULT 0,
	require_confirmation INTEGER NOT NULL DEFAULT 0,
	script_sha256 TEXT NOT NULL DEFAULT '',
	revision INTEGER NOT NULL DEFAULT 1,
	updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO quick_runs(id,name,script_path,script_path_key,arguments_template,timeout_seconds,sort_order,created_at)
	VALUES('quick-1','部署','C:\\jobs\\deploy.ps1','c:\\jobs\\deploy.ps1','',30,1,1)`

func quickRunParamsMigrationOptions() Options {
	return Options{CurrentVersion: 74, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(value string) string { return value }, Now: time.Now}
}

func assertQuickRunParamsSchema74(t *testing.T, db *sql.DB) {
	t.Helper()
	var params string
	if err := db.QueryRow(`SELECT params_json FROM quick_runs WHERE id='quick-1'`).Scan(&params); err != nil || params != "" {
		t.Fatalf("params_json=%q err=%v", params, err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 74 {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
}

func TestSchema74FromVersion73AddsQuickRunParams(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyQuickRunSchema73); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 73, quickRunParamsMigrationOptions()); err != nil {
		t.Fatal(err)
	}
	assertQuickRunParamsSchema74(t, db)
}

func TestSchema74FromMinimumVersionAddsQuickRunParams(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyQuickRunSchema73); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 20, quickRunParamsMigrationOptions()); err != nil {
		t.Fatal(err)
	}
	assertQuickRunParamsSchema74(t, db)
}
