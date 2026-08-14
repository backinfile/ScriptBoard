package statebackup_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/statebackup"

	_ "modernc.org/sqlite"
)

func TestCreateProducesInspectableEncryptedPrivateStateBackup(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`
		PRAGMA user_version = 43;
		CREATE TABLE settings (name TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO settings(name, value) VALUES ('locale', 'zh-CN');
	`); err != nil {
		t.Fatal(err)
	}
	secret := []byte("broker-ciphertext-fixture-that-must-not-appear-in-the-package")
	if err := os.MkdirAll(filepath.Join(stateRoot, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, "secrets", "provider.enc"), secret, 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := statebackup.New(statebackup.Options{
		StateRoot: stateRoot,
		Database:  database,
		Now:       func() time.Time { return time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "scriptboard-state.sbsb")
	passphrase := []byte("correct horse battery staple for state backup")
	artifact, err := manager.Create(context.Background(), statebackup.CreateRequest{
		Destination:     destination,
		Passphrase:      passphrase,
		AuditCheckpoint: json.RawMessage(`{"test":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactInfo, artifactErr := os.Stat(artifact.Path)
	destinationInfo, destinationErr := os.Stat(destination)
	if artifactErr != nil || destinationErr != nil || !os.SameFile(artifactInfo, destinationInfo) || artifact.Manifest.SchemaVersion != 43 || artifact.Manifest.CreatedAt != "2026-08-12T08:00:00Z" {
		t.Fatalf("artifact = %#v", artifact)
	}
	packageBytes, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(packageBytes, secret) || bytes.Contains(packageBytes, []byte("zh-CN")) {
		t.Fatal("encrypted backup exposes private state plaintext")
	}

	manifest, err := statebackup.Inspect(context.Background(), destination, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != artifact.Manifest.ID || manifest.SchemaVersion != 43 {
		t.Fatalf("inspected manifest = %#v, artifact = %#v", manifest, artifact.Manifest)
	}
	if !manifestHasFile(manifest, "app.db") || !manifestHasFile(manifest, "secrets/provider.enc") {
		t.Fatalf("manifest files = %#v", manifest.Files)
	}
}

func TestRestoreReplacesPrivateStatePreservesCurrentStateAndRevokesSessions(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceDatabase := createStateDatabase(t, sourceRoot, "restored")
	if _, err := sourceDatabase.Exec(`CREATE TABLE sessions (token_hash TEXT PRIMARY KEY); INSERT INTO sessions(token_hash) VALUES ('restored-session')`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourceRoot, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "secrets", "provider.enc"), []byte("restored-ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := statebackup.New(statebackup.Options{StateRoot: sourceRoot, Database: sourceDatabase})
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("correct horse battery staple for restore")
	archivePath := filepath.Join(t.TempDir(), "restore-source.sbsb")
	artifact, err := manager.Create(context.Background(), statebackup.CreateRequest{Destination: archivePath, Passphrase: passphrase, AuditCheckpoint: json.RawMessage(`{"test":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	targetRoot := t.TempDir()
	targetDatabase := createStateDatabase(t, targetRoot, "current")
	if err := targetDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	staleWAL := []byte("stale-current-wal-must-not-be-applied")
	if err := os.WriteFile(filepath.Join(targetRoot, "app.db-wal"), staleWAL, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(targetRoot, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetRoot, "secrets", "provider.enc"), []byte("current-ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := statebackup.Restore(context.Background(), statebackup.RestoreRequest{
		StateRoot:            targetRoot,
		ArchivePath:          archivePath,
		Passphrase:           passphrase,
		ConfirmBackupID:      artifact.Manifest.ID,
		MinimumSchemaVersion: 20,
		MaximumSchemaVersion: 43,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.ID != artifact.Manifest.ID || result.PreservedStatePath == "" {
		t.Fatalf("restore result = %#v", result)
	}
	if value := readStateValue(t, filepath.Join(targetRoot, "app.db")); value != "restored" {
		t.Fatalf("restored database value = %q", value)
	}
	restoredDatabase, err := sql.Open("sqlite", filepath.Join(targetRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDatabase.Close()
	var sessions int
	if err := restoredDatabase.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("restored sessions = %d, err = %v", sessions, err)
	}
	secret, err := os.ReadFile(filepath.Join(targetRoot, "secrets", "provider.enc"))
	if err != nil || string(secret) != "restored-ciphertext" {
		t.Fatalf("restored secret = %q, err = %v", secret, err)
	}
	if value := readStateValue(t, filepath.Join(result.PreservedStatePath, "app.db")); value != "current" {
		t.Fatalf("preserved database value = %q", value)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "app.db-wal")); !os.IsNotExist(err) {
		t.Fatalf("stale current WAL remained beside restored database: %v", err)
	}
	if preservedWAL, err := os.ReadFile(filepath.Join(result.PreservedStatePath, "app.db-wal")); err != nil || !bytes.Equal(preservedWAL, staleWAL) {
		t.Fatalf("preserved WAL = %q, err = %v", preservedWAL, err)
	}
}

func TestRestoreRejectsIncompatibleSchemaWithoutChangingCurrentState(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceDatabase := createStateDatabase(t, sourceRoot, "too-old")
	if _, err := sourceDatabase.Exec(`PRAGMA user_version = 19; CREATE TABLE sessions (token_hash TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	manager, err := statebackup.New(statebackup.Options{StateRoot: sourceRoot, Database: sourceDatabase})
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "old-schema.sbsb")
	passphrase := []byte("correct horse battery staple for schema")
	artifact, err := manager.Create(context.Background(), statebackup.CreateRequest{Destination: archivePath, Passphrase: passphrase, AuditCheckpoint: json.RawMessage(`{"test":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	targetRoot := t.TempDir()
	targetDatabase := createStateDatabase(t, targetRoot, "current")
	if err := targetDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = statebackup.Restore(context.Background(), statebackup.RestoreRequest{
		StateRoot:            targetRoot,
		ArchivePath:          archivePath,
		Passphrase:           passphrase,
		ConfirmBackupID:      artifact.Manifest.ID,
		MinimumSchemaVersion: 20,
		MaximumSchemaVersion: 43,
	})
	if err == nil {
		t.Fatal("restore accepted an incompatible old schema")
	}
	if value := readStateValue(t, filepath.Join(targetRoot, "app.db")); value != "current" {
		t.Fatalf("incompatible restore changed current state to %q", value)
	}
}

func TestRestoreRollsBackPrivateStateWhenFinalizationFails(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceDatabase := createStateDatabase(t, sourceRoot, "restored")
	if _, err := sourceDatabase.Exec(`CREATE TABLE sessions (token_hash TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	manager, err := statebackup.New(statebackup.Options{StateRoot: sourceRoot, Database: sourceDatabase})
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "restore-source.sbsb")
	passphrase := []byte("correct horse battery staple for rollback")
	artifact, err := manager.Create(context.Background(), statebackup.CreateRequest{Destination: archivePath, Passphrase: passphrase, AuditCheckpoint: json.RawMessage(`{"test":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	targetRoot := t.TempDir()
	targetDatabase := createStateDatabase(t, targetRoot, "current")
	if err := targetDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = statebackup.Restore(context.Background(), statebackup.RestoreRequest{
		StateRoot: targetRoot, ArchivePath: archivePath, Passphrase: passphrase, ConfirmBackupID: artifact.Manifest.ID,
		MinimumSchemaVersion: 20, MaximumSchemaVersion: 43,
		Finalize: func(context.Context, statebackup.RestoreResult) error { return errors.New("checkpoint write failed") },
	})
	if err == nil || !strings.Contains(err.Error(), "checkpoint write failed") {
		t.Fatalf("restore finalization error = %v", err)
	}
	if value := readStateValue(t, filepath.Join(targetRoot, "app.db")); value != "current" {
		t.Fatalf("failed finalization left restored state active: %q", value)
	}
	preserved := filepath.Join(filepath.Dir(targetRoot), filepath.Base(targetRoot)+".before-restore-"+artifact.Manifest.ID)
	if _, err := os.Stat(preserved); !os.IsNotExist(err) {
		t.Fatalf("failed finalization left preserved directory %q: %v", preserved, err)
	}
}

func TestInspectRejectsTamperedAndTruncatedEncryptedBackups(t *testing.T) {
	stateRoot := t.TempDir()
	database := createStateDatabase(t, stateRoot, "source")
	t.Cleanup(func() { _ = database.Close() })
	manager, err := statebackup.New(statebackup.Options{StateRoot: stateRoot, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("correct horse battery staple for tamper tests")
	archivePath := filepath.Join(t.TempDir(), "source.sbsb")
	if _, err := manager.Create(context.Background(), statebackup.CreateRequest{Destination: archivePath, Passphrase: passphrase, AuditCheckpoint: json.RawMessage(`{"test":true}`)}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string][]byte{
		"tampered":  append(append([]byte(nil), body[:len(body)-1]...), body[len(body)-1]^0xff),
		"truncated": append([]byte(nil), body[:len(body)-7]...),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name+".sbsb")
			if err := os.WriteFile(path, candidate, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := statebackup.Inspect(context.Background(), path, passphrase); err == nil {
				t.Fatal("invalid encrypted backup was accepted")
			}
		})
	}
}

func createStateDatabase(t *testing.T, root, value string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 43; CREATE TABLE settings (name TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO settings(name, value) VALUES ('marker', ?)`, value); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	return database
}

func readStateValue(t *testing.T, databasePath string) string {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var value string
	if err := database.QueryRow(`SELECT value FROM settings WHERE name='marker'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func manifestHasFile(manifest statebackup.Manifest, name string) bool {
	for _, file := range manifest.Files {
		if file.Path == name {
			return true
		}
	}
	return false
}
