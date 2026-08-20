package migrations_test

import (
	"database/sql"
	"testing"
	"time"

	"scriptboard/internal/store/migrations"

	_ "modernc.org/sqlite"
)

func TestApplyMigratesExistingVariableMetadata(t *testing.T) {
	t.Parallel()

	database, err := sql.Open("sqlite", "file:variable-migration?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE variables (
			name TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			is_password INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO variables(name, value, is_password, created_at, updated_at)
		VALUES ('API_TOKEN', 'kept-value', 1, 1, 1);
		PRAGMA user_version=48;
	`); err != nil {
		t.Fatal(err)
	}

	err = migrations.Apply(database, 48, migrations.Options{
		CurrentVersion: 53,
		RandomToken:    func(int) (string, error) { return "token", nil },
		HashToken:      func(value string) string { return value },
		Now:            func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var value, valueType, note string
	var revision int64
	var isPassword bool
	if err := database.QueryRow(`SELECT value, value_type, note, is_password, revision FROM variables WHERE name = 'API_TOKEN'`).Scan(&value, &valueType, &note, &isPassword, &revision); err != nil {
		t.Fatal(err)
	}
	if value != "kept-value" || valueType != "text" || note != "" || !isPassword || revision != 1 {
		t.Fatalf("value=%q type=%q note=%q password=%v revision=%d", value, valueType, note, isPassword, revision)
	}
}
