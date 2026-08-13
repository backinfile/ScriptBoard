package scheduler

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/hostfiles"
)

func TestPreparedScheduleRejectsBrokerResourceSubstitution(t *testing.T) {
	manager := &Manager{prepareScript: func(string) (hostfiles.Script, hostfiles.PreparedDirectory, error) {
		return hostfiles.Script{Path: "/other/job.sh", Directory: "/other", Digest: "digest"}, hostfiles.PreparedDirectory{Path: "/other"}, nil
	}}
	if _, _, err := manager.preparedSchedule("schedule-1", "/scripts/job.sh"); err == nil {
		t.Fatal("accepted a Broker-prepared script for a different schedule path")
	}
}

func TestDueScheduleDoesNotExecuteWhenAdvanceFails(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schedules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		script_path TEXT NOT NULL,
		arguments_template TEXT NOT NULL,
		expression TEXT NOT NULL,
		timeout_seconds INTEGER NOT NULL,
		enabled INTEGER NOT NULL,
		allow_overlap INTEGER NOT NULL,
		next_fire_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		deleted INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create schedules table: %v", err)
	}
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO schedules
		(id, name, script_path, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, updated_at, deleted)
		VALUES ('schedule-1', 'critical job', '/scripts/job.sh', '', '* * * * *', 0, 1, 1, ?, ?, 0)`,
		now.Add(-time.Minute).UnixNano(), now.Add(-time.Minute).UnixNano()); err != nil {
		t.Fatalf("insert schedule: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_schedule_advance
		BEFORE UPDATE OF next_fire_at ON schedules
		BEGIN SELECT RAISE(FAIL, 'schedule store unavailable'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	variableLoads := 0
	manager := &Manager{
		db:  db,
		now: func() time.Time { return now },
		loadVariables: func() (map[string]string, error) {
			variableLoads++
			return nil, errors.New("stop before starting a real run")
		},
	}
	manager.fireDue()

	if variableLoads != 0 {
		t.Fatalf("variable loader called %d times after schedule advance failed, want 0", variableLoads)
	}
}

func TestDueScheduleRollsBackAdvanceWhenTriggerEvidenceCannotBeRecorded(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schedules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		script_path TEXT NOT NULL,
		arguments_template TEXT NOT NULL,
		expression TEXT NOT NULL,
		timeout_seconds INTEGER NOT NULL,
		enabled INTEGER NOT NULL,
		allow_overlap INTEGER NOT NULL,
		next_fire_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		deleted INTEGER NOT NULL
	);
	CREATE TABLE schedule_triggers (
		id TEXT PRIMARY KEY,
		schedule_id TEXT NOT NULL,
		scheduled_for INTEGER NOT NULL,
		result TEXT NOT NULL,
		run_id TEXT NOT NULL,
		error TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("create scheduler tables: %v", err)
	}
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	scheduledFor := now.Add(-time.Minute).UnixNano()
	if _, err := db.Exec(`INSERT INTO schedules
		(id, name, script_path, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, updated_at, deleted)
		VALUES ('schedule-1', 'critical job', '/scripts/job.sh', '', '* * * * *', 0, 1, 1, ?, ?, 0)`,
		scheduledFor, scheduledFor); err != nil {
		t.Fatalf("insert schedule: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_trigger_evidence
		BEFORE INSERT ON schedule_triggers
		BEGIN SELECT RAISE(FAIL, 'trigger store unavailable'); END`); err != nil {
		t.Fatalf("create trigger failure: %v", err)
	}

	variableLoads := 0
	manager := &Manager{
		db:  db,
		now: func() time.Time { return now },
		loadVariables: func() (map[string]string, error) {
			variableLoads++
			return nil, errors.New("stop before starting a real run")
		},
	}
	manager.fireDue()

	if variableLoads != 0 {
		t.Fatalf("variable loader called %d times without durable trigger evidence, want 0", variableLoads)
	}
	var storedNext int64
	if err := db.QueryRow("SELECT next_fire_at FROM schedules WHERE id = 'schedule-1'").Scan(&storedNext); err != nil {
		t.Fatal(err)
	}
	if storedNext != scheduledFor {
		t.Fatalf("next_fire_at = %d, want rollback to %d", storedNext, scheduledFor)
	}
}
