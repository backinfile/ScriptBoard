package migrations

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchema59KeepsExistingQuickAccessPinsAsDirectories(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE file_quick_access_pins (
		path TEXT NOT NULL, path_key TEXT PRIMARY KEY, label TEXT NOT NULL,
		sort_order INTEGER NOT NULL, created_at INTEGER NOT NULL
	); INSERT INTO file_quick_access_pins(path,path_key,label,sort_order,created_at)
		VALUES('/srv/scripts','/srv/scripts','Scripts',1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 58, Options{CurrentVersion: 59, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(value string) string { return value }, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := db.QueryRow(`SELECT target_kind FROM file_quick_access_pins WHERE path_key='/srv/scripts'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "directory" {
		t.Fatalf("migrated Quick access target kind=%q", kind)
	}
}
