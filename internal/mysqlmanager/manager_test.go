package mysqlmanager

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSaveInstanceKeepsPasswordOutOfSQLite(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)

	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{
		Name: "Production", Host: "db.internal", Port: 3306, Username: "scriptboard",
		Password: "never-store-this-in-sqlite", TLSMode: TLSPreferred,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !instance.CredentialConfigured || instance.Password != "" {
		t.Fatalf("instance leaked or lost credential state: %+v", instance)
	}

	databaseBytes, err := os.ReadFile(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(databaseBytes), "never-store-this-in-sqlite") {
		t.Fatal("database contains plaintext MySQL password")
	}
	password, err := manager.instancePassword(instance.ID)
	if err != nil || password != "never-store-this-in-sqlite" {
		t.Fatalf("password round trip = %q, %v", password, err)
	}
	secretBytes, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "mysql-credentials.v2.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secretBytes), "never-store-this-in-sqlite") {
		t.Fatal("credential file contains plaintext MySQL password")
	}
	changed := instance
	changed.Host = "attacker-controlled.internal"
	if _, err := manager.secrets.getForInstance(changed); err == nil {
		t.Fatal("credential binding allowed an instance endpoint substitution")
	}
}

func TestManagedBackendOwnsCredentialAndDatabaseCapabilities(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	backend := &recordingBackend{}
	manager, err := New(Options{DB: database, StateRoot: stateRoot, Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{
		Name: "Managed", Host: "db.internal", Port: 3306, Username: "scriptboard",
		Password: "broker-only-secret", TLSMode: TLSRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if backend.storedID != instance.ID || backend.storedPassword != "broker-only-secret" {
		t.Fatalf("credential was not delegated to managed backend: %+v", backend)
	}
	if _, err := manager.TestInstance(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	if backend.testedID != instance.ID {
		t.Fatalf("database connection was not delegated to managed backend: %+v", backend)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "secrets", "mysql-credentials.v2.enc")); !os.IsNotExist(err) {
		t.Fatalf("managed Web initialized a local MySQL credential store: %v", err)
	}
	if manager.runner != nil || manager.server != nil || manager.secrets.vault != nil {
		t.Fatal("managed Web retained a local MySQL secret, process, or database capability")
	}
	if bytes.Contains(mustReadFile(t, filepath.Join(stateRoot, "app.db")), []byte("broker-only-secret")) {
		t.Fatal("managed Web database contains plaintext MySQL password")
	}
}

type recordingBackend struct {
	storedID, storedPassword, testedID string
}

func (backend *recordingBackend) StoreCredential(_ context.Context, instance Instance, password string) error {
	backend.storedID, backend.storedPassword = instance.ID, password
	return nil
}
func (*recordingBackend) DeleteCredential(context.Context, string) error { return nil }
func (backend *recordingBackend) Test(_ context.Context, instance Instance) (ConnectionTest, error) {
	backend.testedID = instance.ID
	return ConnectionTest{OK: true}, nil
}
func (*recordingBackend) Databases(context.Context, Instance) ([]Database, error) {
	return nil, nil
}
func (*recordingBackend) Status(context.Context, Instance) (Status, error) { return Status{}, nil }
func (*recordingBackend) DatabaseExists(context.Context, Instance, string) (bool, error) {
	return false, nil
}
func (*recordingBackend) CreateDatabase(context.Context, Instance, CreateDatabaseInput) error {
	return nil
}
func (*recordingBackend) ReplaceDatabase(context.Context, Instance, string) error { return nil }
func (*recordingBackend) DropDatabase(context.Context, Instance, string) error    { return nil }
func (*recordingBackend) Dump(context.Context, Instance, string, string) (DumpResult, error) {
	return DumpResult{}, nil
}
func (*recordingBackend) Import(context.Context, Instance, string, string) error { return nil }
func (*recordingBackend) Tools() ToolSettings                                    { return ToolSettings{} }
func (*recordingBackend) SetTools(context.Context, ToolSettings) error           { return nil }
func (*recordingBackend) TestTools(context.Context) ToolStatus                   { return ToolStatus{} }

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestManagerMigratesStateRootMySQLKeyToExternalSealedStore(t *testing.T) {
	stateRoot := t.TempDir()
	secretsDirectory := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(secretsDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(map[string][]byte{
		"instance-one": gcm.Seal(nonce, nonce, []byte("legacy-mysql-secret"), []byte("instance-one")),
	})
	legacyKeyPath := filepath.Join(secretsDirectory, "mysql-credentials.key")
	legacyDataPath := filepath.Join(secretsDirectory, "mysql-credentials.enc")
	if err := os.WriteFile(legacyKeyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyDataPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	_, err = database.Exec(`INSERT INTO mysql_instances
		(id,name,host,port,username,tls_mode,ca_path,credential_configured,connection_state,created_at,updated_at)
		VALUES ('instance-one','Legacy','legacy.internal',3306,'legacy-user','preferred','',1,'untried',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	password, err := manager.instancePassword("instance-one")
	if err != nil || password != "legacy-mysql-secret" {
		t.Fatalf("migrated password=%q err=%v", password, err)
	}
	for _, path := range []string{legacyKeyPath, legacyDataPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy credential material remained at %s: %v", path, err)
		}
	}
	sealed, err := os.ReadFile(filepath.Join(secretsDirectory, "mysql-credentials.v2.enc"))
	if err != nil || bytes.Contains(sealed, []byte("legacy-mysql-secret")) {
		t.Fatalf("sealed migration output invalid: err=%v body=%s", err, sealed)
	}
}

type recordingRunner struct {
	executable string
	args       []string
	output     string
	err        error
}

func (runner *recordingRunner) Run(_ context.Context, executable string, args []string, _ io.Reader, stdout, _ io.Writer) error {
	runner.executable = executable
	runner.args = append([]string(nil), args...)
	if runner.err == nil {
		_, _ = io.Copy(stdout, bytes.NewBufferString(runner.output))
	}
	return runner.err
}

type restoreRunner struct {
	dumpOutput   string
	imports      []string
	failImportAt int
}

func (runner *restoreRunner) Run(_ context.Context, executable string, _ []string, stdin io.Reader, stdout, _ io.Writer) error {
	if executable == "mysqldump" {
		_, _ = io.WriteString(stdout, runner.dumpOutput)
		return nil
	}
	body, _ := io.ReadAll(stdin)
	runner.imports = append(runner.imports, string(body))
	if len(runner.imports) == runner.failImportAt {
		return io.ErrUnexpectedEOF
	}
	return nil
}

type fakeServer struct {
	exists       bool
	replacements int
	drops        int
	testResult   ConnectionTest
	testErr      error
	statusErr    error
}

func (server *fakeServer) Test(context.Context, Instance, string) (ConnectionTest, error) {
	if server.testResult == (ConnectionTest{}) && server.testErr == nil {
		return ConnectionTest{OK: true}, nil
	}
	return server.testResult, server.testErr
}
func (server *fakeServer) Databases(context.Context, Instance, string) ([]Database, error) {
	return nil, nil
}
func (server *fakeServer) Status(context.Context, Instance, string) (Status, error) {
	return Status{}, server.statusErr
}
func (server *fakeServer) DatabaseExists(context.Context, Instance, string, string) (bool, error) {
	return server.exists, nil
}
func (server *fakeServer) CreateDatabase(context.Context, Instance, string, CreateDatabaseInput) error {
	return nil
}
func (server *fakeServer) ReplaceDatabase(context.Context, Instance, string, string) error {
	server.replacements++
	return nil
}
func (server *fakeServer) DropDatabase(context.Context, Instance, string, string) error {
	server.drops++
	return nil
}
func (server *fakeServer) NonTransactionalTables(context.Context, Instance, string, string) ([]string, error) {
	return nil, nil
}

func TestInstanceConnectionStatePersistsAndResetsAfterEditing(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{
		Name: "Production", Host: "db.internal", Port: 3306, Username: "scriptboard", Password: "secret", TLSMode: TLSPreferred,
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.ConnectionState != ConnectionUntried {
		t.Fatalf("new connection state = %q, want %q", instance.ConnectionState, ConnectionUntried)
	}

	manager.server = &fakeServer{}
	if _, err := manager.TestInstance(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	connected, err := manager.Instance(context.Background(), instance.ID)
	if err != nil || connected.ConnectionState != ConnectionConnected {
		t.Fatalf("connected state = %q, error = %v", connected.ConnectionState, err)
	}
	renamed, err := manager.SaveInstance(context.Background(), InstanceInput{
		ID: instance.ID, Name: "Renamed production", Host: instance.Host, Port: instance.Port, Username: instance.Username, TLSMode: instance.TLSMode,
	})
	if err != nil || renamed.ConnectionState != ConnectionConnected {
		t.Fatalf("state after display-name edit = %q, error = %v", renamed.ConnectionState, err)
	}

	manager.server = &fakeServer{statusErr: errors.New("connection refused")}
	if _, err := manager.Status(context.Background(), instance.ID); err == nil {
		t.Fatal("failed status probe unexpectedly succeeded")
	}
	reloaded, err := manager.Instance(context.Background(), instance.ID)
	if err != nil || reloaded.ConnectionState != ConnectionFailed {
		t.Fatalf("persisted failed state = %q, error = %v", reloaded.ConnectionState, err)
	}

	edited, err := manager.SaveInstance(context.Background(), InstanceInput{
		ID: instance.ID, Name: instance.Name, Host: "new-db.internal", Port: instance.Port, Username: instance.Username, TLSMode: instance.TLSMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.ConnectionState != ConnectionUntried {
		t.Fatalf("edited connection state = %q, want %q", edited.ConnectionState, ConnectionUntried)
	}
}

func TestRestoreFailureAutomaticallyRollsBackSafetyBackup(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeServer{exists: true}
	manager.server = server
	initialRunner := &recordingRunner{output: "CREATE TABLE wanted (id INT);\n"}
	manager.runner = initialRunner
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{
		Name: "Production", Host: "db.internal", Port: 3306, Username: "scriptboard", Password: "secret", TLSMode: TLSPreferred,
	})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := manager.Backup(context.Background(), BackupRequest{InstanceID: instance.ID, Database: "inventory", Kind: BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	runner := &restoreRunner{dumpOutput: "CREATE TABLE original (id INT);\n", failImportAt: 1}
	manager.runner = runner
	operation, err := manager.Restore(context.Background(), RestoreRequest{
		InstanceID: instance.ID, BackupID: desired.ID, TargetDatabase: "inventory", Actor: Actor{UserID: "admin-1", Username: "admin"},
	})
	if err == nil {
		t.Fatal("restore unexpectedly succeeded")
	}
	if operation.Phase != "rolled_back" || operation.SafetyBackupID == "" || operation.RollbackError != "" {
		t.Fatalf("restore operation = %+v", operation)
	}
	if server.replacements != 2 {
		t.Fatalf("database replacements = %d, want restore and rollback", server.replacements)
	}
	if len(runner.imports) != 2 || !strings.Contains(runner.imports[0], "wanted") || !strings.Contains(runner.imports[1], "original") {
		t.Fatalf("restore imports = %#v", runner.imports)
	}
}

func TestRestoreRejectsCorruptedBackupBeforeReplacingDatabase(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeServer{exists: true}
	manager.server = server
	manager.runner = &recordingRunner{output: "CREATE TABLE wanted (id INT);\n"}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "Production", Host: "localhost", Port: 3306, Username: "admin", Password: "secret", TLSMode: TLSPreferred})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := manager.Backup(context.Background(), BackupRequest{InstanceID: instance.ID, Database: "inventory", Kind: BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(backup.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("corruption")
	_ = file.Close()
	operation, err := manager.Restore(context.Background(), RestoreRequest{InstanceID: instance.ID, BackupID: backup.ID, TargetDatabase: "inventory"})
	if err == nil || operation.Phase != "failed" {
		t.Fatalf("corrupt restore operation=%+v error=%v", operation, err)
	}
	if server.replacements != 0 {
		t.Fatalf("database was replaced before verification: %d", server.replacements)
	}
}

func TestImportRejectsCorruptGzipAndRedactsCommandErrors(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "Import", Host: "localhost", Port: 3306, Username: "admin", Password: "highly-sensitive", TLSMode: TLSPreferred})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportBackup(context.Background(), ImportRequest{InstanceID: instance.ID, Database: "inventory", Filename: "broken.sql.gz", Reader: strings.NewReader("not-gzip")}); err == nil {
		t.Fatal("corrupt gzip import unexpectedly succeeded")
	}
	redacted := sanitizedCommandError("access denied password=highly-sensitive", "highly-sensitive")
	if strings.Contains(redacted, "highly-sensitive") || !strings.Contains(redacted, "[REDACTED]") {
		t.Fatalf("command error was not redacted: %q", redacted)
	}
}

func TestMySQLImportArgumentsDisableLocalClientCommands(t *testing.T) {
	arguments := mysqlImportArguments("C:/private/client.cnf", "inventory")
	want := []string{
		"--defaults-extra-file=C:/private/client.cnf",
		"--binary-mode", "--batch", "--skip-reconnect", "--default-character-set=utf8mb4", "--", "inventory",
	}
	if strings.Join(arguments, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("import arguments = %#v, want %#v", arguments, want)
	}
}

func TestValidateGzipSQLRejectsExpansionBeyondLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "expansion.sql.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	if _, err := compressed.Write(bytes.Repeat([]byte("A"), 2048)); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := validateGzipSQL(file, 1024); err == nil || !strings.Contains(err.Error(), "expanded size") {
		t.Fatalf("gzip expansion error = %v", err)
	}
}

func TestBackupRecordsCanBeFilteredByDatabase(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "Filter", Host: "localhost", Port: 3306, Username: "admin", Password: "secret", TLSMode: TLSPreferred})
	if err != nil {
		t.Fatal(err)
	}
	for index, databaseName := range []string{"inventory", "reporting", "inventory"} {
		_, err := manager.ImportBackup(context.Background(), ImportRequest{
			InstanceID: instance.ID,
			Database:   databaseName,
			Filename:   databaseName + ".sql",
			Reader:     strings.NewReader("CREATE TABLE filtered_" + databaseName + "_" + string(rune('a'+index)) + " (id INT);\n"),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	databases, err := manager.BackupDatabases(context.Background(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(databases, ",") != "inventory,reporting" {
		t.Fatalf("backup databases = %v", databases)
	}
	backups, total, err := manager.BackupsPage(context.Background(), instance.ID, "inventory", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(backups) != 2 {
		t.Fatalf("filtered backups total=%d rows=%+v", total, backups)
	}
	for _, backup := range backups {
		if backup.Database != "inventory" {
			t.Fatalf("filter returned database %q", backup.Database)
		}
	}
}

func TestMySQLValidationBoundaries(t *testing.T) {
	for _, name := range []string{"mysql", " INFORMATION_SCHEMA ", "Performance_Schema", "sys"} {
		if !IsSystemDatabase(name) {
			t.Fatalf("system database accepted: %q", name)
		}
	}
	if got := quoteIdentifier("inventory`archive"); got != "`inventory``archive`" {
		t.Fatalf("quoted identifier = %q", got)
	}
	for _, mode := range []TLSMode{TLSDisabled, TLSPreferred, TLSRequired, TLSVerifyIdentity} {
		if !validTLSMode(mode) {
			t.Fatalf("valid TLS mode rejected: %q", mode)
		}
	}
	if validTLSMode("opportunistic") {
		t.Fatal("unknown TLS mode accepted")
	}
	root := filepath.Join(t.TempDir(), "backups")
	if !pathWithin(root, filepath.Join(root, "instance", "backup.sql.gz")) || pathWithin(root, filepath.Join(root, "..", "escape.sql.gz")) {
		t.Fatal("backup root boundary is incorrect")
	}
	if _, err := planParser.Parse("0 2 * * *"); err != nil {
		t.Fatalf("five-field Cron rejected: %v", err)
	}
	if _, err := planParser.Parse("0 0 2 * * *"); err == nil {
		t.Fatal("six-field Cron unexpectedly accepted")
	}
}

func TestImportAndPlanRetentionNeverDeleteManualBackups(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	manager.server = &fakeServer{}
	manager.runner = &recordingRunner{output: "CREATE TABLE retained (id INT);\n"}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "Retention", Host: "localhost", Port: 3306, Username: "admin", Password: "secret", TLSMode: TLSPreferred})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := manager.ImportBackup(context.Background(), ImportRequest{InstanceID: instance.ID, Database: "inventory", Filename: "manual.sql", Reader: strings.NewReader("CREATE TABLE manual_copy (id INT);\n")})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manager.SavePlan(context.Background(), PlanInput{Name: "Nightly", InstanceID: instance.ID, Databases: []string{"inventory"}, Expression: "0 2 * * *", RetentionCount: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Backup(context.Background(), BackupRequest{InstanceID: instance.ID, Database: "inventory", PlanID: plan.ID, Kind: BackupScheduled})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Backup(context.Background(), BackupRequest{InstanceID: instance.ID, Database: "inventory", PlanID: plan.ID, Kind: BackupScheduled})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.applyRetention(context.Background(), plan.ID, "inventory", 1); err != nil {
		t.Fatal(err)
	}
	backups, err := manager.Backups(context.Background(), instance.ID, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 {
		t.Fatalf("retained backups = %+v", backups)
	}
	if _, err := os.Stat(manual.Path); err != nil {
		t.Fatalf("manual import was removed: %v", err)
	}
	if _, err := os.Stat(second.Path); err != nil {
		t.Fatalf("latest scheduled backup was removed: %v", err)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("old scheduled backup still exists: %v", err)
	}
}

func TestDropDatabaseRequiresAndKeepsSafetyBackup(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeServer{exists: true}
	manager.server = server
	manager.runner = &recordingRunner{output: "CREATE TABLE before_drop (id INT);\n"}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "Drop", Host: "localhost", Port: 3306, Username: "admin", Password: "secret", TLSMode: TLSPreferred})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DropDatabase(context.Background(), DropDatabaseRequest{InstanceID: instance.ID, Database: "inventory", Confirmation: "wrong"}); err == nil {
		t.Fatal("drop accepted an incorrect confirmation")
	}
	operation, err := manager.DropDatabase(context.Background(), DropDatabaseRequest{InstanceID: instance.ID, Database: "inventory", Confirmation: "inventory"})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != "completed" || operation.SafetyBackupID == "" || server.drops != 1 {
		t.Fatalf("drop operation=%+v drops=%d", operation, server.drops)
	}
	safety, err := manager.BackupByID(context.Background(), operation.SafetyBackupID)
	if err != nil || safety.Kind != BackupSafety {
		t.Fatalf("safety backup=%+v error=%v", safety, err)
	}
}

func TestBackupCreatesVerifiedCompressedArtifactWithoutPasswordArguments(t *testing.T) {
	stateRoot := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	applyTestSchema(t, database)
	runner := &recordingRunner{output: "CREATE TABLE widgets (id INT);\nINSERT INTO widgets VALUES (1);\n"}
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	manager.server = &fakeServer{}
	manager.runner = runner
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{
		Name: "Production", Host: "db.internal", Port: 3306, Username: "scriptboard",
		Password: "cli-password-must-stay-private", TLSMode: TLSPreferred,
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := manager.Backup(context.Background(), BackupRequest{
		InstanceID: instance.ID, Database: "inventory", Kind: BackupManual,
		ActorUserID: "admin-1", ActorUsername: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if backup.SizeBytes == 0 || len(backup.SHA256) != 64 || !strings.HasSuffix(backup.Path, ".sql.gz") {
		t.Fatalf("backup metadata = %+v", backup)
	}
	if runner.executable != "mysqldump" || !containsArgument(runner.args, "--single-transaction") ||
		!containsArgument(runner.args, "--routines") || !containsArgument(runner.args, "inventory") {
		t.Fatalf("mysqldump invocation = %q %q", runner.executable, runner.args)
	}
	if strings.Contains(strings.Join(runner.args, " "), "cli-password-must-stay-private") {
		t.Fatal("password leaked into process arguments")
	}
	plain, err := readGzipFile(backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != runner.output {
		t.Fatalf("backup content = %q", plain)
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM mysql_backups WHERE id=?", backup.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup row count=%d error=%v", count, err)
	}
}

func applyTestSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range SchemaStatements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("apply schema: %v\n%s", err, statement)
		}
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func readGzipFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
