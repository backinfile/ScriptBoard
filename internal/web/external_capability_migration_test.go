package web

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSchema36ExternalKeysSplitIntoSingleEntryCapabilities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema36.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE external_trigger_keys (
			id TEXT PRIMARY KEY, label TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, token_hint TEXT NOT NULL,
			enabled INTEGER NOT NULL, expires_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, last_used_at INTEGER
		)`,
		`CREATE TABLE external_trigger_entries (
			id TEXT PRIMARY KEY, key_id TEXT NOT NULL REFERENCES external_trigger_keys(id) ON DELETE CASCADE,
			name TEXT NOT NULL, label TEXT NOT NULL, action_type TEXT NOT NULL, target TEXT NOT NULL DEFAULT '',
			config_json TEXT NOT NULL DEFAULT '{}', enabled INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			UNIQUE (key_id, name)
		)`,
		`INSERT INTO external_trigger_keys (id, label, token_hash, token_hint, enabled, created_at, updated_at)
			VALUES ('shared-key', 'Shared key', 'original-hash', 'original-hint', 1, 1, 1)`,
		`INSERT INTO external_trigger_entries (id, key_id, name, label, action_type, config_json, enabled, created_at, updated_at) VALUES
			('entry-a', 'shared-key', 'a', 'First', 'website_monitor', '{}', 1, 1, 1),
			('entry-b', 'shared-key', 'b', 'Second', 'website_monitor', '{}', 1, 2, 2),
			('entry-c', 'shared-key', 'c', 'Third', 'website_monitor', '{}', 1, 3, 3)`,
		`PRAGMA user_version=36`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var version, keyCount, distinctKeyCount, disabledExtras int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM external_trigger_keys").Scan(&keyCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(DISTINCT key_id) FROM external_trigger_entries").Scan(&distinctKeyCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM external_trigger_entries AS entry
		JOIN external_trigger_keys AS key ON key.id = entry.key_id
		WHERE entry.id IN ('entry-b', 'entry-c') AND entry.enabled = 0 AND key.enabled = 0`).Scan(&disabledExtras); err != nil {
		t.Fatal(err)
	}
	var originalKeyID string
	if err := database.QueryRow("SELECT key_id FROM external_trigger_entries WHERE id = 'entry-a'").Scan(&originalKeyID); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || keyCount != 3 || distinctKeyCount != 3 || disabledExtras != 2 || originalKeyID != "shared-key" {
		t.Fatalf("migration schema=%d keys=%d distinct=%d disabled=%d original=%q", version, keyCount, distinctKeyCount, disabledExtras, originalKeyID)
	}
	if _, err := database.Exec(`INSERT INTO external_trigger_entries
		(id, key_id, name, label, action_type, config_json, enabled, created_at, updated_at)
		VALUES ('entry-d', 'shared-key', 'd', 'Fourth', 'website_monitor', '{}', 1, 4, 4)`); err == nil {
		t.Fatal("migrated key accepted a second Entry")
	}
}
