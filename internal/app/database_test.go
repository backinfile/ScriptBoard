package app

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/hostfiles"
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
	beforeDatabase, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries := directoryEntryNames(t, filepath.Dir(path))

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
	afterDatabase, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDatabase, afterDatabase) {
		t.Fatal("legacy database bytes changed while rejecting its schema")
	}
	if afterEntries := directoryEntryNames(t, filepath.Dir(path)); !reflect.DeepEqual(beforeEntries, afterEntries) {
		t.Fatalf("legacy state directory changed: before=%v after=%v", beforeEntries, afterEntries)
	}
}

func TestOpenRejectsLegacyStateRootBeforeCreatingLocksOrChangingPermissions(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "legacy-state")
	if err := os.Mkdir(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(stateRoot, "app.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE legacy (value TEXT); INSERT INTO legacy VALUES ('preserve'); PRAGMA user_version=18;`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	beforeDatabase, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries := directoryEntryNames(t, stateRoot)
	beforeInfo, err := os.Stat(stateRoot)
	if err != nil {
		t.Fatal(err)
	}

	application, err := Open(Config{StateRoot: stateRoot})
	if application != nil {
		_ = application.Close()
		t.Fatal("legacy State Root unexpectedly opened")
	}
	if err == nil || !strings.Contains(err.Error(), "new State Root") {
		t.Fatalf("legacy State Root error = %v", err)
	}
	afterDatabase, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDatabase, afterDatabase) {
		t.Fatal("legacy database bytes changed during application startup rejection")
	}
	if afterEntries := directoryEntryNames(t, stateRoot); !reflect.DeepEqual(beforeEntries, afterEntries) {
		t.Fatalf("legacy State Root entries changed: before=%v after=%v", beforeEntries, afterEntries)
	}
	afterInfo, err := os.Stat(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if beforeInfo.Mode().Perm() != afterInfo.Mode().Perm() {
		t.Fatalf("legacy State Root permissions changed from %#o to %#o", beforeInfo.Mode().Perm(), afterInfo.Mode().Perm())
	}
}

func directoryEntryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
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
	if version != 21 {
		t.Fatalf("schema version=%d, want 21", version)
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
	for _, table := range []string{"file_operations", "trash_entries", "assistant_settings", "assistant_models", "assistant_conversations", "assistant_messages"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("required schema 21 table %q is missing: count=%d error=%v", table, count, err)
		}
	}
	var gitState int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'git_state'`).Scan(&gitState); err != nil || gitState != 0 {
		t.Fatalf("removed git_state table exists: count=%d error=%v", gitState, err)
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

func TestOpenDatabaseMigratesSchema20ToAssistantSchema21(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('preserved-admin', 'preserved', 'hash', 'administrator', 1, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"assistant_approvals", "assistant_tool_calls", "assistant_context_refs", "assistant_messages", "assistant_conversations", "assistant_models", "assistant_settings"} {
		if _, err := db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version=20; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 20: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 21 {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	var username string
	if err := migrated.QueryRow("SELECT username FROM users WHERE id = 'preserved-admin'").Scan(&username); err != nil || username != "preserved" {
		t.Fatalf("preserved user = %q, error = %v", username, err)
	}
	for _, table := range []string{"assistant_settings", "assistant_models", "assistant_conversations", "assistant_messages"} {
		var count int
		if err := migrated.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migrated table %q count=%d error=%v", table, count, err)
		}
	}
}

func TestFileOperationCommitRegistersRecoverableSourceTrash(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	trashRoot := filepath.Join(root, ".scriptboard-trash")
	if err := os.Mkdir(trashRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	trashPath := filepath.Join(trashRoot, "move-operation")
	if err := os.WriteFile(trashPath, []byte("moved source"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := hostfiles.FileOperation{
		ID: "move-operation", Kind: "cross_filesystem_move",
		SourcePath: filepath.Join(root, "source.txt"), SourcePathKey: hostfiles.ComparisonKey(filepath.Join(root, "source.txt")),
		DestinationPath: filepath.Join(root, "destination.txt"), DestinationPathKey: hostfiles.ComparisonKey(filepath.Join(root, "destination.txt")),
		TrashPath: trashPath, Phase: hostfiles.OperationSourceTrashed, CreatedAt: now, UpdatedAt: now,
	}
	store := newSQLiteFileOperationStore(db)
	if err := store.Create(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at)
		VALUES ('quick-moved', 'Moved', ?, ?, '', 0, 1, 1)`, operation.SourcePath, operation.SourcePathKey); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedules
		(id, name, script_path, script_path_key, arguments_template, expression, timeout_seconds, enabled,
		 allow_overlap, next_fire_at, created_at, updated_at)
		VALUES ('schedule-moved', 'Moved', ?, ?, '', '* * * * *', 0, 1, 0, 1, 1, 1)`, operation.SourcePath, operation.SourcePathKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), operation); err != nil {
		t.Fatalf("repeat file operation commit should be idempotent: %v", err)
	}

	var originalPath, storedPath string
	if err := db.QueryRow("SELECT original_path, stored_path FROM trash_entries WHERE id = ?", operation.ID).Scan(&originalPath, &storedPath); err != nil {
		t.Fatalf("cross-filesystem source trash was not registered: %v", err)
	}
	if originalPath != operation.SourcePath || storedPath != operation.TrashPath {
		t.Fatalf("trash entry = %q -> %q", originalPath, storedPath)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM trash_entries WHERE id = ?", operation.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotent commit trash row count = %d, error = %v", count, err)
	}
	for _, reference := range []struct{ table, id string }{{"quick_runs", "quick-moved"}, {"schedules", "schedule-moved"}} {
		var path, key string
		if err := db.QueryRow("SELECT script_path, script_path_key FROM "+reference.table+" WHERE id = ?", reference.id).Scan(&path, &key); err != nil {
			t.Fatalf("read moved %s reference: %v", reference.table, err)
		}
		if path != operation.DestinationPath || key != operation.DestinationPathKey {
			t.Fatalf("%s reference = %q (%q), want %q (%q)", reference.table, path, key, operation.DestinationPath, operation.DestinationPathKey)
		}
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
	if err == nil || !strings.Contains(err.Error(), "incompatible with schema 21") || !strings.Contains(err.Error(), "new State Root") {
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
	_, err = db.Exec(`INSERT INTO runs (id, script_path, script_path_key, script_sha256, arguments_template, arguments_json, executor, source_type, status, created_at, error, log_path) VALUES ('run-1', 'C:\\jobs\\job.cmd', 'c:\\jobs\\job.cmd', 'digest', '', '[]', 'cmd.exe', 'manual', 'running', 1, '', 'runs/run-1.jsonl')`)
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
