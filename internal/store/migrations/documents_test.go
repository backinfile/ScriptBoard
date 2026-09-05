package migrations

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchema65AddsDocumentsTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Apply(db, 64, Options{CurrentVersion: 65, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(value string) string { return value }, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO documents(path, path_key, group_id, sort_order, created_at)
		VALUES('/srv/scripts/deploy.md', '/srv/scripts/deploy.md', NULL, 1, 1)`); err != nil {
		t.Fatalf("insert document: %v", err)
	}
	var path string
	if err := db.QueryRow(`SELECT path FROM documents WHERE path_key='/srv/scripts/deploy.md'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != "/srv/scripts/deploy.md" {
		t.Fatalf("stored document path=%q", path)
	}
}
