package runmanager

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"scriptboard/internal/managedfiles"
)

func TestReadSourceVerifiesSnapshotAndReportsExpiry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		script_kind TEXT NOT NULL,
		source_filename TEXT NOT NULL,
		source_expired INTEGER NOT NULL,
		script_sha256 TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	source := []byte("echo immutable\n")
	digest := sha256.Sum256(source)
	if _, err := db.Exec(`INSERT INTO runs VALUES ('run-1', 'one_time', 'source.sh', 0, ?)`, fmt.Sprintf("%x", digest[:])); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(root, "state")
	runRoot := filepath.Join(stateRoot, "runs", "run-1")
	if err := os.MkdirAll(runRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runRoot, "source.sh"), source, 0o400); err != nil {
		t.Fatal(err)
	}
	manager := New(db, managedfiles.Open(filepath.Join(root, "managed")), stateRoot, 0, nil)

	got, err := manager.ReadSource("run-1")
	if err != nil || string(got) != string(source) {
		t.Fatalf("ReadSource()=%q, %v", got, err)
	}
	if _, err := db.Exec("UPDATE runs SET source_expired = 1 WHERE id = 'run-1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReadSource("run-1"); !errors.Is(err, ErrSourceExpired) {
		t.Fatalf("ReadSource() after expiry error=%v", err)
	}
}
