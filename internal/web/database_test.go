package web

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

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
	for _, table := range []string{"file_operations", "file_quick_access_pins", "trash_entries", "external_trigger_keys", "external_trigger_entries", "external_trigger_requests", "application_versions", "kubernetes_connection", "kubernetes_versions", "kubernetes_metric_minutes"} {
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

func TestOpenDatabaseMigratesSchema52WithoutLegacyMFADeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	var deadlineColumn int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='mfa_required_at'`).Scan(&deadlineColumn); err != nil {
		t.Fatal(err)
	}
	if deadlineColumn == 0 {
		if _, err := database.Exec(`ALTER TABLE users ADD COLUMN mfa_required_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`PRAGMA user_version=52; PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 52: %v", err)
	}
	defer migrated.Close()
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='mfa_required_at'`).Scan(&deadlineColumn); err != nil {
		t.Fatal(err)
	}
	if deadlineColumn != 0 {
		t.Fatal("legacy MFA enrollment deadline column survived schema 52 migration")
	}
}

func TestOpenDatabaseMigratesSchema42AuditResourceIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE audit_events DROP COLUMN resource_revision`,
		`ALTER TABLE audit_events DROP COLUMN resource_digest_sha256`,
		`PRAGMA user_version=42`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare schema 42 with %q: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate schema 42: %v", err)
	}
	defer migrated.Close()
	for _, column := range []string{"resource_revision", "resource_digest_sha256"} {
		var count int
		if err := migrated.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('audit_events') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("column %s count=%d err=%v", column, count, err)
		}
	}
}

func TestOpenDatabaseMigratesSecuritySchema43WithDevSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE instance_settings`,
		`DROP TABLE kubernetes_connection`,
		`DROP TABLE kubernetes_versions`,
		`DROP TABLE kubernetes_metric_minutes`,
		`PRAGMA user_version=43`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare security schema 43 with %q: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate security schema 43: %v", err)
	}
	defer migrated.Close()
	for _, table := range []string{"instance_settings", "kubernetes_connection", "kubernetes_versions", "kubernetes_metric_minutes"} {
		var count int
		if err := migrated.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("dev-line table %s count=%d err=%v", table, count, err)
		}
	}
}

func TestOpenDatabaseMigratesSingleKubernetesConnectionToConnectionScopedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE kubernetes_metric_minutes`,
		`DROP TABLE kubernetes_versions`,
		`DROP TABLE kubernetes_connection`,
		`CREATE TABLE kubernetes_connection (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1), name TEXT NOT NULL, kubeconfig_path TEXT NOT NULL,
			context_name TEXT NOT NULL DEFAULT '', operation_mode TEXT NOT NULL, fingerprint TEXT NOT NULL DEFAULT '',
			capabilities_json TEXT NOT NULL DEFAULT '{}', last_tested_at INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL)`,
		`CREATE TABLE kubernetes_versions (
			workload_key TEXT NOT NULL, observed_at INTEGER NOT NULL, image TEXT NOT NULL, revision TEXT NOT NULL,
			PRIMARY KEY (workload_key, observed_at))`,
		`CREATE TABLE kubernetes_metric_minutes (
			workload_key TEXT NOT NULL, bucket_at INTEGER NOT NULL, cpu_millicores INTEGER NOT NULL, memory_bytes INTEGER NOT NULL,
			ready INTEGER NOT NULL, desired INTEGER NOT NULL, restarts INTEGER NOT NULL, PRIMARY KEY (workload_key, bucket_at))`,
		`INSERT INTO kubernetes_connection VALUES (1,'production','/etc/kubeconfig','production','observe','sha256:prod','{}',1,'',2)`,
		`INSERT INTO kubernetes_versions VALUES ('default/Deployment/api',3,'api:v1','1')`,
		`INSERT INTO kubernetes_metric_minutes VALUES ('default/Deployment/api',4,5,6,1,1,0)`,
		`PRAGMA user_version=46`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare single Kubernetes connection with %q: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate single Kubernetes connection: %v", err)
	}
	defer migrated.Close()
	var connectionID, name string
	if err := migrated.QueryRow(`SELECT id,name FROM kubernetes_connection`).Scan(&connectionID, &name); err != nil {
		t.Fatal(err)
	}
	if connectionID == "" || name != "production" {
		t.Fatalf("migrated connection id=%q name=%q", connectionID, name)
	}
	for _, table := range []string{"kubernetes_versions", "kubernetes_metric_minutes"} {
		var historyConnectionID string
		if err := migrated.QueryRow(`SELECT connection_id FROM ` + table).Scan(&historyConnectionID); err != nil {
			t.Fatalf("read %s connection: %v", table, err)
		}
		if historyConnectionID != connectionID {
			t.Fatalf("%s connection=%q want %q", table, historyConnectionID, connectionID)
		}
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

func TestOpenDatabaseRetiresSchema31WebsiteMonitorExternalInterfaces(t *testing.T) {
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
	var remoteSources int
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='website_monitor_remote_sources'`).Scan(&remoteSources); err != nil || remoteSources != 0 {
		t.Fatalf("retired remote source table count=%d error=%v", remoteSources, err)
	}
}

func TestRetireRemoteWebsiteFeatureRemovesRowsMetadataAndDedicatedSecret(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "retire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE external_trigger_entries (action_type TEXT NOT NULL);
		INSERT INTO external_trigger_entries VALUES ('website_monitor'), ('quick_run');
		CREATE TABLE website_monitor_remote_sources (id TEXT PRIMARY KEY);
		INSERT INTO website_monitor_remote_sources VALUES ('branch')`); err != nil {
		t.Fatal(err)
	}
	secretDirectory := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(secretDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(secretDirectory, "remote-website-connections.enc")
	if err := os.WriteFile(secretPath, []byte("retired ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	retiredIDs, err := retireRemoteWebsiteFeature(database, stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(retiredIDs) != 1 || retiredIDs[0] != "branch" {
		t.Fatalf("retired source IDs=%v", retiredIDs)
	}
	var remoteEntries, remoteTable int
	if err := database.QueryRow(`SELECT COUNT(*) FROM external_trigger_entries WHERE action_type='website_monitor'`).Scan(&remoteEntries); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='website_monitor_remote_sources'`).Scan(&remoteTable); err != nil {
		t.Fatal(err)
	}
	if remoteEntries != 0 || remoteTable != 0 {
		t.Fatalf("retired rows=%d table=%d", remoteEntries, remoteTable)
	}
	if _, err := os.Stat(secretPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dedicated remote website secret still exists: %v", err)
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

func TestOpenDatabaseMigratesInstanceSettingsFromSchema35(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE instance_settings; PRAGMA user_version=35; PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if _, err := migrated.Exec(`INSERT INTO instance_settings
		(singleton, display_name, updated_at, updated_by_user_id) VALUES (1, 'Operations', 1, 'admin')`); err != nil {
		t.Fatalf("write migrated instance settings: %v", err)
	}
	var version int
	if err := migrated.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("migrated schema version=%d error=%v, want %d", version, err, currentSchemaVersion)
	}
}

func TestOpenDatabaseAddsRegistryCardsFromSchema36(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		DROP INDEX custom_dashboard_cards_order_idx;
		ALTER TABLE custom_dashboard_cards RENAME TO custom_dashboard_cards_current;
		CREATE TABLE custom_dashboard_cards (
			id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
			name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','percentage','quota','key_value','website')),
			source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
			value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
			config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
			sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
			last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		DROP TABLE custom_dashboard_cards_current;
		CREATE INDEX custom_dashboard_cards_order_idx ON custom_dashboard_cards(dashboard_id, sort_order, created_at);
		PRAGMA user_version=36;
		PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if _, err := migrated.Exec(`INSERT INTO custom_dashboards(id,name,slug,is_public,sort_order,created_at,updated_at) VALUES('dashboard','Images','images',0,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrated.Exec(`INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,sort_order,created_at,updated_at) VALUES('card','dashboard','Registry','registry',1,1,1)`); err != nil {
		t.Fatalf("registry card rejected after migration: %v", err)
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
