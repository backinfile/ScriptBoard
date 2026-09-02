package redismanager

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS redis_instances (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		environment TEXT NOT NULL DEFAULT 'unspecified' CHECK(environment IN ('production','development','unspecified')),
		host TEXT NOT NULL,
		port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535),
		username TEXT NOT NULL DEFAULT '',
		tls_mode TEXT NOT NULL CHECK(tls_mode IN ('disabled','verify_identity','insecure_skip_verify')),
		ca_path TEXT NOT NULL DEFAULT '',
		credential_configured INTEGER NOT NULL DEFAULT 0 CHECK(credential_configured IN (0,1)),
		connection_state TEXT NOT NULL DEFAULT 'untried' CHECK(connection_state IN ('untried','connected','failed')),
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
}
