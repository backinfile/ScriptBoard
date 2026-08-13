package clusterstatus

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS kubernetes_connection (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		kubeconfig_path TEXT NOT NULL,
		context_name TEXT NOT NULL DEFAULT '',
		operation_mode TEXT NOT NULL CHECK (operation_mode IN ('observe', 'limited')),
		fingerprint TEXT NOT NULL DEFAULT '',
		capabilities_json TEXT NOT NULL DEFAULT '{}',
		last_tested_at INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		UNIQUE (name)
	)`,
	`CREATE TABLE IF NOT EXISTS kubernetes_versions (
		connection_id TEXT NOT NULL REFERENCES kubernetes_connection(id) ON DELETE CASCADE,
		workload_key TEXT NOT NULL,
		observed_at INTEGER NOT NULL,
		image TEXT NOT NULL,
		revision TEXT NOT NULL,
		PRIMARY KEY (connection_id, workload_key, observed_at)
	)`,
	`CREATE TABLE IF NOT EXISTS kubernetes_metric_minutes (
		connection_id TEXT NOT NULL REFERENCES kubernetes_connection(id) ON DELETE CASCADE,
		workload_key TEXT NOT NULL,
		bucket_at INTEGER NOT NULL,
		cpu_millicores INTEGER NOT NULL,
		memory_bytes INTEGER NOT NULL,
		ready INTEGER NOT NULL,
		desired INTEGER NOT NULL,
		restarts INTEGER NOT NULL,
		PRIMARY KEY (connection_id, workload_key, bucket_at)
	)`,
}
