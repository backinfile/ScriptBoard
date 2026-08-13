package migrations

import (
	"database/sql"
	"fmt"

	storesqlite "scriptboard/internal/store/sqlite"
)

func migrateKubernetesConnections(migration *sql.Tx) error {
	singleton, err := storesqlite.ColumnExists(migration, "kubernetes_connection", "singleton")
	if err != nil {
		return fmt.Errorf("inspect Kubernetes connection migration: %w", err)
	}
	if !singleton {
		return nil
	}
	for _, statement := range []string{
		`ALTER TABLE kubernetes_connection RENAME TO kubernetes_connection_singleton`,
		`ALTER TABLE kubernetes_versions RENAME TO kubernetes_versions_singleton`,
		`ALTER TABLE kubernetes_metric_minutes RENAME TO kubernetes_metric_minutes_singleton`,
		`CREATE TABLE kubernetes_connection (
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
		`CREATE TABLE kubernetes_versions (
			connection_id TEXT NOT NULL REFERENCES kubernetes_connection(id) ON DELETE CASCADE,
			workload_key TEXT NOT NULL,
			observed_at INTEGER NOT NULL,
			image TEXT NOT NULL,
			revision TEXT NOT NULL,
			PRIMARY KEY (connection_id, workload_key, observed_at)
		)`,
		`CREATE TABLE kubernetes_metric_minutes (
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
		`INSERT INTO kubernetes_connection
			(id,name,kubeconfig_path,context_name,operation_mode,fingerprint,capabilities_json,last_tested_at,last_error,updated_at)
			SELECT 'k8s_legacy',name,kubeconfig_path,context_name,operation_mode,fingerprint,capabilities_json,last_tested_at,last_error,updated_at
			FROM kubernetes_connection_singleton WHERE singleton=1`,
		`INSERT INTO kubernetes_versions (connection_id,workload_key,observed_at,image,revision)
			SELECT 'k8s_legacy',workload_key,observed_at,image,revision FROM kubernetes_versions_singleton`,
		`INSERT INTO kubernetes_metric_minutes (connection_id,workload_key,bucket_at,cpu_millicores,memory_bytes,ready,desired,restarts)
			SELECT 'k8s_legacy',workload_key,bucket_at,cpu_millicores,memory_bytes,ready,desired,restarts FROM kubernetes_metric_minutes_singleton`,
		`DROP TABLE kubernetes_metric_minutes_singleton`,
		`DROP TABLE kubernetes_versions_singleton`,
		`DROP TABLE kubernetes_connection_singleton`,
	} {
		if _, err := migration.Exec(statement); err != nil {
			return fmt.Errorf("migrate Kubernetes connections: %w", err)
		}
	}
	return nil
}
