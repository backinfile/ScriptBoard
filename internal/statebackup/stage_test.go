package statebackup_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scriptboard/internal/statebackup"
)

func TestStageRestoreAuthenticatesExtractsRevokesSessionsAndPersistsNoPassphrase(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceDatabase := createStateDatabase(t, sourceRoot, "staged")
	if _, err := sourceDatabase.Exec(`CREATE TABLE sessions (token_hash TEXT PRIMARY KEY); INSERT INTO sessions(token_hash) VALUES ('must-be-revoked')`); err != nil {
		t.Fatal(err)
	}
	manager, err := statebackup.New(statebackup.Options{StateRoot: sourceRoot, Database: sourceDatabase})
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("stage restore passphrase must remain transient")
	archivePath := filepath.Join(t.TempDir(), "staged.sbsb")
	artifact, err := manager.Create(context.Background(), statebackup.CreateRequest{Destination: archivePath, Passphrase: passphrase, AuditCheckpoint: json.RawMessage(`{"signed":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	targetRoot := t.TempDir()
	targetDatabase := createStateDatabase(t, targetRoot, "current")
	defer targetDatabase.Close()
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	validated := false
	stage, err := statebackup.StageRestore(context.Background(), statebackup.StageRequest{
		StateRoot: targetRoot, ArchivePath: archivePath, Passphrase: passphrase, ConfirmBackupID: artifact.Manifest.ID,
		MinimumSchemaVersion: 20, MaximumSchemaVersion: 43, Now: now,
		ValidateStaged: func(_ context.Context, databasePath string, manifest statebackup.Manifest) error {
			validated = manifest.ID == artifact.Manifest.ID && readStateValue(t, databasePath) == "staged"
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !validated || stage.Manifest.ID != artifact.Manifest.ID || !stage.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("stage = %#v validated=%v", stage, validated)
	}
	if len(stage.PayloadFiles) == 0 {
		t.Fatal("stage did not bind the post-revocation payload")
	}
	stages, err := statebackup.ListStages(targetRoot, now)
	if err != nil || len(stages) != 1 || stages[0].ID != stage.ID {
		t.Fatalf("stages=%#v err=%v", stages, err)
	}
	stageRoot := filepath.Join(filepath.Dir(targetRoot), "."+filepath.Base(targetRoot)+".restore-stages", stage.ID)
	stagedDatabase, err := sql.Open("sqlite", filepath.Join(stageRoot, "payload", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var sessions int
	if err := stagedDatabase.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("staged sessions=%d err=%v", sessions, err)
	}
	record, err := os.ReadFile(filepath.Join(stageRoot, "STAGE.json"))
	if err != nil {
		t.Fatal(err)
	}
	if stringContainsBytes(record, passphrase) {
		t.Fatal("stage record persisted restore passphrase")
	}
	if value := readStateValue(t, filepath.Join(targetRoot, "app.db")); value != "current" {
		t.Fatalf("staging changed active state to %q", value)
	}
	if verified, err := statebackup.VerifyStage(targetRoot, stage.ID, now); err != nil || verified.ID != stage.ID {
		t.Fatalf("verify stage=%#v err=%v", verified, err)
	}
	if err := stagedDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	if err := statebackup.DiscardStage(targetRoot, stage.ID); err != nil {
		t.Fatal(err)
	}
	if stages, err := statebackup.ListStages(targetRoot, now); err != nil || len(stages) != 0 {
		t.Fatalf("stages after discard=%#v err=%v", stages, err)
	}
}

func TestStageRestoreRejectsWrongBackupIDWithoutLeavingStage(t *testing.T) {
	sourceRoot := t.TempDir()
	database := createStateDatabase(t, sourceRoot, "source")
	manager, err := statebackup.New(statebackup.Options{StateRoot: sourceRoot, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("stage restore wrong confirmation fixture")
	archivePath := filepath.Join(t.TempDir(), "source.sbsb")
	if _, err := manager.Create(context.Background(), statebackup.CreateRequest{Destination: archivePath, Passphrase: passphrase, AuditCheckpoint: json.RawMessage(`{"signed":true}`)}); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	targetRoot := t.TempDir()
	if _, err := statebackup.StageRestore(context.Background(), statebackup.StageRequest{StateRoot: targetRoot, ArchivePath: archivePath, Passphrase: passphrase, ConfirmBackupID: "wrong", MinimumSchemaVersion: 20, MaximumSchemaVersion: 43}); err == nil {
		t.Fatal("staging accepted the wrong backup ID")
	}
	if stages, err := statebackup.ListStages(targetRoot, time.Now()); err != nil || len(stages) != 0 {
		t.Fatalf("wrong confirmation left stages=%#v err=%v", stages, err)
	}
}

func stringContainsBytes(body, value []byte) bool {
	if len(value) == 0 || len(body) < len(value) {
		return false
	}
	for index := 0; index+len(value) <= len(body); index++ {
		match := true
		for offset := range value {
			if body[index+offset] != value[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
