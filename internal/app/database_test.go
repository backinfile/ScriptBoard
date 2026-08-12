package app

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/assistant"
	"scriptboard/internal/hostfiles"
)

func TestOpenDatabaseRejectsPreMultiUserSchemaWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE admin (id INTEGER PRIMARY KEY, username TEXT NOT NULL, password_hash TEXT NOT NULL);
		INSERT INTO admin VALUES (1, 'legacy-admin', 'preserve-me');
		PRAGMA user_version=18;
	`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	beforeDatabase, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries := directoryEntryNames(t, filepath.Dir(path))

	_, err = openDatabase(path)
	if err == nil || !strings.Contains(err.Error(), "new State Root") {
		t.Fatalf("expected incompatible-schema rejection, got %v", err)
	}

	unchanged, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer unchanged.Close()
	var username, passwordHash string
	if err := unchanged.QueryRow("SELECT username, password_hash FROM admin").Scan(&username, &passwordHash); err != nil {
		t.Fatalf("legacy database was changed: %v", err)
	}
	if username != "legacy-admin" || passwordHash != "preserve-me" {
		t.Fatalf("legacy row changed to username=%q password_hash=%q", username, passwordHash)
	}
	afterDatabase, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDatabase, afterDatabase) {
		t.Fatal("legacy database bytes changed while rejecting its schema")
	}
	if afterEntries := directoryEntryNames(t, filepath.Dir(path)); !reflect.DeepEqual(beforeEntries, afterEntries) {
		t.Fatalf("legacy state directory changed: before=%v after=%v", beforeEntries, afterEntries)
	}
}

func TestOpenRejectsLegacyStateRootBeforeCreatingLocksOrChangingPermissions(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "legacy-state")
	if err := os.Mkdir(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(stateRoot, "app.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE legacy (value TEXT); INSERT INTO legacy VALUES ('preserve'); PRAGMA user_version=18;`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	beforeDatabase, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries := directoryEntryNames(t, stateRoot)
	beforeInfo, err := os.Stat(stateRoot)
	if err != nil {
		t.Fatal(err)
	}

	application, err := Open(Config{StateRoot: stateRoot})
	if application != nil {
		_ = application.Close()
		t.Fatal("legacy State Root unexpectedly opened")
	}
	if err == nil || !strings.Contains(err.Error(), "new State Root") {
		t.Fatalf("legacy State Root error = %v", err)
	}
	afterDatabase, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDatabase, afterDatabase) {
		t.Fatal("legacy database bytes changed during application startup rejection")
	}
	if afterEntries := directoryEntryNames(t, stateRoot); !reflect.DeepEqual(beforeEntries, afterEntries) {
		t.Fatalf("legacy State Root entries changed: before=%v after=%v", beforeEntries, afterEntries)
	}
	afterInfo, err := os.Stat(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if beforeInfo.Mode().Perm() != afterInfo.Mode().Perm() {
		t.Fatalf("legacy State Root permissions changed from %#o to %#o", beforeInfo.Mode().Perm(), afterInfo.Mode().Perm())
	}
}

func directoryEntryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func TestOpenDatabaseCreatesFixedRoleUserSchema(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version=%d, want %d", version, currentSchemaVersion)
	}
	var usersTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&usersTable); err != nil {
		t.Fatal(err)
	}
	if usersTable != 1 {
		t.Fatal("users table was not created")
	}
	var adminTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'admin'`).Scan(&adminTable); err != nil {
		t.Fatal(err)
	}
	if adminTable != 0 {
		t.Fatal("legacy admin table should not exist")
	}
	for _, table := range []string{"file_operations", "file_quick_access_pins", "trash_entries", "assistant_settings", "assistant_models", "assistant_conversations", "assistant_messages", "external_trigger_keys", "external_trigger_entries", "external_trigger_requests", "website_monitor_remote_sources"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("required schema table %q is missing: count=%d error=%v", table, count, err)
		}
	}
	var gitState int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'git_state'`).Scan(&gitState); err != nil || gitState != 0 {
		t.Fatalf("removed git_state table exists: count=%d error=%v", gitState, err)
	}

	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('admin-one', 'admin', 'hash', 'administrator', 1, 1, 1, 1)`); err != nil {
		t.Fatalf("insert first administrator: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('admin-two', 'admin2', 'hash', 'administrator', 1, 1, 1, 1)`); err == nil {
		t.Fatal("second administrator was accepted")
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('custom-role', 'custom', 'hash', 'custom', 1, 1, 1, 1)`); err == nil {
		t.Fatal("custom role was accepted")
	}
}

func TestOpenDatabaseMigratesSchema28QuickAccessPinsToGlobalScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		DROP TABLE file_quick_access_pins;
		CREATE TABLE file_quick_access_pins (
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			path_key TEXT NOT NULL,
			label TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (user_id, path_key)
		);
		INSERT INTO users (id, username, password_hash, role, enabled, auth_version, created_at, updated_at) VALUES
			('admin', 'admin', 'hash', 'administrator', 1, 1, 1, 1),
			('operator', 'operator', 'hash', 'operator', 1, 1, 1, 1);
		INSERT INTO file_quick_access_pins (user_id, path, path_key, label, sort_order, created_at) VALUES
			('admin', '/shared', '/shared', 'shared', 2, 2),
			('operator', '/shared', '/shared', 'shared', 1, 1),
			('operator', '/operator', '/operator', 'operator', 3, 3);
		PRAGMA user_version=28;
		PRAGMA wal_checkpoint(TRUNCATE);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, count int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version=%d, want %d", version, currentSchemaVersion)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM file_quick_access_pins").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("global Quick access pin count=%d, want 2", count)
	}
	var userIDColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('file_quick_access_pins') WHERE name = 'user_id'`).Scan(&userIDColumn); err != nil {
		t.Fatal(err)
	}
	if userIDColumn != 0 {
		t.Fatal("global Quick access table still contains user_id")
	}
}

func TestOpenDatabaseMigratesSchema20ToAssistantSchema23(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES ('preserved-admin', 'preserved', 'hash', 'administrator', 1, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"assistant_approvals", "assistant_tool_calls", "assistant_context_refs", "assistant_messages", "assistant_conversations", "assistant_models", "assistant_settings"} {
		if _, err := db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version=20; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 20: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	var username string
	if err := migrated.QueryRow("SELECT username FROM users WHERE id = 'preserved-admin'").Scan(&username); err != nil || username != "preserved" {
		t.Fatalf("preserved user = %q, error = %v", username, err)
	}
	for _, table := range []string{"assistant_settings", "assistant_models", "assistant_conversations", "assistant_messages"} {
		var count int
		if err := migrated.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migrated table %q count=%d error=%v", table, count, err)
		}
	}
}

func TestOpenDatabaseMigratesSchema21ToolCallsToTextPositions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO assistant_models (id, owner_user_id, name, provider, model, endpoint, credential_configured, is_default, created_at, updated_at, updated_by_user_id) VALUES ('model', 'owner', 'Model', 'openai-compatible', 'model', 'http://localhost', 1, 1, 1, 1, 'owner')`,
		`INSERT INTO assistant_conversations (id, owner_user_id, title, model_id, status, created_at, updated_at) VALUES ('conversation', 'owner', 'Conversation', 'model', 'idle', 1, 1)`,
		`DROP INDEX assistant_models_owner_default_idx`,
		`DROP INDEX assistant_models_visibility_idx`,
		`ALTER TABLE assistant_models DROP COLUMN is_shared`,
		`ALTER TABLE assistant_models DROP COLUMN owner_user_id`,
		`ALTER TABLE assistant_models DROP COLUMN supports_images`,
		`INSERT INTO assistant_messages (id, conversation_id, sequence, role, body, status, created_at, finished_at) VALUES ('message', 'conversation', 1, 'assistant', 'Before after', 'complete', 1, 2)`,
		`INSERT INTO assistant_tool_calls (id, conversation_id, message_id, tool_name, status, started_at) VALUES ('tool', 'conversation', 'message', 'inspect_host', 'complete', 1)`,
		`ALTER TABLE assistant_tool_calls DROP COLUMN body_offset`,
		`ALTER TABLE assistant_tool_calls DROP COLUMN request_json`,
		`ALTER TABLE assistant_tool_calls DROP COLUMN response_json`,
		`PRAGMA user_version=21`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 21 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 21: %v", err)
	}
	defer migrated.Close()
	var version, offset int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	if err := migrated.QueryRow("SELECT body_offset FROM assistant_tool_calls WHERE id = 'tool'").Scan(&offset); err != nil || offset != 12 {
		t.Fatalf("migrated body offset = %d, error = %v", offset, err)
	}
	var requestJSON, responseJSON string
	if err := migrated.QueryRow("SELECT request_json, response_json FROM assistant_tool_calls WHERE id = 'tool'").Scan(&requestJSON, &responseJSON); err != nil || requestJSON != "{}" || responseJSON != "null" {
		t.Fatalf("migrated JSON = %q / %q, error = %v", requestJSON, responseJSON, err)
	}
}

func TestOpenDatabaseMigratesSchema22ToolCallsToJSONPayloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO assistant_models (id, owner_user_id, name, provider, model, endpoint, credential_configured, is_default, created_at, updated_at, updated_by_user_id) VALUES ('model', 'owner', 'Model', 'openai-compatible', 'model', 'http://localhost', 1, 1, 1, 1, 'owner')`,
		`INSERT INTO assistant_conversations (id, owner_user_id, title, model_id, status, created_at, updated_at) VALUES ('conversation', 'owner', 'Conversation', 'model', 'idle', 1, 1)`,
		`DROP INDEX assistant_models_owner_default_idx`,
		`DROP INDEX assistant_models_visibility_idx`,
		`ALTER TABLE assistant_models DROP COLUMN is_shared`,
		`ALTER TABLE assistant_models DROP COLUMN owner_user_id`,
		`INSERT INTO assistant_messages (id, conversation_id, sequence, role, body, status, created_at, finished_at) VALUES ('message', 'conversation', 1, 'assistant', 'Done', 'complete', 1, 2)`,
		`INSERT INTO assistant_tool_calls (id, conversation_id, message_id, body_offset, tool_name, status, started_at) VALUES ('tool', 'conversation', 'message', 0, 'inspect_host', 'complete', 1)`,
		`ALTER TABLE assistant_tool_calls DROP COLUMN request_json`,
		`ALTER TABLE assistant_tool_calls DROP COLUMN response_json`,
		`PRAGMA user_version=22`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 22 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 22: %v", err)
	}
	defer migrated.Close()
	var version int
	var requestJSON, responseJSON string
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	if err := migrated.QueryRow("SELECT request_json, response_json FROM assistant_tool_calls WHERE id = 'tool'").Scan(&requestJSON, &responseJSON); err != nil || requestJSON != "{}" || responseJSON != "null" {
		t.Fatalf("migrated JSON = %q / %q, error = %v", requestJSON, responseJSON, err)
	}
}

func TestOpenDatabaseMigratesSchema23ConversationCapabilitiesAndTelemetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO assistant_models (id, owner_user_id, name, provider, model, endpoint, credential_configured, is_default, created_at, updated_at, updated_by_user_id) VALUES ('model', 'owner', 'Model', 'openai-compatible', 'model', 'http://localhost', 1, 1, 1, 1, 'owner')`,
		`INSERT INTO assistant_conversations (id, owner_user_id, title, model_id, status, created_at, updated_at) VALUES ('conversation', 'owner', 'Conversation', 'model', 'idle', 1, 1)`,
		`DROP INDEX assistant_models_owner_default_idx`,
		`DROP INDEX assistant_models_visibility_idx`,
		`ALTER TABLE assistant_models DROP COLUMN is_shared`,
		`ALTER TABLE assistant_models DROP COLUMN owner_user_id`,
		`ALTER TABLE assistant_models DROP COLUMN supports_images`,
	}
	for _, column := range []string{
		"capability_profile", "profile_version", "thinking_level", "stats_user_messages", "stats_assistant_messages", "stats_tool_calls", "stats_tool_results", "stats_total_messages",
		"stats_input_tokens", "stats_output_tokens", "stats_cache_read_tokens", "stats_cache_write_tokens", "stats_total_tokens", "stats_cost",
		"stats_context_tokens", "stats_context_window", "stats_context_percent", "stats_updated_at",
	} {
		statements = append(statements, `ALTER TABLE assistant_conversations DROP COLUMN `+column)
	}
	statements = append(statements, `PRAGMA user_version=23`, `PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 23 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 23: %v", err)
	}
	defer migrated.Close()
	var version int
	var profile, thinking string
	var totalTokens int64
	var supportsImages int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	if err := migrated.QueryRow(`SELECT capability_profile, thinking_level, stats_total_tokens FROM assistant_conversations WHERE id = 'conversation'`).Scan(&profile, &thinking, &totalTokens); err != nil {
		t.Fatal(err)
	}
	if profile != assistant.ProfileGeneral || thinking != "medium" || totalTokens != 0 {
		t.Fatalf("migrated defaults = %q %q %d", profile, thinking, totalTokens)
	}
	if err := migrated.QueryRow(`SELECT supports_images FROM assistant_models WHERE id = 'model'`).Scan(&supportsImages); err != nil || supportsImages != 0 {
		t.Fatalf("migrated image capability = %d, error = %v", supportsImages, err)
	}
}

func TestOpenDatabaseMigratesSchema24ModelVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO assistant_models (id, owner_user_id, name, provider, model, endpoint, credential_configured, is_default, created_at, updated_at, updated_by_user_id) VALUES ('model', 'owner', 'Model', 'openai-compatible', 'model', 'http://localhost', 1, 1, 1, 1, 'owner')`,
		`DROP INDEX assistant_models_owner_default_idx`,
		`DROP INDEX assistant_models_visibility_idx`,
		`ALTER TABLE assistant_models DROP COLUMN is_shared`,
		`ALTER TABLE assistant_models DROP COLUMN owner_user_id`,
		`PRAGMA user_version=24`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 24 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 24: %v", err)
	}
	defer migrated.Close()
	var version, shared int
	var owner string
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	if err := migrated.QueryRow(`SELECT owner_user_id, is_shared FROM assistant_models WHERE id = 'model'`).Scan(&owner, &shared); err != nil {
		t.Fatal(err)
	}
	if owner != "owner" || shared != 0 {
		t.Fatalf("migrated model visibility = owner %q shared %d", owner, shared)
	}
}

func TestOpenDatabaseMigratesSchema25ModelConnectionStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO assistant_models (id, owner_user_id, name, provider, model, endpoint, credential_configured, is_default, created_at, updated_at, updated_by_user_id) VALUES ('model', 'owner', 'Model', 'openai-compatible', 'model', 'http://localhost', 1, 1, 1, 1, 'owner')`,
		`ALTER TABLE assistant_models DROP COLUMN connection_ok`,
		`PRAGMA user_version=25`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 25 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 25: %v", err)
	}
	defer migrated.Close()
	var version, connectionOK int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	if err := migrated.QueryRow(`SELECT connection_ok FROM assistant_models WHERE id = 'model'`).Scan(&connectionOK); err != nil || connectionOK != 0 {
		t.Fatalf("migrated connection status = %d, error = %v", connectionOK, err)
	}
}

func TestOpenDatabaseMigratesSchema26ExternalInterfaceTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE external_trigger_requests`,
		`DROP TABLE external_trigger_entries`,
		`DROP TABLE external_trigger_keys`,
		`PRAGMA user_version=26`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 26 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 26: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	for _, table := range []string{"external_trigger_keys", "external_trigger_entries", "external_trigger_requests", "external_trigger_control", "external_trigger_nonces"} {
		var count int
		if err := migrated.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count = %d, error = %v", table, count, err)
		}
	}
}

func TestOpenDatabaseMigratesSchema38ExternalSignatureProtection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE external_trigger_entries DROP COLUMN require_signature`,
		`DROP TABLE external_trigger_nonces`,
		`PRAGMA user_version=38`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 38 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 38: %v", err)
	}
	defer migrated.Close()
	var version, nonceTable int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='external_trigger_nonces'`).Scan(&nonceTable); err != nil || nonceTable != 1 {
		t.Fatalf("nonce table count=%d error=%v", nonceTable, err)
	}
	if _, err := migrated.Exec(`INSERT INTO external_trigger_keys
		(id, label, token_hash, token_hint, enabled, created_at, updated_at)
		VALUES ('key', 'Key', 'hash', 'hint', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrated.Exec(`INSERT INTO external_trigger_entries
		(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
		VALUES ('entry', 'key', 'log', 'Log', 'log', '', '{}', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var requireSignature int
	if err := migrated.QueryRow(`SELECT require_signature FROM external_trigger_entries WHERE id='entry'`).Scan(&requireSignature); err != nil || requireSignature != 0 {
		t.Fatalf("migrated signature requirement=%d error=%v", requireSignature, err)
	}
}

func TestOpenDatabaseMigratesSchema39SessionAuthenticationAssurance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE sessions DROP COLUMN authentication_assurance`,
		`ALTER TABLE sessions DROP COLUMN reauthenticated_at`,
		`PRAGMA user_version=39`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare schema 39 with %q: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 39: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	for _, column := range []string{"authentication_assurance", "reauthenticated_at"} {
		var count int
		if err := migrated.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = ?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("column %s count=%d err=%v", column, count, err)
		}
	}
}

func TestOpenDatabaseMigratesSchema40AuditCorrelationMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE audit_events DROP COLUMN request_id`,
		`ALTER TABLE audit_events DROP COLUMN authentication_assurance`,
		`PRAGMA user_version=40`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare schema 40 with %q: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 40: %v", err)
	}
	defer migrated.Close()
	for _, column := range []string{"request_id", "authentication_assurance"} {
		var count int
		if err := migrated.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('audit_events') WHERE name = ?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("column %s count=%d err=%v", column, count, err)
		}
	}
}

func TestOpenDatabaseRollsBackSchema41MFAEnrollmentOnInjectedFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, enabled, auth_version, mfa_required_at, created_at, updated_at)
			VALUES ('administrator', 'admin', 'preserve-hash', 'administrator', 1, 1, 0, 1, 1)`,
		`ALTER TABLE users DROP COLUMN mfa_required_at`,
		`CREATE TRIGGER inject_mfa_enrollment_failure
			BEFORE UPDATE OF mfa_required_at ON users
			BEGIN SELECT RAISE(ABORT, 'injected State Root migration failure'); END`,
		`PRAGMA user_version=41`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare schema 41 failure injection with %q: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if migrated != nil {
		_ = migrated.Close()
		t.Fatal("migration unexpectedly succeeded")
	}
	if err == nil || !strings.Contains(err.Error(), "injected State Root migration failure") {
		t.Fatalf("expected injected migration failure, got %v", err)
	}

	unchanged, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer unchanged.Close()
	var version, mfaColumn int
	if err := unchanged.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 41 {
		t.Fatalf("schema version=%d, want rollback to 41", version)
	}
	if err := unchanged.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='mfa_required_at'`).Scan(&mfaColumn); err != nil {
		t.Fatal(err)
	}
	if mfaColumn != 0 {
		t.Fatal("mfa_required_at survived the failed transactional migration")
	}
	var username, role string
	if err := unchanged.QueryRow(`SELECT username, role FROM users WHERE id='administrator'`).Scan(&username, &role); err != nil {
		t.Fatalf("read preserved administrator: %v", err)
	}
	if username != "admin" || role != "administrator" {
		t.Fatalf("administrator changed to username=%q role=%q", username, role)
	}
}

func TestOpenDatabaseMigratesSchema30MySQLConnectionState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE mysql_instances DROP COLUMN connection_state`,
		`PRAGMA user_version=30`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare schema 30 with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 30: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("version = %d, error = %v", version, err)
	}
	var connectionState string
	if _, err := migrated.Exec(`INSERT INTO mysql_instances
		(id, name, host, port, username, tls_mode, ca_path, credential_configured, created_at, updated_at)
		VALUES ('instance', 'Instance', 'localhost', 3306, 'root', 'preferred', '', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := migrated.QueryRow(`SELECT connection_state FROM mysql_instances WHERE id='instance'`).Scan(&connectionState); err != nil || connectionState != "untried" {
		t.Fatalf("connection state = %q, error = %v", connectionState, err)
	}
}

func TestOpenDatabaseMigratesSchema31WebsiteMonitorExternalInterfaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE external_trigger_entries;
		CREATE TABLE external_trigger_entries (
			id TEXT PRIMARY KEY,
			key_id TEXT NOT NULL REFERENCES external_trigger_keys(id) ON DELETE CASCADE,
			name TEXT NOT NULL, label TEXT NOT NULL,
			action_type TEXT NOT NULL CHECK (action_type IN ('log', 'upload', 'quick_run', 'variable')),
			target TEXT NOT NULL DEFAULT '', config_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			UNIQUE (key_id, name)
		);
		PRAGMA user_version=31; PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 31: %v", err)
	}
	defer migrated.Close()
	if _, err := migrated.Exec(`INSERT INTO external_trigger_keys
		(id, label, token_hash, token_hint, enabled, created_at, updated_at)
		VALUES ('key', 'Key', 'hash', 'hint', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrated.Exec(`INSERT INTO external_trigger_entries
		(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
		VALUES ('entry', 'key', 'websites', 'Websites', 'website_monitor', '', '{}', 1, 1, 1)`); err != nil {
		t.Fatalf("new action type rejected after migration: %v", err)
	}
	for _, table := range []string{"external_trigger_entries", "website_monitor_remote_sources"} {
		var count int
		if err := migrated.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d error=%v", table, count, err)
		}
	}
}

func TestFileOperationCommitRegistersRecoverableSourceTrash(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	trashRoot := filepath.Join(root, ".scriptboard-trash")
	if err := os.Mkdir(trashRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	trashPath := filepath.Join(trashRoot, "move-operation")
	if err := os.WriteFile(trashPath, []byte("moved source"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := hostfiles.FileOperation{
		ID: "move-operation", Kind: "cross_filesystem_move",
		SourcePath: filepath.Join(root, "source.txt"), SourcePathKey: hostfiles.ComparisonKey(filepath.Join(root, "source.txt")),
		DestinationPath: filepath.Join(root, "destination.txt"), DestinationPathKey: hostfiles.ComparisonKey(filepath.Join(root, "destination.txt")),
		TrashPath: trashPath, Phase: hostfiles.OperationSourceTrashed, CreatedAt: now, UpdatedAt: now,
	}
	store := newSQLiteFileOperationStore(db)
	if err := store.Create(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at)
		VALUES ('quick-moved', 'Moved', ?, ?, '', 0, 1, 1)`, operation.SourcePath, operation.SourcePathKey); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedules
		(id, name, script_path, script_path_key, arguments_template, expression, timeout_seconds, enabled,
		 allow_overlap, next_fire_at, created_at, updated_at)
		VALUES ('schedule-moved', 'Moved', ?, ?, '', '* * * * *', 0, 1, 0, 1, 1, 1)`, operation.SourcePath, operation.SourcePathKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), operation); err != nil {
		t.Fatalf("repeat file operation commit should be idempotent: %v", err)
	}

	var originalPath, storedPath string
	if err := db.QueryRow("SELECT original_path, stored_path FROM trash_entries WHERE id = ?", operation.ID).Scan(&originalPath, &storedPath); err != nil {
		t.Fatalf("cross-filesystem source trash was not registered: %v", err)
	}
	if originalPath != operation.SourcePath || storedPath != operation.TrashPath {
		t.Fatalf("trash entry = %q -> %q", originalPath, storedPath)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM trash_entries WHERE id = ?", operation.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotent commit trash row count = %d, error = %v", count, err)
	}
	for _, reference := range []struct{ table, id string }{{"quick_runs", "quick-moved"}, {"schedules", "schedule-moved"}} {
		var path, key string
		if err := db.QueryRow("SELECT script_path, script_path_key FROM "+reference.table+" WHERE id = ?", reference.id).Scan(&path, &key); err != nil {
			t.Fatalf("read moved %s reference: %v", reference.table, err)
		}
		if path != operation.DestinationPath || key != operation.DestinationPathKey {
			t.Fatalf("%s reference = %q (%q), want %q (%q)", reference.table, path, key, operation.DestinationPath, operation.DestinationPathKey)
		}
	}
	var quickRevision int64
	if err := db.QueryRow("SELECT revision FROM quick_runs WHERE id = 'quick-moved'").Scan(&quickRevision); err != nil || quickRevision != 2 {
		t.Fatalf("moved Quick Run revision = %d, error = %v", quickRevision, err)
	}
}

func TestOpenDatabaseRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = openDatabase(path)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("incompatible with schema %d", currentSchemaVersion)) || !strings.Contains(err.Error(), "new State Root") {
		t.Fatalf("expected newer-schema rejection, got %v", err)
	}
}

func TestOpenDatabaseCreatesIndexesForPeriodicAndTimeOrderedQueries(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name  string
		index string
		query string
	}{
		{
			name:  "next enabled schedule",
			index: "schedules_due_idx",
			query: `SELECT MIN(next_fire_at) FROM schedules WHERE enabled = 1 AND deleted = 0`,
		},
		{
			name:  "newest runs",
			index: "runs_created_idx",
			query: `SELECT id FROM runs ORDER BY created_at DESC LIMIT 50`,
		},
		{
			name:  "newest audit events",
			index: "audit_events_occurred_idx",
			query: `SELECT id FROM audit_events ORDER BY occurred_at DESC LIMIT 50`,
		},
		{
			name:  "newest trash entries",
			index: "trash_entries_deleted_idx",
			query: `SELECT id FROM trash_entries ORDER BY deleted_at DESC LIMIT 50`,
		},
		{
			name:  "latest trigger for schedule",
			index: "schedule_triggers_schedule_time_idx",
			query: `SELECT result FROM schedule_triggers WHERE schedule_id = 'schedule-1' ORDER BY scheduled_for DESC LIMIT 1`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows, err := db.Query("EXPLAIN QUERY PLAN " + test.query)
			if err != nil {
				t.Fatal(err)
			}
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				plan.WriteString(detail)
				plan.WriteByte('\n')
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.String(), test.index) {
				t.Fatalf("query plan does not use %s:\n%s", test.index, plan.String())
			}
		})
	}
}

func TestOpenDatabaseMarksUnsupervisedRunDisconnected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO runs (id, script_path, script_path_key, script_sha256, arguments_template, arguments_json, executor, source_type, status, created_at, error, log_path) VALUES ('run-1', 'C:\\jobs\\job.cmd', 'c:\\jobs\\job.cmd', 'digest', '', '[]', 'cmd.exe', 'manual', 'running', 1, '', 'runs/run-1.jsonl')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db, err = openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, message string
	var finishedAt sql.NullInt64
	if err := db.QueryRow("SELECT status, error, finished_at FROM runs WHERE id = 'run-1'").Scan(&status, &message, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "disconnected" || message == "" || !finishedAt.Valid {
		t.Fatalf("status=%q message=%q finished_at=%v", status, message, finishedAt)
	}
}
