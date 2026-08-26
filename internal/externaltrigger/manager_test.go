package externaltrigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testManager(t *testing.T, now time.Time) (*Manager, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("initialize schema: %v", err)
		}
	}
	return New(db, Options{Now: func() time.Time { return now }, SecretsDirectory: filepath.Join(t.TempDir(), "secrets")}), db
}

func TestCreateKeyAndResolveEnabledLogEntry(t *testing.T) {
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	manager, db := testManager(t, now)
	expiresAt := now.Add(24 * time.Hour)
	key, provisionalSecret, err := manager.CreateKey(context.Background(), CreateKeyInput{
		Label: "CI pipeline", Enabled: true, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if !strings.HasPrefix(provisionalSecret, "sbk_") || strings.Contains(key.TokenHint, provisionalSecret) {
		t.Fatalf("secret=%q hint=%q", provisionalSecret, key.TokenHint)
	}
	var storedHash string
	if err := db.QueryRow("SELECT token_hash FROM external_trigger_keys WHERE id = ?", key.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == provisionalSecret || strings.Contains(storedHash, provisionalSecret) {
		t.Fatal("plaintext key was persisted")
	}
	if _, err := manager.secretStore.get(key.ID); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("external key remained recoverable after creation: %v", err)
	}

	entry, secret, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "deployment-log", Label: "Deployment callback", Type: ActionLog,
		Enabled: true, Config: LogConfig{File: "/logs/deploy.log", Category: "deploy", MaxMessageBytes: 1024},
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if _, _, err := manager.Resolve(context.Background(), provisionalSecret, "deployment-log"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("provisional key remained valid after capability binding: %v", err)
	}
	resolvedKey, resolvedEntry, err := manager.Resolve(context.Background(), secret, "deployment-log")
	if err != nil {
		t.Fatalf("resolve trigger: %v", err)
	}
	if resolvedKey.ID != key.ID || resolvedEntry.ID != entry.ID || resolvedEntry.Type != ActionLog {
		t.Fatalf("resolved key=%#v entry=%#v", resolvedKey, resolvedEntry)
	}
	if _, _, err := manager.Resolve(context.Background(), "sbk_invalid", "deployment-log"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid key error = %v", err)
	}
}

func TestGroupSupportsMultipleImmutableEntriesWithoutDeletingKeys(t *testing.T) {
	manager, _ := testManager(t, time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC))
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Single capability", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "notice", Label: "Notice", Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "/logs/notice.log", MaxMessageBytes: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "other", Label: "Other", Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "/logs/other.log", MaxMessageBytes: 100},
	})
	if err != nil || second.GroupID != key.GroupID {
		t.Fatalf("second capability=%#v error=%v", second, err)
	}
	updated, err := manager.UpdateEntry(context.Background(), UpdateEntryInput{
		ID: entry.ID, Name: entry.Name, Label: entry.Label, Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "/logs/changed.log", MaxMessageBytes: 100},
	})
	if err != nil || updated.Target != "/logs/changed.log" {
		t.Fatalf("capability scope update = %#v error=%v", updated, err)
	}
	if err := manager.DeleteEntry(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Key(context.Background(), key.ID); err != nil {
		t.Fatalf("group credential was deleted with one capability: %v", err)
	}
}

func TestEntryApprovalRequirementDefaultsOffAndPersistsUpdates(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	group, err := manager.CreateGroup(context.Background(), "Approval hooks", "approval-hooks")
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		GroupID: group.ID, Name: "notice", Label: "Notice", Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "/logs/notice.log", MaxMessageBytes: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.RequireApproval {
		t.Fatal("new entries must not require approval by default")
	}
	updated, err := manager.UpdateEntry(context.Background(), UpdateEntryInput{
		ID: entry.ID, Name: entry.Name, Label: entry.Label, Type: entry.Type, Enabled: true, RequireApproval: true,
		Config: LogConfig{File: "/logs/notice.log", MaxMessageBytes: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.RequireApproval {
		t.Fatal("updated entry did not retain its approval requirement")
	}
}

func TestApprovalCanBeClaimedOnlyOnceAndRetainsInvocationInput(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	approval := Approval{
		ID: "approval-one", OccurredAt: now, KeyID: "key-one", KeyLabel: "CI", EntryID: "entry-one", EntryName: "deploy",
		ActionType: ActionVariable, EntryUpdatedAt: now.Add(-time.Minute), PayloadJSON: `{"value":"production"}`, Source: "127.0.0.1",
	}
	if err := manager.CreateApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.ListApprovals(context.Background(), ApprovalPending, 50)
	if err != nil || len(pending) != 1 || pending[0].PayloadJSON != approval.PayloadJSON {
		t.Fatalf("pending approvals=%#v error=%v", pending, err)
	}
	claimed, err := manager.ClaimApproval(context.Background(), approval.ID, "admin-one")
	if err != nil || claimed.Status != ApprovalProcessing || claimed.DecidedBy != "admin-one" {
		t.Fatalf("claimed approval=%#v error=%v", claimed, err)
	}
	if _, err := manager.ClaimApproval(context.Background(), approval.ID, "admin-two"); !errors.Is(err, ErrApprovalNotPending) {
		t.Fatalf("second claim error=%v", err)
	}
	if err := manager.CompleteApproval(context.Background(), approval.ID, ApprovalApproved, "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	completed, err := manager.Approval(context.Background(), approval.ID)
	if err != nil || completed.Status != ApprovalApproved || completed.Result != "succeeded" {
		t.Fatalf("completed approval=%#v error=%v", completed, err)
	}
}

func TestApprovalRecoveryMarksTheInvocationUnknown(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	approval := Approval{ID: "approval-recovery", OccurredAt: now, KeyID: "key-one", KeyLabel: "CI", EntryID: "entry-one", EntryName: "deploy", ActionType: ActionUpload, EntryUpdatedAt: now, PayloadJSON: `{}`}
	if err := manager.CreateApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordInvocation(context.Background(), Invocation{ID: approval.ID, OccurredAt: now, KeyID: approval.KeyID, KeyLabel: approval.KeyLabel, EntryID: approval.EntryID, EntryName: approval.EntryName, ActionType: approval.ActionType, Result: "pending_approval", HTTPStatus: 202}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ClaimApproval(context.Background(), approval.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := manager.Approval(context.Background(), approval.ID)
	if err != nil || recovered.Status != ApprovalFailed || recovered.Result != "unknown" {
		t.Fatalf("recovered approval=%#v error=%v", recovered, err)
	}
	requests, err := manager.ListInvocations(context.Background(), 10)
	if err != nil || len(requests) != 1 || requests[0].Result != "unknown" || requests[0].HTTPStatus != 500 {
		t.Fatalf("recovered invocations=%#v error=%v", requests, err)
	}
}

func TestApprovalAndInvocationFinalizeInOneTransaction(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	approval := Approval{ID: "approval-finalize", OccurredAt: now, KeyID: "key-one", KeyLabel: "CI", EntryID: "entry-one", EntryName: "deploy", ActionType: ActionVariable, EntryUpdatedAt: now, PayloadJSON: `{"value":"production"}`}
	if err := manager.CreateApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordInvocation(context.Background(), Invocation{ID: approval.ID, OccurredAt: now, KeyID: approval.KeyID, KeyLabel: approval.KeyLabel, EntryID: approval.EntryID, EntryName: approval.EntryName, ActionType: approval.ActionType, Result: "pending_approval", HTTPStatus: 202}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ClaimApproval(context.Background(), approval.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := manager.FinalizeApprovalInvocation(context.Background(), approval.ID, ApprovalApproved, "succeeded", 200, 0, "", "production"); err != nil {
		t.Fatal(err)
	}
	completed, err := manager.Approval(context.Background(), approval.ID)
	if err != nil || completed.Status != ApprovalApproved {
		t.Fatalf("completed approval=%#v error=%v", completed, err)
	}
	requests, err := manager.ListInvocations(context.Background(), 10)
	if err != nil || len(requests) != 1 || requests[0].Result != "succeeded" || requests[0].HTTPStatus != 200 {
		t.Fatalf("completed invocations=%#v error=%v", requests, err)
	}
}

func TestGroupKeysShareEveryCallablePathAndKeepIndependentLifetimes(t *testing.T) {
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	group, err := manager.CreateGroup(context.Background(), "Deploy hooks", "deploy-hooks")
	if err != nil {
		t.Fatal(err)
	}
	first, firstSecret, err := manager.CreateKey(context.Background(), CreateKeyInput{GroupID: group.ID, Label: "CI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Hour)
	second, secondSecret, err := manager.CreateKey(context.Background(), CreateKeyInput{GroupID: group.ID, Label: "On call", Enabled: true, ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"deploy", "notify"} {
		if _, _, err := manager.CreateEntry(context.Background(), CreateEntryInput{GroupID: group.ID, Name: name, Label: name, Type: ActionLog, Enabled: true, Config: LogConfig{File: "/logs/" + name + ".log", MaxMessageBytes: 100}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, secret := range []string{firstSecret, secondSecret} {
		for _, name := range []string{"deploy", "notify"} {
			key, entry, err := manager.Resolve(context.Background(), secret, name)
			if err != nil || key.GroupID != group.ID || entry.Name != name {
				t.Fatalf("resolve key=%#v entry=%#v err=%v", key, entry, err)
			}
		}
	}
	if err := manager.SetKeyEnabled(context.Background(), first.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Resolve(context.Background(), firstSecret, "deploy"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("disabled key error=%v", err)
	}
	if _, _, err := manager.Resolve(context.Background(), secondSecret, "deploy"); err != nil {
		t.Fatalf("second key affected by first key toggle: %v", err)
	}
	_ = second
}

func TestGroupCallNameOwnsTheExternalRoute(t *testing.T) {
	manager, _ := testManager(t, time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC))
	group, err := manager.CreateGroup(context.Background(), "Deployment automation", "deploy-hooks")
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := manager.CreateKey(context.Background(), CreateKeyInput{GroupID: group.ID, Label: "CI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateEntry(context.Background(), CreateEntryInput{GroupID: group.ID, Name: "deploy", Label: "Deploy", Type: ActionLog, Enabled: true, Config: LogConfig{File: "/logs/deploy.log", MaxMessageBytes: 100}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ResolveScoped(context.Background(), secret, "deploy-hooks", "deploy"); err != nil {
		t.Fatalf("resolve call name: %v", err)
	}
	if _, _, err := manager.ResolveScoped(context.Background(), secret, group.Label, "deploy"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("display label unexpectedly resolved: %v", err)
	}
	updated, err := manager.UpdateGroup(context.Background(), group.ID, "Release automation", "release-hooks")
	if err != nil || updated.CallName != "release-hooks" || key.GroupID != updated.ID {
		t.Fatalf("update group = %#v key=%#v err=%v", updated, key, err)
	}
	if _, _, err := manager.ResolveScoped(context.Background(), secret, "release-hooks", "deploy"); err != nil {
		t.Fatalf("resolve updated call name: %v", err)
	}
	if _, err := manager.CreateGroup(context.Background(), "Other", "release-hooks"); !errors.Is(err, ErrGroupNameExists) {
		t.Fatalf("duplicate call name error = %v", err)
	}
}

func TestGlobalControlDefaultsEnabledAndPersistsToggle(t *testing.T) {
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	manager, db := testManager(t, now)
	enabled, updatedAt, err := manager.GlobalEnabled(context.Background())
	if err != nil || !enabled || !updatedAt.IsZero() {
		t.Fatalf("default global control enabled=%v updated=%v err=%v", enabled, updatedAt, err)
	}
	if err := manager.SetGlobalEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	reopened := New(db, Options{Now: func() time.Time { return now.Add(time.Minute) }, SecretsDirectory: filepath.Join(t.TempDir(), "secrets")})
	enabled, updatedAt, err = reopened.GlobalEnabled(context.Background())
	if err != nil || enabled || !updatedAt.Equal(now) {
		t.Fatalf("persisted global control enabled=%v updated=%v err=%v", enabled, updatedAt, err)
	}
}

func TestQuickRunEntryRequiresPublishedRevisionAndDigest(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	for _, test := range []struct {
		name   string
		config QuickRunConfig
		valid  bool
	}{
		{name: "published", config: QuickRunConfig{QuickRunID: "quick-1", Revision: 3, ScriptSHA256: validDigest}, valid: true},
		{name: "missing revision", config: QuickRunConfig{QuickRunID: "quick-1", ScriptSHA256: validDigest}},
		{name: "missing digest", config: QuickRunConfig{QuickRunID: "quick-1", Revision: 3}},
		{name: "invalid digest", config: QuickRunConfig{QuickRunID: "quick-1", Revision: 3, ScriptSHA256: "not-a-digest"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := validateEntry("quick", "Quick run", ActionQuickRun, test.config)
			if test.valid && err != nil {
				t.Fatalf("valid published Quick Run rejected: %v", err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid published Quick Run error = %v", err)
			}
		})
	}
}

func TestKeyNamesAreUniqueIgnoringCaseAndSurroundingWhitespace(t *testing.T) {
	manager, _ := testManager(t, time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC))
	group, err := manager.CreateGroup(context.Background(), "Deployment", "deployment")
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := manager.CreateKey(context.Background(), CreateKeyInput{GroupID: group.ID, Label: "Webhook", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateKey(context.Background(), CreateKeyInput{GroupID: group.ID, Label: " webhook ", Enabled: true}); !errors.Is(err, ErrKeyLabelExists) {
		t.Fatalf("duplicate create error = %v", err)
	}
	second, _, err := manager.CreateKey(context.Background(), CreateKeyInput{GroupID: group.ID, Label: "Deploy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateKey(context.Background(), second.ID, "WEBHOOK", nil); !errors.Is(err, ErrKeyLabelExists) {
		t.Fatalf("duplicate update error = %v", err)
	}
	if err := manager.UpdateKey(context.Background(), first.ID, "WEBHOOK", nil); err != nil {
		t.Fatalf("updating a key's own name: %v", err)
	}
}

func TestRotateAndDeleteKeyNeverPersistRecoverableSecret(t *testing.T) {
	manager, _ := testManager(t, time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC))
	key, original, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Agent", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, rotated, err := manager.RotateKey(context.Background(), key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == original {
		t.Fatal("rotation did not replace the key")
	}
	if _, revealErr := manager.secretStore.get(key.ID); !errors.Is(revealErr, ErrSecretUnavailable) {
		t.Fatalf("rotated key remained recoverable: %v", revealErr)
	}
	if err := manager.DeleteKey(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.secretStore.get(key.ID); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("deleted key secret error=%v", err)
	}
}

func TestPurgeLegacyKeySecretsKeepsUnrelatedSecrets(t *testing.T) {
	manager, _ := testManager(t, time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC))
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Legacy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.secretStore.set(key.ID, "legacy-complete-key"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StoreSecret("remote-website:one", "remote-secret"); err != nil {
		t.Fatal(err)
	}
	if err := manager.PurgeLegacyKeySecrets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.secretStore.get(key.ID); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("legacy external key was not purged: %v", err)
	}
	if secret, err := manager.Secret("remote-website:one"); err != nil || secret != "remote-secret" {
		t.Fatalf("unrelated secret changed: secret=%q err=%v", secret, err)
	}
}

func TestResolveRejectsExpiredKeyAndDisabledEntry(t *testing.T) {
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	expiresAt := now.Add(time.Hour)
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Agent", Enabled: true, ExpiresAt: &expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	entry, secret, err := manager.CreateEntry(context.Background(), CreateEntryInput{KeyID: key.ID, Name: "notice", Label: "Notice", Type: ActionLog, Enabled: false, Config: LogConfig{File: "/logs/notice.log", MaxMessageBytes: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Resolve(context.Background(), secret, entry.Name); !errors.Is(err, ErrEntryDisabled) {
		t.Fatalf("disabled entry error = %v", err)
	}
	if err := manager.SetEntryEnabled(context.Background(), entry.ID, true); err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return expiresAt }
	if _, _, err := manager.Resolve(context.Background(), secret, entry.Name); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("expired key error = %v", err)
	}
}

func TestValidateVariableValueUsesConfiguredTypeBounds(t *testing.T) {
	minimum, maximum := int64(2), int64(5)
	integer := VariableConfig{VariableName: "retries", Type: VariableInteger, Minimum: &minimum, Maximum: &maximum}
	if value, err := ValidateVariableValue(integer, json.Number("4")); err != nil || value != "4" {
		t.Fatalf("integer value = %q, error = %v", value, err)
	}
	if _, err := ValidateVariableValue(integer, json.Number("6")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("out-of-range error = %v", err)
	}
	enumeration := VariableConfig{VariableName: "environment", Type: VariableEnum, Options: []string{"staging", "production"}}
	if _, err := ValidateVariableValue(enumeration, "root"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("enum error = %v", err)
	}
}

func TestCompleteInvocationUpdatesPendingRecord(t *testing.T) {
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	manager, db := testManager(t, now)
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Agent", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pending := Invocation{ID: "request-1", OccurredAt: now, KeyID: key.ID, KeyLabel: key.Label, EntryID: "entry", EntryName: "quick", ActionType: ActionQuickRun, Result: "processing"}
	if err := manager.RecordInvocation(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	completed := pending
	completed.Result = "accepted"
	completed.HTTPStatus = 202
	completed.Duration = 250 * time.Millisecond
	completed.RunID = "run-1"
	if err := manager.CompleteInvocation(context.Background(), completed); err != nil {
		t.Fatal(err)
	}
	var result, runID string
	var status int
	if err := db.QueryRow("SELECT result, http_status, run_id FROM external_trigger_requests WHERE id = ?", pending.ID).Scan(&result, &status, &runID); err != nil {
		t.Fatal(err)
	}
	if result != "accepted" || status != 202 || runID != "run-1" {
		t.Fatalf("completed invocation result=%q status=%d run=%q", result, status, runID)
	}
}

func TestCompletionFailureQueuesExactResultAndReconcilesWithoutRepeatingAction(t *testing.T) {
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	manager, database := testManager(t, now)
	ctx := context.Background()
	key, _, err := manager.CreateKey(ctx, CreateKeyInput{Label: "reconcile", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pending := Invocation{ID: "request-reconcile", OccurredAt: now, KeyID: key.ID, KeyLabel: key.Label, EntryID: "entry", EntryName: "quick", ActionType: ActionQuickRun, Result: "processing"}
	if err := manager.RecordInvocation(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_external_completion BEFORE UPDATE ON external_trigger_requests
		WHEN NEW.id='request-reconcile' BEGIN SELECT RAISE(FAIL, 'fixture completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	completed := pending
	completed.Result, completed.HTTPStatus, completed.RunID, completed.Message = "accepted", 202, "run-1", "started once"
	if err := manager.CompleteInvocation(ctx, completed); err == nil || !strings.Contains(err.Error(), "queued for retry") {
		t.Fatalf("completion error=%v", err)
	}
	entries, err := os.ReadDir(manager.reconciliationDirectory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("queued entries=%v err=%v", entries, err)
	}
	if _, err := database.Exec(`DROP TRIGGER fail_external_completion`); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileInvocations(ctx, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var result, runID, message string
	var status int
	if err := database.QueryRow(`SELECT result,http_status,run_id,message FROM external_trigger_requests WHERE id=?`, pending.ID).Scan(&result, &status, &runID, &message); err != nil {
		t.Fatal(err)
	}
	if result != "accepted" || status != 202 || runID != "run-1" || message != "started once" {
		t.Fatalf("reconciled result=%q status=%d run=%q message=%q", result, status, runID, message)
	}
}

func TestStaleProcessingInvocationBecomesUnknown(t *testing.T) {
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	manager, database := testManager(t, now)
	ctx := context.Background()
	key, _, err := manager.CreateKey(ctx, CreateKeyInput{Label: "stale", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	invocation := Invocation{ID: "request-stale", OccurredAt: now.Add(-time.Hour), KeyID: key.ID, KeyLabel: key.Label, EntryID: "entry", EntryName: "quick", ActionType: ActionQuickRun, Result: "processing"}
	if err := manager.RecordInvocation(ctx, invocation); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileInvocations(ctx, now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var result, message string
	if err := database.QueryRow(`SELECT result,message FROM external_trigger_requests WHERE id=?`, invocation.ID).Scan(&result, &message); err != nil {
		t.Fatal(err)
	}
	if result != "unknown" || !strings.Contains(message, "not recorded") {
		t.Fatalf("stale result=%q message=%q", result, message)
	}
}
