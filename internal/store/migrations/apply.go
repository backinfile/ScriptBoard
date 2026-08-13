package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"scriptboard/internal/assistant"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/customdashboard"
	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/mysqlmanager"
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
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('administrator', 'maintainer', 'operator', 'viewer')),
			enabled INTEGER NOT NULL DEFAULT 1,
			auth_version INTEGER NOT NULL DEFAULT 1,
			mfa_required_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			auth_version INTEGER NOT NULL,
			authentication_assurance INTEGER NOT NULL DEFAULT 1 CHECK (authentication_assurance BETWEEN 1 AND 2),
			reauthenticated_at INTEGER NOT NULL DEFAULT 0,
			csrf_token TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at INTEGER NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			result TEXT NOT NULL,
			source_address TEXT NOT NULL,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_username TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			authentication_assurance TEXT NOT NULL DEFAULT '',
			resource_revision TEXT NOT NULL DEFAULT '',
			resource_digest_sha256 TEXT NOT NULL DEFAULT '',
			previous_hash TEXT NOT NULL DEFAULT '',
			event_hash TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS instance_settings (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			display_name TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			updated_by_user_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS audit_chain_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			anchor_hash TEXT NOT NULL DEFAULT '',
			tail_hash TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT OR IGNORE INTO audit_chain_state (id, anchor_hash, tail_hash) VALUES (1, '', '')`,
		`CREATE TABLE IF NOT EXISTS trash_entries (
			id TEXT PRIMARY KEY,
			original_path TEXT NOT NULL,
			original_path_key TEXT NOT NULL,
			stored_path TEXT NOT NULL UNIQUE,
			stored_path_key TEXT NOT NULL UNIQUE,
			deleted_at INTEGER NOT NULL,
			size INTEGER NOT NULL,
			is_directory INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			script_path TEXT NOT NULL,
			script_path_key TEXT NOT NULL,
			script_sha256 TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			template_arguments_json TEXT NOT NULL DEFAULT '[]',
			arguments_json TEXT NOT NULL,
			executor TEXT NOT NULL,
			source_type TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			started_at INTEGER,
			finished_at INTEGER,
			exit_code INTEGER,
			error TEXT NOT NULL DEFAULT '',
			timeout_seconds INTEGER NOT NULL DEFAULT 0,
			log_path TEXT NOT NULL
			, source_name TEXT NOT NULL DEFAULT ''
			, source_id TEXT NOT NULL DEFAULT ''
			, runtime_identity TEXT NOT NULL DEFAULT ''
			, log_expired INTEGER NOT NULL DEFAULT 0
			, log_incomplete INTEGER NOT NULL DEFAULT 0
			, log_truncated INTEGER NOT NULL DEFAULT 0
			, dropped_bytes INTEGER NOT NULL DEFAULT 0
			, script_kind TEXT NOT NULL DEFAULT 'host_file'
			, working_directory TEXT NOT NULL DEFAULT ''
			, working_directory_key TEXT NOT NULL DEFAULT ''
			, source_filename TEXT NOT NULL DEFAULT ''
			, source_expired INTEGER NOT NULL DEFAULT 0
			, source_audit_event_id INTEGER REFERENCES audit_events(id)
			, log_bytes INTEGER NOT NULL DEFAULT -1
			, initiated_by_user_id TEXT NOT NULL DEFAULT ''
			, initiated_by_username TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS variables (
			name TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			is_password INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS quick_run_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS quick_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			script_path TEXT NOT NULL,
			script_path_key TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			source_run_id TEXT REFERENCES runs(id),
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL,
			locked INTEGER NOT NULL DEFAULT 0,
			script_sha256 TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			group_name TEXT NOT NULL DEFAULT '',
			group_id TEXT REFERENCES schedule_groups(id) ON DELETE SET NULL,
			script_path TEXT NOT NULL,
			script_path_key TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			expression TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			enabled INTEGER NOT NULL,
			allow_overlap INTEGER NOT NULL,
			next_fire_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
			, deleted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_triggers (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL REFERENCES schedules(id),
			scheduled_for INTEGER NOT NULL,
			result TEXT NOT NULL,
			run_id TEXT NOT NULL,
			error TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_trigger_aggregates (
			schedule_id TEXT NOT NULL REFERENCES schedules(id),
			period TEXT NOT NULL,
			result TEXT NOT NULL,
			trigger_count INTEGER NOT NULL,
			PRIMARY KEY (schedule_id, period, result)
		)`,
		`CREATE TABLE IF NOT EXISTS file_operations (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('cross_filesystem_move')),
			source_path TEXT NOT NULL,
			source_path_key TEXT NOT NULL,
			destination_path TEXT NOT NULL,
			destination_path_key TEXT NOT NULL,
			temporary_path TEXT NOT NULL DEFAULT '',
			trash_path TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL,
			bytes_total INTEGER NOT NULL DEFAULT 0,
			bytes_completed INTEGER NOT NULL DEFAULT 0,
			verification_digest TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS file_quick_access_pins (
			path TEXT NOT NULL,
			path_key TEXT PRIMARY KEY,
			label TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS host_metric_minutes (
			bucket_at INTEGER PRIMARY KEY,
			sample_count INTEGER NOT NULL,
			average_json TEXT NOT NULL,
			maximum_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS application_pins (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('host', 'docker')),
			identity TEXT NOT NULL,
			name TEXT NOT NULL,
			technical TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (kind, identity)
		)`,
		`CREATE TABLE IF NOT EXISTS application_metric_minutes (
			application_id TEXT NOT NULL,
			bucket_at INTEGER NOT NULL,
			sample_count INTEGER NOT NULL,
			cpu_average REAL NOT NULL,
			cpu_maximum REAL NOT NULL,
			memory_average INTEGER NOT NULL,
			memory_maximum INTEGER NOT NULL,
			read_average REAL NOT NULL,
			read_maximum REAL NOT NULL,
			write_average REAL NOT NULL,
			write_maximum REAL NOT NULL,
			PRIMARY KEY (application_id, bucket_at)
		)`,
		`CREATE TABLE IF NOT EXISTS application_versions (
			application_id TEXT NOT NULL,
			observed_at INTEGER NOT NULL,
			image TEXT NOT NULL,
			container_id TEXT NOT NULL,
			PRIMARY KEY (application_id, observed_at)
		)`,
	} {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("初始化 SQLite: %w", err)
		}
	}
	for _, statement := range websitemonitor.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("initialize Website Monitor SQLite schema: %w", err)
		}
	}
	for _, statement := range assistant.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("initialize Assistant SQLite schema: %w", err)
		}
	}
	for _, statement := range externaltrigger.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("initialize External Interface SQLite schema: %w", err)
		}
	}
	for _, statement := range mysqlmanager.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("initialize MySQL management SQLite schema: %w", err)
		}
	}
	for _, statement := range customdashboard.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("initialize custom dashboard SQLite schema: %w", err)
		}
	}
	for _, statement := range clusterstatus.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("initialize Kubernetes monitoring SQLite schema: %w", err)
		}
	}
	if schemaVersion == 21 {
		if _, err := migration.Exec(`ALTER TABLE assistant_tool_calls ADD COLUMN body_offset INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate Assistant tool-call positions: %w", err)
		}
		if _, err := migration.Exec(`UPDATE assistant_tool_calls SET body_offset = COALESCE(
			(SELECT LENGTH(body) FROM assistant_messages WHERE id = assistant_tool_calls.message_id), 0)`); err != nil {
			return fmt.Errorf("backfill Assistant tool-call positions: %w", err)
		}
	}
	if schemaVersion == 21 || schemaVersion == 22 {
		for _, statement := range []string{
			`ALTER TABLE assistant_tool_calls ADD COLUMN request_json TEXT NOT NULL DEFAULT '{}'`,
			`ALTER TABLE assistant_tool_calls ADD COLUMN response_json TEXT NOT NULL DEFAULT 'null'`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				return fmt.Errorf("migrate Assistant tool-call JSON payloads: %w", err)
			}
		}
	}
	if schemaVersion >= 21 && schemaVersion <= 23 {
		exists, err := storesqlite.ColumnExists(migration, "assistant_models", "supports_images")
		if err != nil {
			return fmt.Errorf("inspect Assistant model capability migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN supports_images INTEGER NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("migrate Assistant model image capability: %w", err)
			}
		}
		for _, column := range []struct{ name, definition string }{
			{"capability_profile", `capability_profile TEXT NOT NULL DEFAULT 'general' CHECK (capability_profile IN ('general', 'diagnose-failed-run', 'investigate-website-incident', 'triage-host-pressure', 'review-script-safety', 'design-schedule'))`},
			{"profile_version", `profile_version TEXT NOT NULL DEFAULT ''`},
			{"thinking_level", `thinking_level TEXT NOT NULL DEFAULT 'medium' CHECK (thinking_level IN ('off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'))`},
			{"stats_user_messages", `stats_user_messages INTEGER NOT NULL DEFAULT 0`},
			{"stats_assistant_messages", `stats_assistant_messages INTEGER NOT NULL DEFAULT 0`},
			{"stats_tool_calls", `stats_tool_calls INTEGER NOT NULL DEFAULT 0`},
			{"stats_tool_results", `stats_tool_results INTEGER NOT NULL DEFAULT 0`},
			{"stats_total_messages", `stats_total_messages INTEGER NOT NULL DEFAULT 0`},
			{"stats_input_tokens", `stats_input_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_output_tokens", `stats_output_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_cache_read_tokens", `stats_cache_read_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_cache_write_tokens", `stats_cache_write_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_total_tokens", `stats_total_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_cost", `stats_cost REAL NOT NULL DEFAULT 0`},
			{"stats_context_tokens", `stats_context_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_context_window", `stats_context_window INTEGER NOT NULL DEFAULT 0`},
			{"stats_context_percent", `stats_context_percent REAL`},
			{"stats_updated_at", `stats_updated_at INTEGER NOT NULL DEFAULT 0`},
		} {
			exists, err := storesqlite.ColumnExists(migration, "assistant_conversations", column.name)
			if err != nil {
				return fmt.Errorf("inspect Assistant capability migration: %w", err)
			}
			if exists {
				continue
			}
			if _, err := migration.Exec(`ALTER TABLE assistant_conversations ADD COLUMN ` + column.definition); err != nil {
				return fmt.Errorf("migrate Assistant capability profiles and telemetry: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 24 {
		for _, column := range []struct{ name, definition string }{
			{"owner_user_id", `owner_user_id TEXT NOT NULL DEFAULT ''`},
			{"is_shared", `is_shared INTEGER NOT NULL DEFAULT 0`},
		} {
			exists, err := storesqlite.ColumnExists(migration, "assistant_models", column.name)
			if err != nil {
				return fmt.Errorf("inspect Assistant model visibility migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN ` + column.definition); err != nil {
					return fmt.Errorf("migrate Assistant model visibility: %w", err)
				}
			}
		}
		if _, err := migration.Exec(`UPDATE assistant_models SET owner_user_id = COALESCE(
			NULLIF(owner_user_id, ''), NULLIF(updated_by_user_id, ''),
			(SELECT id FROM users WHERE role = 'administrator' LIMIT 1), 'legacy-owner')
			WHERE owner_user_id = ''`); err != nil {
			return fmt.Errorf("backfill Assistant model owners: %w", err)
		}
		if _, err := migration.Exec(`DROP INDEX IF EXISTS assistant_models_default_idx`); err != nil {
			return fmt.Errorf("replace Assistant model default index: %w", err)
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 25 {
		exists, err := storesqlite.ColumnExists(migration, "assistant_models", "connection_ok")
		if err != nil {
			return fmt.Errorf("inspect Assistant model connection migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN connection_ok INTEGER NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("migrate Assistant model connection status: %w", err)
			}
		}
	}
	if schemaVersion == 28 {
		for _, statement := range []string{
			`ALTER TABLE file_quick_access_pins RENAME TO file_quick_access_pins_user_scoped`,
			`CREATE TABLE file_quick_access_pins (
				path TEXT NOT NULL,
				path_key TEXT PRIMARY KEY,
				label TEXT NOT NULL,
				sort_order INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			)`,
			`INSERT INTO file_quick_access_pins (path, path_key, label, sort_order, created_at)
				SELECT path, path_key, label, MIN(sort_order), MIN(created_at)
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
	if schemaVersion >= 20 && schemaVersion <= 43 {
		for _, column := range []struct{ name, definition string }{
			{"supports_reasoning", `supports_reasoning INTEGER NOT NULL DEFAULT 0`},
			{"default_thinking_level", `default_thinking_level TEXT NOT NULL DEFAULT 'medium' CHECK (default_thinking_level IN ('off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'))`},
		} {
			exists, err := storesqlite.ColumnExists(migration, "assistant_models", column.name)
			if err != nil {
				return fmt.Errorf("inspect Assistant model reasoning migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN ` + column.definition); err != nil {
					return fmt.Errorf("migrate Assistant model reasoning defaults: %w", err)
				}
			}
		}
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
	if schemaVersion >= 20 && schemaVersion <= 43 {
		exists, err := storesqlite.ColumnExists(migration, "users", "mfa_required_at")
		if err != nil {
			return fmt.Errorf("inspect privileged MFA policy migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE users ADD COLUMN mfa_required_at INTEGER NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("add privileged MFA policy: %w", err)
			}
			// Existing privileged accounts receive a bounded enrollment window so an
			// upgrade cannot strand the only local administrator outside the panel.
			if _, err := migration.Exec(`UPDATE users SET mfa_required_at = ? WHERE role IN ('administrator', 'maintainer')`, options.Now().UTC().Add(7*24*time.Hour).Unix()); err != nil {
				return fmt.Errorf("schedule privileged MFA enrollment: %w", err)
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
		"CREATE UNIQUE INDEX IF NOT EXISTS external_trigger_entries_key_unique_idx ON external_trigger_entries(key_id)",
		"CREATE INDEX IF NOT EXISTS schedules_due_idx ON schedules(next_fire_at) WHERE enabled = 1 AND deleted = 0",
		"CREATE INDEX IF NOT EXISTS schedule_triggers_schedule_time_idx ON schedule_triggers(schedule_id, scheduled_for DESC)",
		"CREATE INDEX IF NOT EXISTS schedule_triggers_unlinked_time_idx ON schedule_triggers(scheduled_for) WHERE run_id = ''",
		"CREATE INDEX IF NOT EXISTS application_pins_order_idx ON application_pins(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS application_metric_minutes_bucket_idx ON application_metric_minutes(bucket_at)",
		"CREATE UNIQUE INDEX IF NOT EXISTS assistant_models_owner_default_idx ON assistant_models(owner_user_id) WHERE is_default = 1",
		"CREATE INDEX IF NOT EXISTS assistant_models_visibility_idx ON assistant_models(owner_user_id, is_shared, is_default DESC, created_at, name)",
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
