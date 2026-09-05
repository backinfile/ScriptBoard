package scheduler

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/runmanager"
)

type overlapProcess struct {
	done chan struct{}
	once sync.Once
}

func (p *overlapProcess) Stdout() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }
func (p *overlapProcess) Stderr() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }
func (p *overlapProcess) Wait() error           { <-p.done; return nil }
func (p *overlapProcess) Terminate(bool) error  { p.once.Do(func() { close(p.done) }); return nil }
func (p *overlapProcess) Close() error          { return nil }

type overlapLauncher struct{}

func (overlapLauncher) RuntimeIdentity() string { return "review-fixture" }
func (overlapLauncher) Launch(context.Context, runmanager.LaunchRequest) (runmanager.ManagedProcess, string, error) {
	return &overlapProcess{done: make(chan struct{})}, "fixture", nil
}

func TestScheduleConcurrentRunNowDisallowsOverlap(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "automatic"}[automatic], func(t *testing.T) { testScheduleConcurrentStart(t, automatic) })
	}
}

func testScheduleConcurrentStart(t *testing.T, automatic bool) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	for _, statement := range []string{`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			script_path TEXT NOT NULL,
			script_path_key TEXT NOT NULL,
			script_sha256 TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			template_arguments_json TEXT NOT NULL DEFAULT '[]',
			arguments_json TEXT NOT NULL,
			executor TEXT NOT NULL,
			source_type TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			started_at INTEGER,
			finished_at INTEGER,
			exit_code INTEGER,
			error TEXT NOT NULL DEFAULT '',
			timeout_seconds INTEGER NOT NULL DEFAULT 0,
			log_path TEXT NOT NULL
			, source_name TEXT NOT NULL DEFAULT ''
			, source_id TEXT NOT NULL DEFAULT ''
			, runtime_identity TEXT NOT NULL DEFAULT ''
			, log_expired INTEGER NOT NULL DEFAULT 0
			, log_incomplete INTEGER NOT NULL DEFAULT 0
			, log_truncated INTEGER NOT NULL DEFAULT 0
			, dropped_bytes INTEGER NOT NULL DEFAULT 0
			, script_kind TEXT NOT NULL DEFAULT 'host_file'
			, working_directory TEXT NOT NULL DEFAULT ''
			, working_directory_key TEXT NOT NULL DEFAULT ''
			, source_filename TEXT NOT NULL DEFAULT ''
			, source_expired INTEGER NOT NULL DEFAULT 0
			, source_audit_event_id INTEGER REFERENCES audit_events(id)
			, log_bytes INTEGER NOT NULL DEFAULT -1
			, initiated_by_user_id TEXT NOT NULL DEFAULT ''
			, initiated_by_username TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE schedules (id TEXT,name TEXT,script_path TEXT,arguments_template TEXT,timeout_seconds INTEGER,allow_overlap INTEGER,deleted INTEGER,expression TEXT,enabled INTEGER,next_fire_at INTEGER,updated_at INTEGER)`,
		`CREATE TABLE schedule_triggers (id TEXT,schedule_id TEXT,scheduled_for INTEGER,result TEXT,run_id TEXT,error TEXT)`} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	files, err := hostfiles.Open(hostfiles.Options{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	name := "fixture.sh"
	if runtime.GOOS == "windows" {
		name = "fixture.cmd"
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("@echo off\r\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runs := runmanager.NewWithLauncher(db, files, root, 0, nil, overlapLauncher{})
	defer runs.Close()
	if _, err := db.Exec(`INSERT INTO schedules VALUES ('review','review',?,'',0,0,0,'* * * * *',1,1,1)`, path); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	m := &Manager{db: db, runs: runs, now: time.Now, loadVariables: func() (map[string]string, error) { entered <- struct{}{}; <-release; return nil, nil }}
	if automatic {
		done := make(chan struct{})
		go func() { m.fireDue(); close(done) }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("automatic start did not reach barrier")
		}
		_, err := runs.Start(runmanager.StartRequest{ScriptPath: path})
		if err != nil {
			close(release)
			<-done
			t.Fatal(err)
		}
		close(release)
		<-done
		var result string
		if err := db.QueryRow("SELECT result FROM schedule_triggers").Scan(&result); err != nil || result != "skipped" {
			t.Fatalf("automatic overlap result=%q error=%v", result, err)
		}
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM runs").Scan(&count); err != nil || count != 1 {
			t.Fatalf("running executions=%d error=%v", count, err)
		}
		return
	}

	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := m.RunNow("review"); results <- err }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("concurrent calls did not reach barrier")
		}
	}
	close(release)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else {
			t.Log(err)
		}
	}
	if successes != 1 {
		t.Fatalf("allow_overlap=false accepted %d concurrent starts; expected one", successes)
	}
}
