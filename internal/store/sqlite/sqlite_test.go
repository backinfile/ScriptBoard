package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenSQLiteRejectsIncompatibleDatabaseBeforeWritableOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, _, err := OpenSQLite(path, 45, func(int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if err := Checkpoint(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = OpenSQLite(path, 45, func(version int) bool { return version <= 45 })
	if err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("expected incompatible schema error, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("incompatible database changed during preflight")
	}
}

func TestColumnExistsValidatesIdentifiers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, _, err := OpenSQLite(path, 45, func(int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	transaction, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec("CREATE TABLE example (known TEXT)"); err != nil {
		t.Fatal(err)
	}
	if exists, err := ColumnExists(transaction, "example", "known"); err != nil || !exists {
		t.Fatalf("expected known column: exists=%v error=%v", exists, err)
	}
	if _, err := ColumnExists(transaction, "example); DROP TABLE example; --", "known"); err == nil {
		t.Fatal("expected invalid identifier rejection")
	}
}
