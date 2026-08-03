package runmanager

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/hostfiles"
)

func TestGetMetadataDoesNotReadRunLog(t *testing.T) {
	root := t.TempDir()
	db := openRunMetadataTestDB(t, root)
	defer db.Close()

	logPath := filepath.Join(root, "run.jsonl")
	if err := os.WriteFile(logPath, []byte(`{"sequence":1,"time":1,"source":"stdout","data":"aGVsbG8K"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	insertRunMetadataTestRow(t, db, "run-1", logPath, time.Now().UTC())
	manager := New(db, testHostFiles(t), root, 0, nil)

	metadata, err := manager.GetMetadata("run-1")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if len(metadata.Events) != 0 {
		t.Fatalf("GetMetadata returned %d log events, want none", len(metadata.Events))
	}

	full, err := manager.Get("run-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(full.Events) != 1 || full.Events[0].Data != "hello\n" {
		t.Fatalf("Get events = %#v", full.Events)
	}
}

func TestFollowEventsResumesAfterSequenceAndReturnsFinalStatus(t *testing.T) {
	root := t.TempDir()
	db := openRunMetadataTestDB(t, root)
	defer db.Close()

	logPath := filepath.Join(root, "run.jsonl")
	log := "" +
		`{"sequence":1,"time":1,"source":"stdout","data":"Zmlyc3QK"}` + "\n" +
		`{"sequence":2,"time":2,"source":"stderr","data":"c2Vjb25kCg=="}` + "\n"
	if err := os.WriteFile(logPath, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	insertRunMetadataTestRow(t, db, "run-1", logPath, time.Now().UTC())
	manager := New(db, testHostFiles(t), root, 0, nil)

	var events []Event
	status, err := manager.FollowEvents(context.Background(), "run-1", 1, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("FollowEvents: %v", err)
	}
	if status != "succeeded" {
		t.Fatalf("final status = %q, want succeeded", status)
	}
	if len(events) != 1 || events[0].Sequence != 2 || events[0].Data != "second\n" {
		t.Fatalf("events = %#v", events)
	}
}

func TestFollowEventsWakesWhenAnActiveRunAppendsOutput(t *testing.T) {
	root := t.TempDir()
	db := openRunMetadataTestDB(t, root)
	defer db.Close()

	logPath := filepath.Join(root, "run.jsonl")
	if err := os.WriteFile(logPath, []byte(`{"sequence":1,"time":1,"source":"stdout","data":"Zmlyc3QK"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	insertRunMetadataTestRow(t, db, "run-1", logPath, time.Now().UTC())
	if _, err := db.Exec("UPDATE runs SET status = 'running' WHERE id = 'run-1'"); err != nil {
		t.Fatal(err)
	}
	manager := New(db, testHostFiles(t), root, 0, nil)
	active := &activeRun{changed: make(chan struct{}, 1)}
	manager.active["run-1"] = active

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		status string
		events []Event
		err    error
	}
	finished := make(chan result, 1)
	go func() {
		var events []Event
		status, err := manager.FollowEvents(ctx, "run-1", 1, func(event Event) error {
			events = append(events, event)
			return nil
		})
		finished <- result{status: status, events: events, err: err}
	}()

	if err := os.WriteFile(logPath, []byte(""+
		`{"sequence":1,"time":1,"source":"stdout","data":"Zmlyc3QK"}`+"\n"+
		`{"sequence":2,"time":2,"source":"stdout","data":"c2Vjb25kCg=="}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE runs SET status = 'succeeded' WHERE id = 'run-1'"); err != nil {
		t.Fatal(err)
	}
	active.signalChanged()

	got := <-finished
	if got.err != nil {
		t.Fatalf("FollowEvents: %v", got.err)
	}
	if got.status != "succeeded" || len(got.events) != 1 ||
		got.events[0].Sequence != 2 || got.events[0].Data != "second\n" {
		t.Fatalf("result = %#v", got)
	}
}

func openRunMetadataTestDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		script_path TEXT NOT NULL,
		script_sha256 TEXT NOT NULL,
		arguments_template TEXT NOT NULL,
		template_arguments_json TEXT NOT NULL,
		arguments_json TEXT NOT NULL,
		executor TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_name TEXT NOT NULL,
		source_id TEXT NOT NULL,
		runtime_identity TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		started_at INTEGER,
		finished_at INTEGER,
		exit_code INTEGER,
		error TEXT NOT NULL,
		timeout_seconds INTEGER NOT NULL,
		log_path TEXT NOT NULL,
		log_expired INTEGER NOT NULL,
		log_incomplete INTEGER NOT NULL,
		log_truncated INTEGER NOT NULL,
		dropped_bytes INTEGER NOT NULL,
		script_kind TEXT NOT NULL,
		working_directory TEXT NOT NULL,
		source_filename TEXT NOT NULL,
		source_expired INTEGER NOT NULL,
		source_audit_event_id INTEGER,
		log_bytes INTEGER NOT NULL DEFAULT -1,
		initiated_by_user_id TEXT NOT NULL DEFAULT '',
		initiated_by_username TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func TestCleanupLogsBackfillsUnknownLogSizes(t *testing.T) {
	root := t.TempDir()
	db := openRunMetadataTestDB(t, root)
	defer db.Close()

	logPath := filepath.Join(root, "run.jsonl")
	content := []byte(`{"sequence":1,"time":1,"source":"stdout","data":"aGVsbG8K"}` + "\n")
	if err := os.WriteFile(logPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	insertRunMetadataTestRow(t, db, "run-1", logPath, time.Now().UTC())
	manager := New(db, testHostFiles(t), root, 0, nil)

	if cleaned, err := manager.CleanupLogs(24*time.Hour, 1<<30); err != nil || cleaned != 0 {
		t.Fatalf("CleanupLogs = %d, %v", cleaned, err)
	}
	var size int64
	if err := db.QueryRow("SELECT log_bytes FROM runs WHERE id = 'run-1'").Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size != int64(len(content)) {
		t.Fatalf("log bytes = %d, want %d", size, len(content))
	}
}

func insertRunMetadataTestRow(t *testing.T, db *sql.DB, id, logPath string, createdAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO runs (
		id, script_path, script_sha256, arguments_template, template_arguments_json, arguments_json,
		executor, source_type, source_name, source_id, runtime_identity, status, created_at,
		error, timeout_seconds, log_path, log_expired, log_incomplete, log_truncated, dropped_bytes,
		script_kind, working_directory, source_filename, source_expired
	) VALUES (?, 'hello.cmd', 'digest', '', '[]', '[]', 'cmd.exe', 'admin/manual', '', '', 'tester',
		'succeeded', ?, '', 30, ?, 0, 0, 0, 0, 'host_file', '', '', 0)`,
		id, createdAt.UnixNano(), logPath); err != nil {
		t.Fatal(err)
	}
}

func testHostFiles(t *testing.T) *hostfiles.Manager {
	t.Helper()
	manager, err := hostfiles.Open(hostfiles.Options{})
	if err != nil {
		t.Fatalf("open host files: %v", err)
	}
	return manager
}
