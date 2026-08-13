package update

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRestoreDatabaseRejectsCorruptSnapshotWithoutTouchingActiveDatabase(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "app.db")
	createFaultTestDatabase(t, database, "active")
	snapshot := filepath.Join(root, "snapshot.db")
	if err := os.WriteFile(snapshot, []byte("truncated-not-sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreDatabase(snapshot, database); err == nil {
		t.Fatal("corrupt snapshot was restored")
	}
	if value := readFaultTestDatabase(t, database); value != "active" {
		t.Fatalf("active database changed to %q", value)
	}
	if _, err := os.Stat(database + ".update-replaced"); !os.IsNotExist(err) {
		t.Fatalf("replacement artifact remains: %v", err)
	}
}

func TestRestoreDatabaseStagesAndVerifiesBeforeAtomicSwap(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "app.db")
	snapshot := filepath.Join(root, "snapshot.db")
	createFaultTestDatabase(t, database, "active")
	createFaultTestDatabase(t, snapshot, "snapshot")
	if err := os.WriteFile(database+".update-replaced", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreDatabase(snapshot, database); err != nil {
		t.Fatal(err)
	}
	if value := readFaultTestDatabase(t, database); value != "snapshot" {
		t.Fatalf("restored database value=%q", value)
	}
	for _, path := range []string{database + ".update-restore", database + ".update-replaced"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("restore artifact %s remains: %v", path, err)
		}
	}
}

func TestRollbackSnapshotFailureStopsBeforePlatformMutation(t *testing.T) {
	stateRoot := t.TempDir()
	id, _ := NewOperationID()
	nonce, _ := NewOperationID()
	root, _ := OperationDirectory(stateRoot, id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := validManifest()
	operation := Operation{
		Schema: OperationSchema, ID: id, Nonce: nonce, Phase: PhaseValidating,
		PreviousVersion: "1.0.0", TargetVersion: manifest.Version,
		PreviousCommit: strings.Repeat("a", 40), TargetCommit: manifest.Commit,
		InstallRoot: filepath.Join(t.TempDir(), "install"), StateRoot: stateRoot,
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), DatabasePath: filepath.Join(stateRoot, "app.db"),
		ArchivePath: filepath.Join(root, "archive.zip"), ExtractedPath: filepath.Join(root, "extracted"),
		SnapshotPath: filepath.Join(root, "database-before-update.db"), Manifest: manifest,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	createFaultTestDatabase(t, operation.DatabasePath, "active")
	if err := os.WriteFile(operation.SnapshotPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveOperation(operation); err != nil {
		t.Fatal(err)
	}
	if err := rollbackOperation(context.Background(), &operation, context.DeadlineExceeded); err == nil || !strings.Contains(err.Error(), "snapshot failed verification") {
		t.Fatalf("rollback error=%v", err)
	}
	reloaded, err := LoadOperation(stateRoot, id)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Phase != PhaseNeedsRecovery || readFaultTestDatabase(t, operation.DatabasePath) != "active" {
		t.Fatalf("operation=%#v", reloaded)
	}
}

func createFaultTestDatabase(t *testing.T, path, value string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE marker (value TEXT NOT NULL); INSERT INTO marker VALUES (?)`, value); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func readFaultTestDatabase(t *testing.T, path string) string {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var value string
	if err := database.QueryRow(`SELECT value FROM marker`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
