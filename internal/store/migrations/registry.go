package migrations

import (
	"database/sql"
	"fmt"

	storesqlite "scriptboard/internal/store/sqlite"
)

func migrateRegistryOperations(migration *sql.Tx, schemaVersion int) error {
	if schemaVersion < 20 || schemaVersion > 45 {
		return nil
	}
	exists, err := storesqlite.ColumnExists(migration, "custom_dashboard_registry_operations", "phase")
	if err != nil {
		return fmt.Errorf("inspect Registry operation phase migration: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := migration.Exec(`ALTER TABLE custom_dashboard_registry_operations ADD COLUMN phase TEXT NOT NULL DEFAULT 'prepared' CHECK(phase IN ('prepared','committed'))`); err != nil {
		return fmt.Errorf("add Registry operation phase: %w", err)
	}
	return nil
}
