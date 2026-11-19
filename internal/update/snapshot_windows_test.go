package update

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsUpdateRejectsOperationsOutsideInstallRoot(t *testing.T) {
	stateRoot := t.TempDir()
	if err := requireProtectedOperationStorage(stateRoot); err == nil {
		t.Fatal("an executable outside the managed Install Root can apply or recover an update")
	}
}

func TestWindowsUpdateSnapshotIncludesWAL(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "app.db")
	snapshot := filepath.Join(t.TempDir(), "backup.db")
	db, err := sql.Open("sqlite", active)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE marker(value TEXT); INSERT INTO marker VALUES ('committed in WAL')"); err != nil {
		t.Fatal(err)
	}
	if wal, err := os.Stat(active + "-wal"); err != nil || wal.Size() == 0 {
		t.Fatalf("fixture has no WAL data: %v", err)
	}
	if err := snapshotDatabase(context.Background(), active, snapshot); err != nil {
		t.Fatal(err)
	}
	if got := readFaultTestDatabase(t, snapshot); got != "committed in WAL" {
		t.Fatalf("snapshot row = %q", got)
	}
}

func TestWindowsUpdateSnapshotRejectsHardLinkedDatabase(t *testing.T) {
	original := filepath.Join(t.TempDir(), "original.db")
	createFaultTestDatabase(t, original, "original")
	active := filepath.Join(t.TempDir(), "app.db")
	if err := os.Link(original, active); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := snapshotDatabase(context.Background(), active, filepath.Join(t.TempDir(), "backup.db")); err == nil {
		t.Fatal("privileged update snapshot accepted a hard-linked input")
	}
}
