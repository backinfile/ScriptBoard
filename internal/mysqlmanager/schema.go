package mysqlmanager

// SchemaStatements is owned by the MySQL management module so application
// startup and module tests use the same persistence contract.
var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS mysql_instances (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		host TEXT NOT NULL,
		port INTEGER NOT NULL,
		username TEXT NOT NULL,
		tls_mode TEXT NOT NULL CHECK (tls_mode IN ('disabled', 'preferred', 'required', 'verify_identity')),
		ca_path TEXT NOT NULL DEFAULT '',
		credential_configured INTEGER NOT NULL DEFAULT 0,
		connection_state TEXT NOT NULL DEFAULT 'untried' CHECK (connection_state IN ('untried', 'connected', 'failed')),
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS mysql_backup_plans (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		instance_id TEXT NOT NULL REFERENCES mysql_instances(id),
		databases_json TEXT NOT NULL,
		expression TEXT NOT NULL,
		retention_count INTEGER NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		next_fire_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS mysql_backups (
		id TEXT PRIMARY KEY,
		instance_id TEXT NOT NULL,
		database_name TEXT NOT NULL,
		plan_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL CHECK (kind IN ('manual', 'scheduled', 'imported', 'safety')),
		path TEXT NOT NULL UNIQUE,
		size_bytes INTEGER NOT NULL,
		sha256 TEXT NOT NULL,
		warning TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		created_by_user_id TEXT NOT NULL DEFAULT '',
		created_by_username TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS mysql_operations (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		database_name TEXT NOT NULL,
		target_database TEXT NOT NULL DEFAULT '',
		backup_id TEXT NOT NULL DEFAULT '',
		safety_backup_id TEXT NOT NULL DEFAULT '',
		phase TEXT NOT NULL,
		bytes_total INTEGER NOT NULL DEFAULT 0,
		bytes_completed INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		rollback_error TEXT NOT NULL DEFAULT '',
		cancel_requested INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		actor_user_id TEXT NOT NULL DEFAULT '',
		actor_username TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS mysql_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS mysql_backups_instance_database_idx ON mysql_backups(instance_id, database_name, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS mysql_backups_plan_idx ON mysql_backups(plan_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS mysql_operations_instance_phase_idx ON mysql_operations(instance_id, phase, created_at)`,
	`CREATE INDEX IF NOT EXISTS mysql_backup_plans_due_idx ON mysql_backup_plans(next_fire_at) WHERE enabled = 1`,
}
