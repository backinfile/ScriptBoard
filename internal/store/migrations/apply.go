package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/customdashboard"
	"scriptboard/internal/customtab"
	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/fleetstatus"
	"scriptboard/internal/mcpaccess"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/redismanager"
	storesqlite "scriptboard/internal/store/sqlite"
	"scriptboard/internal/websitemonitor"
)

// Options supplies the application-specific capabilities needed by legacy
// data migrations while keeping SQLite transaction ownership in this package.
type Options struct {
	CurrentVersion int
	RandomToken    func(int) (string, error)
	HashToken      func(string) string
	Now            func() time.Time
}

// Apply installs the current schema and advances every supported predecessor
// in one transaction. A failed migration never exposes a partially upgraded
// database to the application.
func Apply(db *sql.DB, schemaVersion int, options Options) error {
	if options.CurrentVersion <= 0 || options.RandomToken == nil || options.HashToken == nil || options.Now == nil {
		return fmt.Errorf("complete migration options are required")
	}
	migration, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin SQLite migration: %w", err)
	}
	defer func() { _ = migration.Rollback() }()
	schemas := []struct {
		name       string
		statements []string
	}{
		{name: "SQLite", statements: baseSchemaStatements},
		{name: "Website Monitor SQLite", statements: websitemonitor.SchemaStatements},
		{name: "External Interface SQLite", statements: externaltrigger.SchemaStatements},
		{name: "Fleet status SQLite", statements: fleetstatus.SchemaStatements},
		{name: "MySQL management SQLite", statements: mysqlmanager.SchemaStatements},
		{name: "Redis management SQLite", statements: redismanager.SchemaStatements},
		{name: "custom dashboard SQLite", statements: customdashboard.SchemaStatements},
		{name: "custom tab SQLite", statements: customtab.SchemaStatements},
		{name: "Kubernetes monitoring SQLite", statements: clusterstatus.SchemaStatements},
		{name: "MCP OAuth SQLite", statements: mcpaccess.SchemaStatements},
	}
	for _, schema := range schemas {
		for _, statement := range schema.statements {
			if _, err := migration.Exec(statement); err != nil {
				return fmt.Errorf("initialize %s schema: %w", schema.name, err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 61 {
		exists, err := storesqlite.ColumnExists(migration, "quick_runs", "require_confirmation")
		if err != nil {
			return fmt.Errorf("inspect Quick Run confirmation migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE quick_runs ADD COLUMN require_confirmation INTEGER NOT NULL DEFAULT 0 CHECK (require_confirmation IN (0, 1))`); err != nil {
				return fmt.Errorf("add Quick Run confirmation requirement: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 57 {
		exists, err := storesqlite.ColumnExists(migration, "external_trigger_entries", "require_approval")
		if err != nil {
			return fmt.Errorf("inspect External Interface approval migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE external_trigger_entries ADD COLUMN require_approval INTEGER NOT NULL DEFAULT 0 CHECK (require_approval IN (0, 1))`); err != nil {
				return fmt.Errorf("add External Interface approval requirement: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 58 {
		exists, err := storesqlite.ColumnExists(migration, "file_quick_access_pins", "target_kind")
		if err != nil {
			return fmt.Errorf("inspect file Quick access target migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE file_quick_access_pins ADD COLUMN target_kind TEXT NOT NULL DEFAULT 'directory' CHECK (target_kind IN ('directory', 'file'))`); err != nil {
				return fmt.Errorf("add file Quick access target kind: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 60 {
		// Schema 61 makes Quick Run groups the shared organization baseline.
		// Existing Schedule groups are retired without name matching; every newly
		// supported collection starts in the derived Ungrouped section.
		for _, column := range []struct {
			table      string
			definition string
		}{
			{"file_quick_access_pins", "group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL"},
			{"website_monitors", "group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL"},
			{"variables", "group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL"},
		} {
			exists, err := storesqlite.ColumnExists(migration, column.table, "group_id")
			if err != nil {
				return fmt.Errorf("inspect shared group migration for %s: %w", column.table, err)
			}
			if !exists {
				if _, err := migration.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.definition); err != nil {
					return fmt.Errorf("add shared group to %s: %w", column.table, err)
				}
			}
		}
		for _, column := range []struct{ table, definition string }{
			{"variables", "sort_order INTEGER NOT NULL DEFAULT 0"},
			{"schedules", "sort_order INTEGER NOT NULL DEFAULT 0"},
		} {
			exists, err := storesqlite.ColumnExists(migration, column.table, "sort_order")
			if err != nil {
				return fmt.Errorf("inspect shared ordering migration for %s: %w", column.table, err)
			}
			if !exists {
				if _, err := migration.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.definition); err != nil {
					return fmt.Errorf("add shared ordering to %s: %w", column.table, err)
				}
			}
		}
		// Retire the old catalog before copying shared groups so user-controlled
		// Schedule group names cannot collide with migration placeholders.
		if _, err := migration.Exec(`
			UPDATE schedules SET group_id = NULL,
				group_name = CASE WHEN deleted = 0 THEN '' ELSE group_name END;
			DELETE FROM schedule_groups;
			INSERT INTO schedule_groups (id, name, sort_order, created_at, updated_at)
			SELECT id, name, sort_order, created_at, updated_at FROM quick_run_groups;
			UPDATE variables SET sort_order = (
				SELECT COUNT(*) FROM variables AS earlier WHERE earlier.name <= variables.name
			);
			UPDATE schedules SET sort_order = (
				SELECT COUNT(*) FROM schedules AS earlier
				WHERE earlier.deleted = schedules.deleted
				AND (earlier.created_at < schedules.created_at OR (earlier.created_at = schedules.created_at AND earlier.id <= schedules.id))
			)`); err != nil {
			return fmt.Errorf("initialize shared groups: %w", err)
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 56 {
		exists, err := storesqlite.ColumnExists(migration, "custom_dashboards", "show_as_tab")
		if err != nil {
			return fmt.Errorf("inspect custom dashboard tab visibility migration: %w", err)
		}
		if !exists {
			// Existing panels remain available in configuration and opt into the application tab bar explicitly.
			if _, err := migration.Exec(`ALTER TABLE custom_dashboards ADD COLUMN show_as_tab INTEGER NOT NULL DEFAULT 0 CHECK (show_as_tab IN (0,1))`); err != nil {
				return fmt.Errorf("add custom dashboard tab visibility: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 56 {
		exists, err := storesqlite.ColumnExists(migration, "custom_tabs", "visibility_roles")
		if err != nil {
			return fmt.Errorf("inspect custom tab role visibility migration: %w", err)
		}
		if !exists {
			// Feature-line schema 55 databases predate role visibility; current tables already include the column.
			if _, err := migration.Exec(`ALTER TABLE custom_tabs ADD COLUMN visibility_roles TEXT NOT NULL DEFAULT 'administrator,maintainer,operator,viewer'`); err != nil {
				return fmt.Errorf("add custom tab role visibility: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 48 {
		exists, err := storesqlite.ColumnExists(migration, "variables", "value_type")
		if err != nil {
			return fmt.Errorf("inspect Variable type migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE variables ADD COLUMN value_type TEXT NOT NULL DEFAULT 'text' CHECK (value_type IN ('text', 'bool', 'integer', 'float', 'version'))`); err != nil {
				return fmt.Errorf("add Variable value type: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 49 {
		exists, err := storesqlite.ColumnExists(migration, "variables", "revision")
		if err != nil {
			return fmt.Errorf("inspect Variable revision migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE variables ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`); err != nil {
				return fmt.Errorf("add Variable revision: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 50 {
		exists, err := storesqlite.ColumnExists(migration, "variables", "note")
		if err != nil {
			return fmt.Errorf("inspect Variable note migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE variables ADD COLUMN note TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("add Variable note: %w", err)
			}
		}
	}
	if err := migrateKubernetesConnections(migration); err != nil {
		return err
	}
	if schemaVersion == 28 {
		for _, statement := range []string{
			`ALTER TABLE file_quick_access_pins RENAME TO file_quick_access_pins_user_scoped`,
			`CREATE TABLE file_quick_access_pins (
				path TEXT NOT NULL,
				path_key TEXT PRIMARY KEY,
				label TEXT NOT NULL,
				target_kind TEXT NOT NULL DEFAULT 'directory' CHECK (target_kind IN ('directory', 'file')),
				group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL,
				sort_order INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			)`,
			`INSERT INTO file_quick_access_pins (path, path_key, label, target_kind, sort_order, created_at)
				SELECT path, path_key, label, target_kind, MIN(sort_order), MIN(created_at)
				FROM file_quick_access_pins_user_scoped GROUP BY path_key`,
			`DROP TABLE file_quick_access_pins_user_scoped`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				return fmt.Errorf("migrate file Quick Access pins to instance scope: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 30 {
		exists, err := storesqlite.ColumnExists(migration, "mysql_instances", "connection_state")
		if err != nil {
			return fmt.Errorf("inspect MySQL connection state migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE mysql_instances ADD COLUMN connection_state TEXT NOT NULL DEFAULT 'untried' CHECK (connection_state IN ('untried', 'connected', 'failed'))`); err != nil {
				return fmt.Errorf("migrate MySQL connection state: %w", err)
			}
		}
	}
	if err := migrateRegistryOperations(migration, schemaVersion); err != nil {
		return err
	}
	if schemaVersion >= 27 && schemaVersion <= 31 {
		for _, statement := range []string{
			`CREATE TABLE external_trigger_entries_schema32 (
				id TEXT PRIMARY KEY,
				key_id TEXT NOT NULL REFERENCES external_trigger_keys(id) ON DELETE CASCADE,
				name TEXT NOT NULL,
				label TEXT NOT NULL,
				action_type TEXT NOT NULL CHECK (action_type IN ('log', 'upload', 'quick_run', 'variable', 'website_monitor')),
				target TEXT NOT NULL DEFAULT '',
				config_json TEXT NOT NULL DEFAULT '{}',
				enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL,
				UNIQUE (key_id, name)
			)`,
			`INSERT INTO external_trigger_entries_schema32
				(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
				SELECT id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at
				FROM external_trigger_entries`,
			`DROP TABLE external_trigger_entries`,
			`ALTER TABLE external_trigger_entries_schema32 RENAME TO external_trigger_entries`,
			`CREATE INDEX external_trigger_entries_key_idx ON external_trigger_entries(key_id, created_at)`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				return fmt.Errorf("migrate External Interface website monitoring action: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 33 {
		for _, statement := range []string{
			`ALTER TABLE custom_dashboard_cards RENAME TO custom_dashboard_cards_schema33`,
			`CREATE TABLE custom_dashboard_cards (
				id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
				name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','percentage','quota','key_value','website')),
				source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
				value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
				config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
				sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
				last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
			)`,
			`INSERT INTO custom_dashboard_cards
				(id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,snapshot_json,last_error,last_success_at,last_attempt_at,created_at,updated_at)
				SELECT id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,snapshot_json,last_error,last_success_at,last_attempt_at,created_at,updated_at
				FROM custom_dashboard_cards_schema33`,
			`DROP TABLE custom_dashboard_cards_schema33`,
			`CREATE INDEX custom_dashboard_cards_order_idx ON custom_dashboard_cards(dashboard_id, sort_order, created_at)`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				return fmt.Errorf("migrate custom dashboard percentage cards: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		for _, statement := range []string{
			`ALTER TABLE custom_dashboard_cards RENAME TO custom_dashboard_cards_schema36`,
			`CREATE TABLE custom_dashboard_cards (
				id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
				name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','percentage','quota','key_value','website','registry')),
				source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
				value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
				config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
				sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
				last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
			)`,
			`INSERT INTO custom_dashboard_cards
				(id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,snapshot_json,last_error,last_success_at,last_attempt_at,created_at,updated_at)
				SELECT id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,snapshot_json,last_error,last_success_at,last_attempt_at,created_at,updated_at
				FROM custom_dashboard_cards_schema36`,
			`DROP TABLE custom_dashboard_cards_schema36`,
			`CREATE INDEX custom_dashboard_cards_order_idx ON custom_dashboard_cards(dashboard_id, sort_order, created_at)`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				return fmt.Errorf("migrate custom dashboard Registry cards: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		for _, column := range []struct{ name, definition string }{
			{"previous_hash", "previous_hash TEXT NOT NULL DEFAULT ''"},
			{"event_hash", "event_hash TEXT NOT NULL DEFAULT ''"},
		} {
			exists, err := storesqlite.ColumnExists(migration, "audit_events", column.name)
			if err != nil {
				return fmt.Errorf("inspect audit chain migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec("ALTER TABLE audit_events ADD COLUMN " + column.definition); err != nil {
					return fmt.Errorf("add audit chain column: %w", err)
				}
			}
		}
		if err := auditlog.BackfillTransaction(context.Background(), migration); err != nil {
			return fmt.Errorf("backfill audit hash chain: %w", err)
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		for _, column := range []struct{ name, definition string }{
			{"script_sha256", "script_sha256 TEXT NOT NULL DEFAULT ''"},
			{"revision", "revision INTEGER NOT NULL DEFAULT 1"},
		} {
			exists, err := storesqlite.ColumnExists(migration, "quick_runs", column.name)
			if err != nil {
				return fmt.Errorf("inspect Quick Run publication migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec("ALTER TABLE quick_runs ADD COLUMN " + column.definition); err != nil {
					return fmt.Errorf("add Quick Run publication column: %w", err)
				}
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		rows, err := migration.Query(`SELECT entry.id, entry.created_at
			FROM external_trigger_entries AS entry
			WHERE EXISTS (
				SELECT 1 FROM external_trigger_entries AS earlier
				WHERE earlier.key_id = entry.key_id
				AND (earlier.created_at < entry.created_at OR (earlier.created_at = entry.created_at AND earlier.id < entry.id))
			)
			ORDER BY entry.key_id, entry.created_at, entry.id`)
		if err != nil {
			return fmt.Errorf("inspect External Interface capability migration: %w", err)
		}
		type extraExternalEntry struct {
			id        string
			createdAt int64
		}
		var extras []extraExternalEntry
		for rows.Next() {
			var entry extraExternalEntry
			if err := rows.Scan(&entry.id, &entry.createdAt); err != nil {
				_ = rows.Close()
				return fmt.Errorf("read External Interface capability migration: %w", err)
			}
			extras = append(extras, entry)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("iterate External Interface capability migration: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("finish External Interface capability migration: %w", err)
		}
		now := options.Now().UTC().Unix()
		for _, entry := range extras {
			keyID, keyErr := options.RandomToken(12)
			secretPart, secretErr := options.RandomToken(32)
			if keyErr != nil || secretErr != nil {
				return fmt.Errorf("generate migrated External Interface capability key: %v %v", keyErr, secretErr)
			}
			secret := "sbk_" + keyID + "." + secretPart
			if _, err := migration.Exec(`INSERT INTO external_trigger_keys
				(id, label, token_hash, token_hint, enabled, expires_at, created_at, updated_at, last_used_at)
				VALUES (?, ?, ?, ?, 0, NULL, ?, ?, NULL)`,
				keyID, "Migrated capability "+entry.id, options.HashToken(secret), "rotation required", entry.createdAt, now); err != nil {
				return fmt.Errorf("create migrated External Interface capability key: %w", err)
			}
			if _, err := migration.Exec(`UPDATE external_trigger_entries SET key_id = ?, enabled = 0, updated_at = ? WHERE id = ?`, keyID, now, entry.id); err != nil {
				return fmt.Errorf("split migrated External Interface capability: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		exists, err := storesqlite.ColumnExists(migration, "external_trigger_entries", "require_signature")
		if err != nil {
			return fmt.Errorf("inspect External Interface signature migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE external_trigger_entries ADD COLUMN require_signature INTEGER NOT NULL DEFAULT 0 CHECK (require_signature IN (0, 1))`); err != nil {
				return fmt.Errorf("add External Interface signature requirement: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		for _, column := range []struct{ name, definition string }{
			{"authentication_assurance", "authentication_assurance INTEGER NOT NULL DEFAULT 1 CHECK (authentication_assurance BETWEEN 1 AND 2)"},
			{"reauthenticated_at", "reauthenticated_at INTEGER NOT NULL DEFAULT 0"},
		} {
			exists, err := storesqlite.ColumnExists(migration, "sessions", column.name)
			if err != nil {
				return fmt.Errorf("inspect session authentication assurance migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec("ALTER TABLE sessions ADD COLUMN " + column.definition); err != nil {
					return fmt.Errorf("add session authentication assurance: %w", err)
				}
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		for _, column := range []struct{ name, definition string }{
			{"request_id", "request_id TEXT NOT NULL DEFAULT ''"},
			{"authentication_assurance", "authentication_assurance TEXT NOT NULL DEFAULT ''"},
		} {
			exists, err := storesqlite.ColumnExists(migration, "audit_events", column.name)
			if err != nil {
				return fmt.Errorf("inspect audit correlation migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec("ALTER TABLE audit_events ADD COLUMN " + column.definition); err != nil {
					return fmt.Errorf("add audit correlation metadata: %w", err)
				}
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 52 {
		exists, err := storesqlite.ColumnExists(migration, "users", "mfa_required_at")
		if err != nil {
			return fmt.Errorf("inspect legacy MFA enrollment deadline: %w", err)
		}
		if exists {
			if _, err := migration.Exec(`ALTER TABLE users DROP COLUMN mfa_required_at`); err != nil {
				return fmt.Errorf("remove legacy MFA enrollment deadline: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 43 {
		for _, column := range []struct{ name, definition string }{
			{"resource_revision", "resource_revision TEXT NOT NULL DEFAULT ''"},
			{"resource_digest_sha256", "resource_digest_sha256 TEXT NOT NULL DEFAULT ''"},
		} {
			exists, err := storesqlite.ColumnExists(migration, "audit_events", column.name)
			if err != nil {
				return fmt.Errorf("inspect audit resource identity migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec("ALTER TABLE audit_events ADD COLUMN " + column.definition); err != nil {
					return fmt.Errorf("add audit resource identity: %w", err)
				}
			}
		}
	}
	if err := migrateExternalInterfaceGroups(migration, schemaVersion); err != nil {
		return err
	}
	if err := migrateExternalInterfaceGroupCallNames(migration, schemaVersion); err != nil {
		return err
	}
	for _, statement := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS users_single_administrator_idx ON users(role) WHERE role = 'administrator'",
		"CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id)",
		"CREATE INDEX IF NOT EXISTS quick_run_groups_order_idx ON quick_run_groups(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS quick_runs_group_order_idx ON quick_runs(group_id, sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS quick_runs_script_path_idx ON quick_runs(script_path_key)",
		"CREATE INDEX IF NOT EXISTS schedules_group_idx ON schedules(group_name, created_at)",
		"CREATE INDEX IF NOT EXISTS schedule_groups_order_idx ON schedule_groups(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_group_order_idx ON schedules(group_id, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_script_path_idx ON schedules(script_path_key)",
		"CREATE INDEX IF NOT EXISTS runs_source_idx ON runs(source_type, source_id, created_at DESC)",
		"CREATE INDEX IF NOT EXISTS runs_source_audit_idx ON runs(source_audit_event_id)",
		"CREATE INDEX IF NOT EXISTS runs_created_idx ON runs(created_at DESC)",
		"CREATE INDEX IF NOT EXISTS runs_script_path_idx ON runs(script_path_key)",
		"CREATE INDEX IF NOT EXISTS runs_log_cleanup_idx ON runs(log_expired, created_at)",
		"CREATE INDEX IF NOT EXISTS audit_events_occurred_idx ON audit_events(occurred_at DESC)",
		"CREATE INDEX IF NOT EXISTS trash_entries_deleted_idx ON trash_entries(deleted_at DESC)",
		"CREATE INDEX IF NOT EXISTS trash_entries_original_path_idx ON trash_entries(original_path_key)",
		"CREATE INDEX IF NOT EXISTS file_operations_phase_idx ON file_operations(phase, created_at)",
		"CREATE INDEX IF NOT EXISTS file_quick_access_pins_order_idx ON file_quick_access_pins(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS file_quick_access_pins_group_order_idx ON file_quick_access_pins(group_id, sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS website_monitors_group_order_idx ON website_monitors(group_id, sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS variables_group_order_idx ON variables(group_id, sort_order, name)",
		"CREATE INDEX IF NOT EXISTS schedules_shared_group_order_idx ON schedules(group_id, sort_order, created_at)",
		"DROP INDEX IF EXISTS external_trigger_entries_key_unique_idx",
		"CREATE INDEX IF NOT EXISTS external_trigger_keys_group_idx ON external_trigger_keys(group_id, created_at)",
		"CREATE INDEX IF NOT EXISTS external_trigger_entries_group_idx ON external_trigger_entries(group_id, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_due_idx ON schedules(next_fire_at) WHERE enabled = 1 AND deleted = 0",
		"CREATE INDEX IF NOT EXISTS schedule_triggers_schedule_time_idx ON schedule_triggers(schedule_id, scheduled_for DESC)",
		"CREATE INDEX IF NOT EXISTS schedule_triggers_unlinked_time_idx ON schedule_triggers(scheduled_for) WHERE run_id = ''",
		"CREATE INDEX IF NOT EXISTS application_pins_order_idx ON application_pins(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS application_metric_minutes_bucket_idx ON application_metric_minutes(bucket_at)",
		"CREATE INDEX IF NOT EXISTS kubernetes_connection_name_idx ON kubernetes_connection(name)",
		"CREATE INDEX IF NOT EXISTS kubernetes_versions_workload_idx ON kubernetes_versions(connection_id, workload_key, observed_at DESC)",
		"CREATE INDEX IF NOT EXISTS kubernetes_metric_minutes_bucket_idx ON kubernetes_metric_minutes(connection_id, bucket_at)",
	} {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("initialize SQLite indexes: %w", err)
		}
	}
	if _, err := migration.Exec(fmt.Sprintf("PRAGMA user_version=%d", options.CurrentVersion)); err != nil {
		return fmt.Errorf("record SQLite schema version: %w", err)
	}
	if _, err := migration.Exec(`UPDATE runs SET status = 'disconnected', finished_at = ?, error = CASE WHEN error = '' THEN 'service supervision was lost' ELSE error END WHERE status IN ('starting', 'running', 'stopping', 'timing_out')`, options.Now().UnixNano()); err != nil {
		return fmt.Errorf("recover disconnected runs: %w", err)
	}
	if err := migration.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration: %w", err)
	}
	return nil
}
