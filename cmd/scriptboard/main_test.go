package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/app"
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

func TestUpdateVerifyPackageRequiresAllOfflineArtifacts(t *testing.T) {
	err := run([]string{"update", "verify-package"})
	if err == nil || !strings.Contains(err.Error(), "--archive") || !strings.Contains(err.Error(), "--manifest") || !strings.Contains(err.Error(), "--signature") {
		t.Fatalf("verify-package missing argument error = %v", err)
	}
}

func TestUpdateRepairCurrentRequiresExplicitConfirmation(t *testing.T) {
	err := run([]string{"update", "repair-current"})
	if err == nil || !strings.Contains(err.Error(), "REPAIR-CURRENT") {
		t.Fatalf("repair-current confirmation error = %v", err)
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

func TestEmergencyPauseExternalRequiresConfirmationAndAuditsAtomically(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	database := openStateDatabase(t, stateRoot)

	if err := run([]string{"emergency", "pause-external", "--state-root", stateRoot}); err == nil {
		t.Fatal("pause-external succeeded without explicit confirmation")
	}
	assertExternalControl(t, database, true)

	if err := run([]string{"emergency", "pause-external", "--state-root", stateRoot, "--confirm", "PAUSE-EXTERNAL"}); err != nil {
		t.Fatalf("pause external interfaces: %v", err)
	}
	assertExternalControl(t, database, false)
	assertLatestAudit(t, database, "emergency.external.pause", "external-interfaces", "succeeded")
}

func TestEmergencyRevokeKeyDisablesKeyAndPreservesForensicRecord(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	database := openStateDatabase(t, stateRoot)
	if _, err := database.Exec(`INSERT INTO external_trigger_keys
		(id, label, token_hash, token_hint, enabled, created_at, updated_at)
		VALUES ('key-emergency', 'Emergency fixture', 'hash', 'hint', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"emergency", "revoke-key", "--state-root", stateRoot,
		"--key-id", "key-emergency", "--confirm-key-id", "different"}); err == nil {
		t.Fatal("revoke-key succeeded with mismatched confirmation")
	}
	assertKeyEnabled(t, database, "key-emergency", true)

	if err := run([]string{"emergency", "revoke-key", "--state-root", stateRoot,
		"--key-id", "key-emergency", "--confirm-key-id", "key-emergency"}); err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	assertKeyEnabled(t, database, "key-emergency", false)
	assertLatestAudit(t, database, "emergency.external-key.revoke", "key-emergency", "succeeded")
}

func TestEmergencyExportEvidenceVerifiesChainAndRefusesOverwrite(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	database := openStateDatabase(t, stateRoot)
	if _, err := auditlog.New(database).Append(context.Background(), auditlog.Event{
		OccurredAt: "1786410000", Action: "fixture.action", Target: "fixture", Result: "succeeded", SourceAddress: "local",
	}); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	output := filepath.Join(t.TempDir(), "evidence.jsonl")
	if err := run([]string{"emergency", "export-evidence", "--state-root", stateRoot, "--output", output}); err != nil {
		t.Fatalf("export evidence: %v", err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) < 2 {
		t.Fatalf("evidence lines = %d, want metadata plus events: %s", len(lines), content)
	}
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not JSON: %v", index+1, err)
		}
	}
	if err := run([]string{"emergency", "export-evidence", "--state-root", stateRoot, "--output", output}); err == nil {
		t.Fatal("export evidence overwrote an existing forensic artifact")
	}
}

func TestEmergencyExportEvidenceRemovesPartialArtifactWhenAuditChainIsInvalid(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	database := openStateDatabase(t, stateRoot)
	if _, err := auditlog.New(database).Append(context.Background(), auditlog.Event{
		OccurredAt: "1786410000", Action: "fixture.action", Target: "fixture", Result: "succeeded", SourceAddress: "local",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE audit_events SET target = 'tampered' WHERE action = 'fixture.action'"); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	output := filepath.Join(t.TempDir(), "invalid-evidence.jsonl")
	if err := run([]string{"emergency", "export-evidence", "--state-root", stateRoot, "--output", output}); err == nil {
		t.Fatal("export evidence succeeded for a tampered audit chain")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("partial evidence artifact remains after verification failure: %v", err)
	}
}

func initializedStateRoot(t *testing.T) string {
	t.Helper()
	stateRoot := t.TempDir()
	application, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatalf("initialize application state: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("close initialized application: %v", err)
	}
	return stateRoot
}

func openStateDatabase(t *testing.T, stateRoot string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func assertExternalControl(t *testing.T, database *sql.DB, expected bool) {
	t.Helper()
	var enabled bool
	if err := database.QueryRow("SELECT enabled FROM external_trigger_control WHERE id = 1").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != expected {
		t.Fatalf("external trigger enabled = %v, want %v", enabled, expected)
	}
}

func assertKeyEnabled(t *testing.T, database *sql.DB, id string, expected bool) {
	t.Helper()
	var enabled bool
	if err := database.QueryRow("SELECT enabled FROM external_trigger_keys WHERE id = ?", id).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != expected {
		t.Fatalf("key enabled = %v, want %v", enabled, expected)
	}
}

func assertLatestAudit(t *testing.T, database *sql.DB, action, target, result string) {
	t.Helper()
	var actualAction, actualTarget, actualResult string
	if err := database.QueryRow(`SELECT action, target, result FROM audit_events ORDER BY id DESC LIMIT 1`).Scan(
		&actualAction, &actualTarget, &actualResult); err != nil {
		t.Fatal(err)
	}
	if actualAction != action || actualTarget != target || actualResult != result {
		t.Fatalf("latest audit = (%q, %q, %q), want (%q, %q, %q)", actualAction, actualTarget, actualResult, action, target, result)
	}
}
