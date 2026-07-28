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
	if _, err := db.Exec(`INSERT INTO host_metric_minutes(bucket_at, sample_count, average_json, maximum_json) VALUES (1, 1, '{}', '{}')`); err != nil {
		t.Fatalf("host metric history table is unavailable after migration: %v", err)
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

func TestOpenDatabaseMigratesVariablePasswordDisplayFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO variables(name, value, created_at, updated_at) VALUES ('TOKEN', 'plain-value', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE variables DROP COLUMN is_password`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=6`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = openDatabase(path)
	if err != nil {
		t.Fatalf("migrate variable display type: %v", err)
	}
	defer db.Close()
	var value string
	var isPassword bool
	if err := db.QueryRow(`SELECT value, is_password FROM variables WHERE name = 'TOKEN'`).Scan(&value, &isPassword); err != nil {
		t.Fatal(err)
	}
	if value != "plain-value" || isPassword {
		t.Fatalf("value=%q is_password=%v", value, isPassword)
	}
}

func TestOpenDatabaseBackfillsRunScheduleIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedules
		(id, name, group_name, script_path, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, created_at, updated_at, deleted)
		VALUES ('schedule-1', 'Renamed schedule', '', 'job.cmd', '', '0 0 * * *', 0, 1, 1, 1, 1, 1, 0);
		INSERT INTO runs
		(id, script_path, script_sha256, arguments_template, template_arguments_json, arguments_json, executor, source_type, source_name, runtime_identity, status, created_at, log_path)
		VALUES ('run-1', 'job.cmd', 'digest', '', '[]', '[]', 'cmd.exe', 'scheduler', 'Former schedule name', 'test', 'succeeded', 1, '');
		INSERT INTO schedule_triggers
		(id, schedule_id, scheduled_for, result, run_id, error)
		VALUES ('trigger-1', 'schedule-1', 1, 'created', 'run-1', '');
		DROP INDEX runs_source_idx;
		ALTER TABLE runs DROP COLUMN source_id;
		PRAGMA user_version=12;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = openDatabase(path)
	if err != nil {
		t.Fatalf("migrate Run source IDs: %v", err)
	}
	defer db.Close()
	var sourceID string
	if err := db.QueryRow("SELECT source_id FROM runs WHERE id = 'run-1'").Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if sourceID != "schedule-1" {
		t.Fatalf("Run source_id=%q", sourceID)
	}
}

func TestOpenDatabaseRemovesLegacyAIStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE ai_settings (id INTEGER PRIMARY KEY); PRAGMA user_version=8`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := openDatabase(path)
	if err != nil {
		t.Fatalf("remove legacy AI storage: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'ai_%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy AI tables remaining = %d", count)
	}
}

func TestOpenDatabaseMakesQuickRunSourceOptionalWithoutLosingLegacySources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO runs
		(id, script_path, script_sha256, arguments_template, arguments_json, executor, source_type, status, created_at, error, log_path)
		VALUES ('run-1', 'legacy.cmd', 'digest', '', '[]', 'cmd.exe', 'manual', 'succeeded', 1, '', 'runs/run-1.jsonl')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
		VALUES ('quick-legacy', 'Legacy', 'legacy.cmd', '', 0, 'run-1', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ALTER TABLE quick_runs RENAME TO quick_runs_v10",
		`CREATE TABLE quick_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			script_path TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			source_run_id TEXT NOT NULL REFERENCES runs(id),
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO quick_runs
			(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
			SELECT id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at FROM quick_runs_v10`,
		"DROP TABLE quick_runs_v10",
		"PRAGMA user_version=9",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = openDatabase(path)
	if err != nil {
		t.Fatalf("migrate Quick Run source: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path + ".pre-migration-v9"); err != nil {
		t.Fatalf("pre-migration snapshot: %v", err)
	}
	var source sql.NullString
	if err := db.QueryRow("SELECT source_run_id FROM quick_runs WHERE id = 'quick-legacy'").Scan(&source); err != nil {
		t.Fatal(err)
	}
	if !source.Valid || source.String != "run-1" {
		t.Fatalf("legacy source=%v, want run-1", source)
	}
	if _, err := db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
		VALUES ('quick-file', 'From file', 'direct.cmd', '', 0, NULL, 2, 2)`); err != nil {
		t.Fatalf("insert file-created Quick Run without source: %v", err)
	}
}

func TestOpenDatabaseMigratesQuickRunOrganizationWithoutChangingExistingRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
		VALUES ('quick-existing', 'Existing', 'existing.cmd', '--safe', 45, NULL, 7, 123)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ALTER TABLE quick_runs RENAME TO quick_runs_latest",
		`CREATE TABLE quick_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			script_path TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			source_run_id TEXT REFERENCES runs(id),
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO quick_runs
			(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
			SELECT id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at
			FROM quick_runs_latest`,
		"DROP TABLE quick_runs_latest",
		"DROP TABLE IF EXISTS quick_run_groups",
		"PRAGMA user_version=10",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = openDatabase(path)
	if err != nil {
		t.Fatalf("migrate Quick Run organization: %v", err)
	}
	defer db.Close()

	var groupID sql.NullString
	var name, scriptPath, arguments string
	var timeout, sortOrder int
	var sourceRunID sql.NullString
	var locked bool
	var createdAt, updatedAt int64
	if err := db.QueryRow(`SELECT group_id, name, script_path, arguments_template, timeout_seconds,
		source_run_id, sort_order, locked, created_at, updated_at
		FROM quick_runs WHERE id = 'quick-existing'`).Scan(
		&groupID, &name, &scriptPath, &arguments, &timeout,
		&sourceRunID, &sortOrder, &locked, &createdAt, &updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if groupID.Valid || sourceRunID.Valid || locked {
		t.Fatalf("group=%v source=%v locked=%v, want ungrouped source-free unlocked", groupID, sourceRunID, locked)
	}
	if name != "Existing" || scriptPath != "existing.cmd" || arguments != "--safe" || timeout != 45 || sortOrder != 7 {
		t.Fatalf("migrated Quick Run changed: name=%q script=%q arguments=%q timeout=%d order=%d", name, scriptPath, arguments, timeout, sortOrder)
	}
	if createdAt != 123 || updatedAt != createdAt {
		t.Fatalf("timestamps created=%d updated=%d, want both 123", createdAt, updatedAt)
	}
	var groupsTable string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'quick_run_groups'`).Scan(&groupsTable); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDatabaseAddsScheduleGroupsWithoutChangingExistingSchedules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedules
		(id, name, script_path, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, created_at, updated_at)
		VALUES ('schedule-existing', 'Existing', 'existing.cmd', '--safe', '0 2 * * *', 45, 1, 0, 1, 2, 3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP INDEX schedules_group_idx"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE schedules DROP COLUMN group_name"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=11"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = openDatabase(path)
	if err != nil {
		t.Fatalf("migrate Schedule groups: %v", err)
	}
	defer db.Close()

	var name, scriptPath, arguments, expression, groupName string
	var timeout int
	if err := db.QueryRow(`SELECT name, group_name, script_path, arguments_template, expression, timeout_seconds
		FROM schedules WHERE id = 'schedule-existing'`).Scan(
		&name, &groupName, &scriptPath, &arguments, &expression, &timeout,
	); err != nil {
		t.Fatal(err)
	}
	if name != "Existing" || groupName != "" || scriptPath != "existing.cmd" || arguments != "--safe" || expression != "0 2 * * *" || timeout != 45 {
		t.Fatalf("migrated Schedule changed: name=%q group=%q script=%q arguments=%q expression=%q timeout=%d",
			name, groupName, scriptPath, arguments, expression, timeout)
	}
	if _, err := os.Stat(path + ".pre-migration-v11"); err != nil {
		t.Fatalf("pre-migration snapshot: %v", err)
	}
}

func TestOpenDatabaseMigratesLegacyScheduleNamesIntoManagedGroups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO schedules
			(id, name, group_name, script_path, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, created_at, updated_at)
			VALUES
			('schedule-ops-a', 'Ops A', 'Operations', 'a.cmd', '', '0 2 * * *', 0, 1, 1, 1, 10, 10),
			('schedule-ops-b', 'Ops B', 'operations', 'b.cmd', '', '0 3 * * *', 0, 1, 1, 1, 20, 20),
			('schedule-maint', 'Maintenance', 'Maintenance', 'c.cmd', '', '0 4 * * *', 0, 1, 1, 1, 30, 30)`,
		"DROP INDEX schedules_group_order_idx",
		"ALTER TABLE schedules DROP COLUMN group_id",
		"DROP TABLE schedule_groups",
		"PRAGMA user_version=13",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = openDatabase(path)
	if err != nil {
		t.Fatalf("migrate managed Schedule groups: %v", err)
	}
	defer db.Close()

	var groupCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM schedule_groups").Scan(&groupCount); err != nil {
		t.Fatal(err)
	}
	if groupCount != 2 {
		t.Fatalf("Schedule group count=%d, want 2", groupCount)
	}
	var firstGroupID, secondGroupID, firstName, secondName string
	if err := db.QueryRow(`SELECT
			(SELECT group_id FROM schedules WHERE id = 'schedule-ops-a'),
			(SELECT group_id FROM schedules WHERE id = 'schedule-ops-b'),
			(SELECT g.name FROM schedules s JOIN schedule_groups g ON g.id = s.group_id WHERE s.id = 'schedule-ops-a'),
			(SELECT g.name FROM schedules s JOIN schedule_groups g ON g.id = s.group_id WHERE s.id = 'schedule-maint')`).Scan(
		&firstGroupID, &secondGroupID, &firstName, &secondName,
	); err != nil {
		t.Fatal(err)
	}
	if firstGroupID == "" || firstGroupID != secondGroupID || !strings.EqualFold(firstName, "Operations") || secondName != "Maintenance" {
		t.Fatalf("migrated Schedule groups: first=%q second=%q names=%q/%q",
			firstGroupID, secondGroupID, firstName, secondName)
	}
	if _, err := os.Stat(path + ".pre-migration-v13"); err != nil {
		t.Fatalf("pre-migration snapshot: %v", err)
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
