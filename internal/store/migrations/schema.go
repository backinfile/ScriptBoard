package migrations

var baseSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('administrator', 'maintainer', 'operator', 'viewer')),
			enabled INTEGER NOT NULL DEFAULT 1,
			auth_version INTEGER NOT NULL DEFAULT 1,
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
			note TEXT NOT NULL DEFAULT '',
			value_type TEXT NOT NULL DEFAULT 'text' CHECK (value_type IN ('text', 'bool', 'integer', 'float', 'version')),
			is_password INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 1,
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
}
