package web

import (
	"path/filepath"
	"testing"
	"time"

	"scriptboard/internal/hostfiles"
)

func TestSchedulerDisablesInvalidStoredExpressionsAtStartup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	config := Config{
		StateRoot: filepath.Join(root, "state"),
		SchedulerNow: func() time.Time {
			return time.Date(2026, 7, 26, 8, 30, 0, 0, time.UTC)
		},
	}
	application, err := Open(config)
	if err != nil {
		t.Fatalf("create application database: %v", err)
	}
	now := config.SchedulerNow().UnixNano()
	scriptPath := filepath.Join(root, "reports", "daily.ps1")
	// Keep setup on the application-owned connection so Windows never retains a second WAL handle during cleanup.
	if _, err := application.db.Exec(`INSERT INTO schedules
		(id, name, script_path, script_path_key, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, created_at, updated_at)
		VALUES ('invalid-cron', 'Invalid Cron', ?, ?, '', '0 9 ? * *', 0, 1, 1, ?, ?, ?)`,
		scriptPath, hostfiles.ComparisonKey(scriptPath), now, now, now); err != nil {
		_ = application.Close()
		t.Fatalf("insert invalid schedule: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("close initial application: %v", err)
	}

	application, err = Open(config)
	if err != nil {
		t.Fatalf("reopen application: %v", err)
	}
	defer application.Close()

	var enabled bool
	if err := application.db.QueryRow("SELECT enabled FROM schedules WHERE id = 'invalid-cron'").Scan(&enabled); err != nil {
		t.Fatalf("read invalid schedule: %v", err)
	}
	if enabled {
		t.Fatal("invalid stored schedule remained enabled")
	}
	var auditCount int
	if err := application.db.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'disable_invalid_schedule' AND target = '0 9 ? * *' AND result = 'failed' AND source_address = 'scheduler'`).Scan(&auditCount); err != nil {
		t.Fatalf("read invalid schedule audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("invalid schedule audit count = %d, want 1", auditCount)
	}
}
