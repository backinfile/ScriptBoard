package migrations

import (
	"database/sql"
	"fmt"

	storesqlite "scriptboard/internal/store/sqlite"
)

// migrateExternalInterfaceGroups reconciles both historical schema-45 lines:
// one introduced grouped External Interfaces, while the other introduced the
// Registry operation log. Column inspection keeps either predecessor valid.
func migrateExternalInterfaceGroups(migration *sql.Tx, schemaVersion int) error {
	if schemaVersion < 20 || schemaVersion > 45 {
		return nil
	}
	keysGrouped, err := storesqlite.ColumnExists(migration, "external_trigger_keys", "group_id")
	if err != nil {
		return fmt.Errorf("inspect External Interface group migration: %w", err)
	}
	entriesGrouped, err := storesqlite.ColumnExists(migration, "external_trigger_entries", "group_id")
	if err != nil {
		return fmt.Errorf("inspect External Interface entry group migration: %w", err)
	}
	if keysGrouped && entriesGrouped {
		return nil
	}
	if !keysGrouped {
		if _, err := migration.Exec(`ALTER TABLE external_trigger_keys ADD COLUMN group_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add External Interface key groups: %w", err)
		}
	}
	for _, statement := range []string{
		`INSERT OR IGNORE INTO external_trigger_groups (id, label, call_name, enabled, created_at, updated_at)
		 SELECT 'group-' || id, label, label, 1, created_at, updated_at FROM external_trigger_keys`,
		`UPDATE external_trigger_keys SET group_id = 'group-' || id WHERE group_id = ''`,
	} {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("migrate External Interface key groups: %w", err)
		}
	}
	if entriesGrouped {
		return nil
	}
	for _, statement := range []string{
		`CREATE TABLE external_trigger_entries_schema46 (
			id TEXT PRIMARY KEY,
			group_id TEXT NOT NULL DEFAULT '',
			key_id TEXT REFERENCES external_trigger_keys(id) ON DELETE SET NULL,
			name TEXT NOT NULL,
			label TEXT NOT NULL,
			action_type TEXT NOT NULL CHECK (action_type IN ('log', 'upload', 'quick_run', 'variable', 'website_monitor')),
			target TEXT NOT NULL DEFAULT '',
			config_json TEXT NOT NULL DEFAULT '{}',
			require_signature INTEGER NOT NULL DEFAULT 0 CHECK (require_signature IN (0, 1)),
			enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (group_id, name)
		)`,
		`INSERT INTO external_trigger_entries_schema46
			(id, group_id, key_id, name, label, action_type, target, config_json, require_signature, enabled, created_at, updated_at)
		 SELECT entry.id, key.group_id, entry.key_id, entry.name, entry.label, entry.action_type, entry.target, entry.config_json,
			entry.require_signature, entry.enabled, entry.created_at, entry.updated_at
		 FROM external_trigger_entries AS entry JOIN external_trigger_keys AS key ON key.id = entry.key_id`,
		`DROP TABLE external_trigger_entries`,
		`ALTER TABLE external_trigger_entries_schema46 RENAME TO external_trigger_entries`,
	} {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("migrate External Interface entry groups: %w", err)
		}
	}
	return nil
}

func migrateExternalInterfaceGroupCallNames(migration *sql.Tx, schemaVersion int) error {
	if schemaVersion < 20 || schemaVersion > 46 {
		return nil
	}
	exists, err := storesqlite.ColumnExists(migration, "external_trigger_groups", "call_name")
	if err != nil {
		return fmt.Errorf("inspect External Interface group call-name migration: %w", err)
	}
	if !exists {
		if _, err := migration.Exec(`ALTER TABLE external_trigger_groups ADD COLUMN call_name TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add External Interface group call names: %w", err)
		}
	}
	if _, err := migration.Exec(`UPDATE external_trigger_groups SET call_name = label WHERE call_name = ''`); err != nil {
		return fmt.Errorf("backfill External Interface group call names: %w", err)
	}
	if _, err := migration.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS external_trigger_groups_call_name_idx ON external_trigger_groups(call_name COLLATE NOCASE)`); err != nil {
		return fmt.Errorf("index External Interface group call names: %w", err)
	}
	return nil
}
