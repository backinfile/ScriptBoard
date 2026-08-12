package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/app"
	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/config"
	"scriptboard/internal/secretstore"
	"scriptboard/internal/statebackup"
)

func TestWebStartupFilesIncludeOnlyConfiguredWebOwnedFiles(t *testing.T) {
	loaded := config.Config{
		AdminPasswordFile: `C:\\secrets\\admin-password.txt`,
		TLSCert:           `C:\\tls\\server.crt`,
		TLSKey:            `C:\\tls\\server.key`,
	}
	want := []string{loaded.AdminPasswordFile, loaded.TLSCert, loaded.TLSKey}
	got := webStartupFiles(loaded)
	if len(got) != len(want) {
		t.Fatalf("startup files=%v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("startup files[%d]=%q, want %q", index, got[index], want[index])
		}
	}
	if empty := webStartupFiles(config.Config{}); len(empty) != 0 {
		t.Fatalf("empty config exposed startup files: %v", empty)
	}
}

func TestRequireManagedConfigPathRejectsDifferentFile(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed.yaml")
	other := filepath.Join(root, "other.yaml")
	for _, path := range []string{managed, other} {
		if err := os.WriteFile(path, []byte("state_root: fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := requireManagedConfigPath(managed, managed); err != nil {
		t.Fatalf("managed config rejected: %v", err)
	}
	if err := requireManagedConfigPath(other, managed); err == nil {
		t.Fatal("different config file was accepted for the managed installation")
	}
}

func TestRequireManagedConfigPathAcceptsSameMissingDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := requireManagedConfigPath(path, path); err != nil {
		t.Fatalf("same absent default config rejected: %v", err)
	}
}

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
	if !strings.Contains(string(help), "service install [--start]") {
		t.Fatalf("help does not document the complete managed install option:\n%s", help)
	}
}

func TestServiceInstallStartOptionIsRemovedBeforeConfigParsing(t *testing.T) {
	start, remaining := takeBooleanArgument([]string{"--config", "fixture.yaml", "--start"}, "--start")
	if !start {
		t.Fatal("service install did not recognize --start")
	}
	if got := strings.Join(remaining, " "); got != "--config fixture.yaml" {
		t.Fatalf("remaining service install arguments = %q", got)
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

func TestBackupCreateWorksWithoutOpeningTheWebApplication(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	passphraseFile := filepath.Join(t.TempDir(), "backup-passphrase")
	passphrase := []byte("correct horse battery staple for cli backup")
	if err := os.WriteFile(passphraseFile, append(append([]byte(nil), passphrase...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "state.sbsb")
	if err := run([]string{"backup", "create", "--state-root", stateRoot, "--output", output, "--passphrase-file", passphraseFile}); err != nil {
		t.Fatal(err)
	}
	manifest, err := statebackup.Inspect(context.Background(), output, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID == "" || manifest.SchemaVersion != 43 {
		t.Fatalf("backup manifest = %#v", manifest)
	}
}

func TestBackupRestoreReanchorsAuditAndPreservesPreviousCheckpoint(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	passphraseFile := filepath.Join(t.TempDir(), "backup-passphrase")
	passphrase := []byte("correct horse battery staple for cli restore")
	if err := os.WriteFile(passphraseFile, passphrase, 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "state.sbsb")
	if err := run([]string{"backup", "create", "--state-root", stateRoot, "--output", archivePath, "--passphrase-file", passphraseFile}); err != nil {
		t.Fatal(err)
	}
	manifest, err := statebackup.Inspect(context.Background(), archivePath, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"backup", "restore", "--state-root", stateRoot, "--archive", archivePath, "--passphrase-file", passphraseFile, "--confirm-backup-id", manifest.ID}); err != nil {
		t.Fatal(err)
	}
	database, err := openEmergencyDatabaseReadOnly(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := verifySignedAuditCheckpoint(context.Background(), stateRoot, auditlog.New(database)); err != nil {
		t.Fatalf("restored audit checkpoint verification failed: %v", err)
	}
	var restoreEvents int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'state_backup.restore' AND target = ?`, manifest.ID).Scan(&restoreEvents); err != nil || restoreEvents != 1 {
		t.Fatalf("restore audit events = %d, err = %v", restoreEvents, err)
	}
	preserved := filepath.Join(filepath.Dir(stateRoot), filepath.Base(stateRoot)+".before-restore-"+manifest.ID)
	if info, err := os.Stat(filepath.Join(preserved, "external-audit-checkpoint.before-restore.json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("preserved checkpoint info=%v err=%v", info, err)
	}
}

func TestBackupRecoverHostRewrapsExternalKeysAndRestoresAnEmptyCanonicalRoot(t *testing.T) {
	parent := t.TempDir()
	stateRoot := filepath.Join(parent, "state")
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	application, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	archivePassphraseFile := filepath.Join(parent, "archive-passphrase")
	recoveryPassphraseFile := filepath.Join(parent, "recovery-passphrase")
	archivePassphrase := []byte("archive passphrase for replacement host drill")
	recoveryPassphrase := []byte("separate recovery material passphrase")
	if err := os.WriteFile(archivePassphraseFile, archivePassphrase, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryPassphraseFile, recoveryPassphrase, 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(parent, "state.sbsb")
	recoveryPath := filepath.Join(parent, "host-recovery.sbhr")
	if err := run([]string{"backup", "create", "--state-root", stateRoot, "--output", archivePath, "--passphrase-file", archivePassphraseFile}); err != nil {
		t.Fatal(err)
	}
	manifest, err := statebackup.Inspect(context.Background(), archivePath, archivePassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"backup", "export-recovery", "--state-root", stateRoot, "--output", recoveryPath, "--passphrase-file", recoveryPassphraseFile}); err != nil {
		t.Fatal(err)
	}
	credentialPath, err := secretstore.KeyPathForStateRoot(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, signingPath, checkpointPath, err := auditcheckpoint.PathsForStateRoot(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	recoveryBody, err := os.ReadFile(recoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	signingBody, err := os.ReadFile(signingPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(recoveryBody, signingBody) || bytes.Contains(recoveryBody, []byte("credential_key")) || bytes.Contains(recoveryBody, []byte("audit_signing_key")) {
		t.Fatal("host recovery material exposes plaintext key fields")
	}
	if err := os.RemoveAll(stateRoot); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{credentialPath, signingPath, checkpointPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"backup", "recover-host", "--state-root", stateRoot,
		"--archive", archivePath, "--passphrase-file", archivePassphraseFile,
		"--recovery-material", recoveryPath, "--recovery-passphrase-file", recoveryPassphraseFile,
		"--confirm-backup-id", manifest.ID}); err != nil {
		t.Fatal(err)
	}
	database, err := openEmergencyDatabaseReadOnly(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := verifySignedAuditCheckpoint(context.Background(), stateRoot, auditlog.New(database)); err != nil {
		t.Fatalf("recovered host audit verification failed: %v", err)
	}
	var recoveryEvents int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'state_backup.recover_host' AND target = ?`, manifest.ID).Scan(&recoveryEvents); err != nil || recoveryEvents != 1 {
		t.Fatalf("host recovery audit events=%d err=%v", recoveryEvents, err)
	}
}

func TestAuditVerifyWorksWithoutOpeningTheWebApplication(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
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
	if err := run([]string{"audit", "verify", "--state-root", stateRoot, "--json"}); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Valid            bool   `json:"valid"`
		Events           int    `json:"events"`
		SignedCheckpoint string `json:"signed_checkpoint"`
	}
	if err := json.NewDecoder(output).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Valid || result.Events < 1 || result.SignedCheckpoint != "valid" {
		t.Fatalf("audit verification output = %+v", result)
	}
}

func TestAuditVerifyDoesNotBootstrapAMissingSignedCheckpoint(t *testing.T) {
	stateRoot := initializedStateRoot(t)
	_, _, checkpointPath, err := auditcheckpoint.PathsForStateRoot(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(checkpointPath); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"audit", "verify", "--state-root", stateRoot}); err == nil {
		t.Fatal("audit verify trusted a missing external checkpoint")
	}
	if _, err := os.Stat(checkpointPath); !os.IsNotExist(err) {
		t.Fatalf("read-only audit verify recreated checkpoint: %v", err)
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
	assertSignedCheckpointAtLatestAudit(t, stateRoot, database)
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

func assertSignedCheckpointAtLatestAudit(t *testing.T, stateRoot string, database *sql.DB) {
	t.Helper()
	_, _, checkpointPath, err := auditcheckpoint.PathsForStateRoot(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint struct {
		EventID int64 `json:"event_id"`
	}
	if err := json.Unmarshal(body, &checkpoint); err != nil {
		t.Fatal(err)
	}
	var latest int64
	if err := database.QueryRow("SELECT COALESCE(MAX(id), 0) FROM audit_events").Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if checkpoint.EventID != latest {
		t.Fatalf("signed checkpoint event=%d, latest audit=%d", checkpoint.EventID, latest)
	}
}
