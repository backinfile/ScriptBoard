package app

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenDatabaseRejectsPreMultiUserSchemaWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE admin (id INTEGER PRIMARY KEY, username TEXT NOT NULL, password_hash TEXT NOT NULL);
		INSERT INTO admin VALUES (1, 'legacy-admin', 'preserve-me');
		PRAGMA user_version=18;
	`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = openDatabase(path)
	if err == nil || !strings.Contains(err.Error(), "new State Root") {
		t.Fatalf("expected incompatible-schema rejection, got %v", err)
	}

	unchanged, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer unchanged.Close()
	var username, passwordHash string
	if err := unchanged.QueryRow("SELECT username, password_hash FROM admin").Scan(&username, &passwordHash); err != nil {
		t.Fatalf("legacy database was changed: %v", err)
	}
	if username != "legacy-admin" || passwordHash != "preserve-me" {
		t.Fatalf("legacy row changed to username=%q password_hash=%q", username, passwordHash)
	}
}

func TestOpenDatabaseCreatesFixedRoleUserSchema(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 19 {
		t.Fatalf("schema version=%d, want 19", version)
	}
	var usersTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&usersTable); err != nil {
		t.Fatal(err)
	}
	if usersTable != 1 {
		t.Fatal("users table was not created")
	}
	var adminTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'admin'`).Scan(&adminTable); err != nil {
		t.Fatal(err)
	}
	if adminTable != 0 {
		t.Fatal("legacy admin table should not exist")
	}

	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('admin-one', 'admin', 'hash', 'administrator', 1, 1, 1, 1)`); err != nil {
		t.Fatalf("insert first administrator: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('admin-two', 'admin2', 'hash', 'administrator', 1, 1, 1, 1)`); err == nil {
		t.Fatal("second administrator was accepted")
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('custom-role', 'custom', 'hash', 'custom', 1, 1, 1, 1)`); err == nil {
		t.Fatal("custom role was accepted")
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

func TestOpenDatabaseCreatesIndexesForPeriodicAndTimeOrderedQueries(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name  string
		index string
		query string
	}{
		{
			name:  "next enabled schedule",
			index: "schedules_due_idx",
			query: `SELECT MIN(next_fire_at) FROM schedules WHERE enabled = 1 AND deleted = 0`,
		},
		{
			name:  "newest runs",
			index: "runs_created_idx",
			query: `SELECT id FROM runs ORDER BY created_at DESC LIMIT 50`,
		},
		{
			name:  "newest audit events",
			index: "audit_events_occurred_idx",
			query: `SELECT id FROM audit_events ORDER BY occurred_at DESC LIMIT 50`,
		},
		{
			name:  "newest trash entries",
			index: "trash_entries_deleted_idx",
			query: `SELECT id FROM trash_entries ORDER BY deleted_at DESC LIMIT 50`,
		},
		{
			name:  "latest trigger for schedule",
			index: "schedule_triggers_schedule_time_idx",
			query: `SELECT result FROM schedule_triggers WHERE schedule_id = 'schedule-1' ORDER BY scheduled_for DESC LIMIT 1`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows, err := db.Query("EXPLAIN QUERY PLAN " + test.query)
			if err != nil {
				t.Fatal(err)
			}
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				plan.WriteString(detail)
				plan.WriteByte('\n')
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.String(), test.index) {
				t.Fatalf("query plan does not use %s:\n%s", test.index, plan.String())
			}
		})
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
