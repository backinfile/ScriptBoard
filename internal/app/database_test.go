package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenDatabaseSnapshotsAndMigratesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("CREATE TABLE legacy_marker (value TEXT); INSERT INTO legacy_marker VALUES ('preserved')"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}

	snapshotPath := path + ".pre-migration-v0"
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("pre-migration snapshot: %v", err)
	}
	snapshot, err := sql.Open("sqlite", "file:"+filepath.ToSlash(snapshotPath)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var marker string
	if err := snapshot.QueryRow("SELECT value FROM legacy_marker").Scan(&marker); err != nil || marker != "preserved" {
		t.Fatalf("snapshot marker=%q err=%v", marker, err)
	}
}

func TestOpenDatabaseRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = openDatabase(path)
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("expected newer-schema rejection, got %v", err)
	}
}

func TestOpenDatabaseMarksUnsupervisedRunDisconnected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO runs (id, script_path, script_sha256, arguments_template, arguments_json, executor, source_type, status, created_at, error, log_path) VALUES ('run-1', 'job.cmd', 'digest', '', '[]', 'cmd.exe', 'manual', 'running', 1, '', 'runs/run-1.jsonl')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db, err = openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, message string
	var finishedAt sql.NullInt64
	if err := db.QueryRow("SELECT status, error, finished_at FROM runs WHERE id = 'run-1'").Scan(&status, &message, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "disconnected" || message == "" || !finishedAt.Valid {
		t.Fatalf("status=%q message=%q finished_at=%v", status, message, finishedAt)
	}
}
