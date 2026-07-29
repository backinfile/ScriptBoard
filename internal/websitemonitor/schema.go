package websitemonitor

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS website_monitors (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		scope TEXT NOT NULL CHECK (scope IN ('local', 'external')),
		kind TEXT NOT NULL CHECK (kind IN ('http', 'websocket')),
		url TEXT NOT NULL,
		config_json TEXT NOT NULL,
		frequency_seconds INTEGER NOT NULL,
		timeout_seconds INTEGER NOT NULL,
		sort_order INTEGER NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('pending', 'up', 'verifying', 'down', 'paused')),
		failure_count INTEGER NOT NULL DEFAULT 0,
		generation INTEGER NOT NULL DEFAULT 1,
		next_check_at INTEGER NOT NULL DEFAULT 0,
		last_success INTEGER NOT NULL DEFAULT 0,
		last_status_code INTEGER NOT NULL DEFAULT 0,
		last_latency_ms INTEGER NOT NULL DEFAULT 0,
		last_checked_at INTEGER NOT NULL DEFAULT 0,
		last_error_category TEXT NOT NULL DEFAULT '',
		last_summary TEXT NOT NULL DEFAULT '',
		last_technical_error TEXT NOT NULL DEFAULT '',
		last_certificate_json TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		deleted_at INTEGER
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS website_monitors_active_name
		ON website_monitors(lower(name)) WHERE deleted_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS website_monitors_due
		ON website_monitors(next_check_at) WHERE deleted_at IS NULL AND state <> 'paused'`,
	`CREATE TABLE IF NOT EXISTS website_check_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id TEXT NOT NULL REFERENCES website_monitors(id),
		checked_at INTEGER NOT NULL,
		success INTEGER NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		error_category TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		technical_error TEXT NOT NULL DEFAULT '',
		certificate_json TEXT NOT NULL DEFAULT '{}'
	)`,
	`CREATE INDEX IF NOT EXISTS website_check_results_monitor_time
		ON website_check_results(monitor_id, checked_at DESC)`,
	`CREATE TABLE IF NOT EXISTS website_hourly_aggregates (
		monitor_id TEXT NOT NULL REFERENCES website_monitors(id),
		bucket_at INTEGER NOT NULL,
		total_checks INTEGER NOT NULL,
		successful_checks INTEGER NOT NULL,
		failed_checks INTEGER NOT NULL,
		average_latency_ms INTEGER NOT NULL,
		maximum_latency_ms INTEGER NOT NULL,
		error_counts_json TEXT NOT NULL DEFAULT '{}',
		PRIMARY KEY (monitor_id, bucket_at)
	)`,
	`CREATE TABLE IF NOT EXISTS website_incidents (
		id TEXT PRIMARY KEY,
		monitor_id TEXT NOT NULL REFERENCES website_monitors(id),
		started_at INTEGER NOT NULL,
		ended_at INTEGER,
		start_category TEXT NOT NULL,
		start_summary TEXT NOT NULL,
		close_reason TEXT NOT NULL DEFAULT ''
	)`,
}
