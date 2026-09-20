package migrations

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestAddRecordNotesAddsOptionalNoteToManagedRecords(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for _, table := range recordNoteTables {
		if _, err := database.Exec("CREATE TABLE " + table + " (id TEXT PRIMARY KEY)"); err != nil {
			t.Fatalf("create %s: %v", table, err)
		}
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := addRecordNotes(transaction); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, table := range recordNoteTables {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='note'", table).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s note columns = %d, want 1", table, count)
		}
	}
}

func TestAddRecordNotesPreservesExistingNoteColumn(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, table := range recordNoteTables {
		definition := "id TEXT PRIMARY KEY"
		if table == "quick_runs" {
			definition += ", note TEXT NOT NULL DEFAULT ''"
		}
		if _, err := database.Exec("CREATE TABLE " + table + " (" + definition + ")"); err != nil {
			t.Fatal(err)
		}
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := addRecordNotes(transaction); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}
