package mcpaccess

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
		id TEXT PRIMARY KEY, client_id TEXT NOT NULL UNIQUE, registration_type TEXT NOT NULL,
		name TEXT NOT NULL, redirect_uris_json TEXT NOT NULL, metadata_uri TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, revoked_at INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS mcp_oauth_authorizations (
		id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		client_id TEXT NOT NULL, scopes TEXT NOT NULL, auth_version INTEGER NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, revoked_at INTEGER,
		UNIQUE(user_id, client_id)
	)`,
	`CREATE TABLE IF NOT EXISTS mcp_oauth_codes (
		token_hash TEXT PRIMARY KEY, authorization_id TEXT NOT NULL REFERENCES mcp_oauth_authorizations(id) ON DELETE CASCADE,
		client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, resource TEXT NOT NULL,
		code_challenge TEXT NOT NULL, scopes TEXT NOT NULL, expires_at INTEGER NOT NULL, consumed_at INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS mcp_oauth_token_families (
		id TEXT PRIMARY KEY, authorization_id TEXT NOT NULL REFERENCES mcp_oauth_authorizations(id) ON DELETE CASCADE,
		client_id TEXT NOT NULL, absolute_expires_at INTEGER NOT NULL, revoked_at INTEGER, created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS mcp_oauth_access_tokens (
		token_hash TEXT PRIMARY KEY, token_hint TEXT NOT NULL, family_id TEXT NOT NULL REFERENCES mcp_oauth_token_families(id) ON DELETE CASCADE,
		user_id TEXT NOT NULL, client_id TEXT NOT NULL, scopes TEXT NOT NULL, resource TEXT NOT NULL,
		auth_version INTEGER NOT NULL, expires_at INTEGER NOT NULL, revoked_at INTEGER, created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS mcp_oauth_refresh_tokens (
		token_hash TEXT PRIMARY KEY, token_hint TEXT NOT NULL, family_id TEXT NOT NULL REFERENCES mcp_oauth_token_families(id) ON DELETE CASCADE,
		user_id TEXT NOT NULL, client_id TEXT NOT NULL, scopes TEXT NOT NULL, resource TEXT NOT NULL,
		auth_version INTEGER NOT NULL, expires_at INTEGER NOT NULL, consumed_at INTEGER, created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS mcp_invocations (
		id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, user_id TEXT NOT NULL,
		client_id TEXT NOT NULL, tool_name TEXT NOT NULL, target TEXT NOT NULL, parameter_digest TEXT NOT NULL,
		result TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS mcp_idempotency (
		user_id TEXT NOT NULL, client_id TEXT NOT NULL, tool_name TEXT NOT NULL, request_id TEXT NOT NULL,
		result_json TEXT NOT NULL, created_at INTEGER NOT NULL,
		PRIMARY KEY(user_id, client_id, tool_name, request_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_mcp_access_expiry ON mcp_oauth_access_tokens(expires_at)`,
	`CREATE INDEX IF NOT EXISTS idx_mcp_refresh_family ON mcp_oauth_refresh_tokens(family_id)`,
	`CREATE INDEX IF NOT EXISTS idx_mcp_invocations_time ON mcp_invocations(occurred_at DESC)`,
}
