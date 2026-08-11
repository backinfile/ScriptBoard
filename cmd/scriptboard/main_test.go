package main

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/auditlog"
)

func TestHelpDoesNotDocumentRemovedManagedRootShortcuts(t *testing.T) {
	originalStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "scriptboard-help-*.txt")
	if err != nil {
		t.Fatalf("create help output: %v", err)
	}
	t.Cleanup(func() {
		_ = output.Close()
	})
	os.Stdout = output
	t.Cleanup(func() {
		os.Stdout = originalStdout
	})

	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("show help: %v", err)
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind help output: %v", err)
	}
	help, err := io.ReadAll(output)
	if err != nil {
		t.Fatalf("read help output: %v", err)
	}
	for _, removed := range []string{"--here", "--managed-root"} {
		if strings.Contains(string(help), removed) {
			t.Fatalf("help still documents removed option %s:\n%s", removed, help)
		}
	}
}

func TestAuditVerifyWorksWithoutOpeningTheWebApplication(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', authentication_assurance TEXT NOT NULL DEFAULT '', previous_hash TEXT NOT NULL DEFAULT '', event_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY CHECK(id = 1), anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state VALUES (1, '', '')`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := auditlog.New(database).Append(context.Background(), auditlog.Event{
		OccurredAt: "1786410000", Action: "test", Target: "resource", Result: "succeeded", SourceAddress: "local",
	}); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	originalStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "scriptboard-audit-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = output
	t.Cleanup(func() {
		os.Stdout = originalStdout
		_ = output.Close()
	})
	if err := run([]string{"audit", "verify", "--state-root", stateRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(output)
	if !strings.Contains(string(content), "1 条事件") {
		t.Fatalf("audit verification output = %s", content)
	}
}
