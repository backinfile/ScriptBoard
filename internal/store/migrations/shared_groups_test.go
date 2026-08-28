package migrations

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchema61MigratesScheduleGroupsWhoseNamesMatchLegacyPlaceholders(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schedule_groups (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		sort_order INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	INSERT INTO schedule_groups(id,name,sort_order,created_at,updated_at) VALUES
		('alpha','__legacy__beta',1,1,1),
		('beta','__legacy__alpha',2,2,2)`); err != nil {
		t.Fatal(err)
	}

	err = Apply(db, 60, Options{
		CurrentVersion: 61,
		RandomToken:    func(int) (string, error) { return "token", nil },
		HashToken:      func(value string) string { return value },
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("migrate shared groups with legal legacy names: %v", err)
	}
}
