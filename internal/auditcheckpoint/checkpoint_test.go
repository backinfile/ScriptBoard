package auditcheckpoint

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/secretstore"
)

func TestSignedCheckpointDetectsValidLookingTailDeletion(t *testing.T) {
	ctx := context.Background()
	db := openCheckpointDB(t)
	audit := auditlog.New(db)
	firstID, err := audit.Append(ctx, auditlog.Event{OccurredAt: "1", Action: "first"})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := audit.Append(ctx, auditlog.Event{OccurredAt: "2", Action: "second"})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "state")
	vault, err := secretstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := New(Options{StateRoot: root, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoints.VerifyOrBootstrap(ctx, audit, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	if err := checkpoints.Write(ctx, audit, time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	var firstHash string
	if err := db.QueryRow(`SELECT event_hash FROM audit_events WHERE id = ?`, firstID).Scan(&firstHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM audit_events WHERE id = ?`, secondID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE audit_chain_state SET tail_hash = ? WHERE id = 1`, firstHash); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Verify(ctx); err != nil {
		t.Fatalf("fixture should still be a valid shortened local chain: %v", err)
	}
	before, err := os.ReadFile(checkpoints.checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoints.Write(ctx, audit, time.Unix(12, 0)); err == nil || !strings.Contains(err.Error(), "refuse") {
		t.Fatalf("checkpoint refresh laundered a shortened chain: %v", err)
	}
	after, err := os.ReadFile(checkpoints.checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed checkpoint refresh changed the trusted checkpoint")
	}
	if err := checkpoints.VerifyOrBootstrap(ctx, audit, time.Unix(12, 0)); err == nil || !strings.Contains(err.Error(), "checkpoint") {
		t.Fatalf("tail deletion checkpoint error=%v", err)
	}
}

func TestCheckpointSignatureAndExternalPresenceAreRequired(t *testing.T) {
	ctx := context.Background()
	db := openCheckpointDB(t)
	audit := auditlog.New(db)
	_, _ = audit.Append(ctx, auditlog.Event{OccurredAt: "1", Action: "first"})
	root := filepath.Join(t.TempDir(), "state")
	vault, _ := secretstore.New(root)
	checkpoints, _ := New(Options{StateRoot: root, SecretStore: vault})
	if err := checkpoints.VerifyOrBootstrap(ctx, audit, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(checkpoints.checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	document["event_sha256"] = strings.Repeat("0", 64)
	tampered, _ := json.Marshal(document)
	if err := os.WriteFile(checkpoints.checkpointPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkpoints.VerifyOrBootstrap(ctx, audit, time.Unix(11, 0)); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered signature error=%v", err)
	}
	if err := os.Remove(checkpoints.checkpointPath); err != nil {
		t.Fatal(err)
	}
	if err := checkpoints.VerifyOrBootstrap(ctx, audit, time.Unix(12, 0)); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing checkpoint error=%v", err)
	}
}

func TestCheckpointSigningMaterialStaysOutsideStateRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	vault, _ := secretstore.New(root)
	store, err := New(Options{StateRoot: root, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.keyPath, store.checkpointPath} {
		relative, err := filepath.Rel(root, path)
		if err != nil || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..") {
			t.Fatalf("external checkpoint material path=%s relative=%s err=%v", path, relative, err)
		}
	}
}

func TestReadOnlyOpenDoesNotBootstrapMissingTrustMaterial(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := New(Options{StateRoot: root, ReadOnly: true}); err == nil {
		t.Fatal("read-only open bootstrapped missing trust material")
	}
	_, keyPath, checkpointPath, err := PathsForStateRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{keyPath, checkpointPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("read-only open created %s: %v", path, err)
		}
	}
}

func TestIndependentWritersAdoptOnlyForwardSignedCheckpoints(t *testing.T) {
	ctx := context.Background()
	db := openCheckpointDB(t)
	audit := auditlog.New(db)
	root := filepath.Join(t.TempDir(), "state")
	vault, err := secretstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := New(Options{StateRoot: root, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.VerifyOrBootstrap(ctx, audit, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	second, err := New(Options{StateRoot: root, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.VerifyOrBootstrap(ctx, audit, time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	firstID, _ := audit.Append(ctx, auditlog.Event{OccurredAt: "1", Action: "first-writer"})
	if err := first.Write(ctx, audit, time.Unix(12, 0)); err != nil {
		t.Fatal(err)
	}
	secondID, _ := audit.Append(ctx, auditlog.Event{OccurredAt: "2", Action: "second-writer"})
	if err := second.Write(ctx, audit, time.Unix(13, 0)); err != nil {
		t.Fatalf("second writer did not adopt a forward signed checkpoint: %v", err)
	}
	if secondID <= firstID || second.CheckpointEventID() != secondID {
		t.Fatalf("first=%d second=%d checkpoint=%d", firstID, secondID, second.CheckpointEventID())
	}
}

func TestRestoredStateReanchorRequiresBothSignedCheckpointsAndRecordsContinuity(t *testing.T) {
	ctx := context.Background()
	currentDB := openCheckpointDB(t)
	currentAudit := auditlog.New(currentDB)
	if _, err := currentAudit.Append(ctx, auditlog.Event{OccurredAt: "1", Action: "first"}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "state")
	vault, err := secretstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := New(Options{StateRoot: root, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoints.VerifyOrBootstrap(ctx, currentAudit, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	backupDocument, err := checkpoints.TrustedDocument()
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if _, err := currentDB.Exec(`VACUUM INTO ?`, restoredPath); err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"second", "third"} {
		if _, err := currentAudit.Append(ctx, auditlog.Event{OccurredAt: strconv.Itoa(index + 2), Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkpoints.Write(ctx, currentAudit, time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	previousDocument, err := checkpoints.TrustedDocument()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoints.CheckpointEventID() != 3 {
		t.Fatalf("current checkpoint event = %d", checkpoints.CheckpointEventID())
	}
	restoredDB, err := sql.Open("sqlite", restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	restoredAudit := auditlog.New(restoredDB)
	if _, err := checkpoints.VerifyDetached(ctx, restoredAudit, backupDocument); err != nil {
		t.Fatal(err)
	}
	event := auditlog.Event{
		OccurredAt: "12", Action: "state_backup.restore", Target: "backup-id",
		SourceAddress: "local-cli", ActorRole: "local-administrator", AuthenticationAssurance: "local-os-access",
	}
	if err := checkpoints.ReanchorRestoredState(ctx, restoredAudit, previousDocument, backupDocument, event, time.Unix(12, 0)); err != nil {
		t.Fatal(err)
	}
	if checkpoints.CheckpointEventID() != 2 {
		t.Fatalf("restored checkpoint event = %d, want controlled backward transition to 2", checkpoints.CheckpointEventID())
	}
	var action, target, result, revision, digest string
	if err := restoredDB.QueryRow(`SELECT action, target, result, resource_revision, resource_digest_sha256 FROM audit_events WHERE id = 2`).Scan(&action, &target, &result, &revision, &digest); err != nil {
		t.Fatal(err)
	}
	if action != "state_backup.restore" || target != "backup-id" || revision != "3" || len(digest) != 64 || !strings.Contains(result, "backup_checkpoint_event_id=1") {
		t.Fatalf("continuity event = action=%q target=%q result=%q revision=%q digest=%q", action, target, result, revision, digest)
	}
	verificationStore, err := New(Options{StateRoot: root, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	if err := verificationStore.VerifyOrBootstrap(ctx, restoredAudit, time.Time{}); err != nil {
		t.Fatalf("fresh verifier rejected reanchored restored chain: %v", err)
	}
}

func openCheckpointDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL, actor_username TEXT NOT NULL, actor_role TEXT NOT NULL, request_id TEXT NOT NULL, authentication_assurance TEXT NOT NULL, resource_revision TEXT NOT NULL, resource_digest_sha256 TEXT NOT NULL, previous_hash TEXT NOT NULL, event_hash TEXT NOT NULL)`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY, anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state VALUES (1, '', '')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
