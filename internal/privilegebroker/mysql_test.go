package privilegebroker

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/mysqlmanager"
)

func TestMySQLUsesTypedAuthorizedBrokerOperations(t *testing.T) {
	backupRoot := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureMySQLService{artifactRoot: backupRoot}
	authorizer := &fixtureMySQLAuthorizer{actor: Actor{UserID: "administrator", Role: "administrator"}}
	server, err := NewServer(ServerOptions{Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: authorizer, Executor: &fixtureExecutor{},
		MySQL: service})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	backend := NewMySQLBackend(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}), mysqlmanager.ToolSettings{})
	instance := mysqlmanager.Instance{ID: "instance-one", Host: "db.internal", Port: 3306, Username: "operator", TLSMode: mysqlmanager.TLSRequired}
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "mysql-domain-test"})
	if err := backend.StoreCredential(context.Background(), instance, "secret"); err == nil {
		t.Fatal("MySQL credential stored without session authorization")
	}
	if err := backend.StoreCredential(authorized, instance, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Test(authorized, instance); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(backupRoot, "instance-one", "database", "backup.sql.gz")
	if _, err := backend.Dump(authorized, instance, "application", destination); err != nil {
		t.Fatal(err)
	}
	sql := make([]byte, (2<<20)+12345)
	var pseudo uint32 = 0x12345678
	for index := range sql {
		pseudo ^= pseudo << 13
		pseudo ^= pseudo >> 17
		pseudo ^= pseudo << 5
		sql[index] = byte(pseudo)
	}
	artifactPath := filepath.Join(backupRoot, "instance-one", "database", "import.sql.gz")
	artifact, err := backend.StoreArtifact(authorized, artifactPath, bytes.NewReader(sql), false)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(service.artifactContent))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := io.ReadAll(compressed)
	if err != nil || !bytes.Equal(stored, sql) || artifact.SizeBytes != int64(len(service.artifactContent)) || service.artifactPath != artifactPath || service.artifactChunks < 3 {
		t.Fatalf("Broker artifact upload mismatch: bytes=%d err=%v", len(stored), err)
	}
	var downloaded bytes.Buffer
	filename, size, err := backend.DownloadBackup(authorized, "backup-one", &downloaded)
	if err != nil || filename != "fixture.sql.gz" || size != 6 || downloaded.String() != "backup" {
		t.Fatalf("Broker backup download = %q %d %q, %v", filename, size, downloaded.String(), err)
	}
	if service.password != "secret" || service.instance.Host != instance.Host || service.dumps != 1 || service.tests != 1 {
		t.Fatalf("typed MySQL fields were not preserved: %+v", service)
	}
}

func TestBrokerMySQLServicePromotesCombinedBackend(t *testing.T) {
	backend := &fixtureQueryBackend{}
	service := &brokerMySQLService{brokerMySQLBackend: backend}
	instance := mysqlmanager.Instance{ID: "instance-one"}
	if databases, err := service.DatabasesIncludingSystem(context.Background(), instance); err != nil || len(databases) != 1 || databases[0].Name != "system" {
		t.Fatalf("DatabasesIncludingSystem() = %+v, %v", databases, err)
	}
	if objects, err := service.Objects(context.Background(), instance, "application"); err != nil || len(objects) != 1 || objects[0].Name != "player_card" {
		t.Fatalf("Objects() = %+v, %v", objects, err)
	}
	if details, err := service.ObjectDetails(context.Background(), instance, "application", "player_card"); err != nil || details.Object.Name != "player_card" {
		t.Fatalf("ObjectDetails() = %+v, %v", details, err)
	}
	if result, err := service.ExecuteSQL(context.Background(), instance, mysqlmanager.SQLRequest{Database: "application", Statement: "SELECT 1"}); err != nil || result.ReturnedRows != 1 {
		t.Fatalf("ExecuteSQL() = %+v, %v", result, err)
	}
	if strings.Join(backend.calls, ",") != "databases,objects,details,sql" {
		t.Fatalf("query forwarding calls = %v", backend.calls)
	}
}

func TestBrokerCommitsUploadedArtifactAtomically(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "instance", "database", "backup.sql.gz")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte("CREATE TABLE fixture (id INT);\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	service := &brokerMySQLService{}
	cut := compressed.Len() / 2
	if _, err := service.StoreArtifactChunk(context.Background(), path, compressed.Bytes()[:cut], 0, false); err != nil {
		t.Fatal(err)
	}
	result, err := service.StoreArtifactChunk(context.Background(), path, compressed.Bytes()[cut:], int64(cut), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.SizeBytes != int64(compressed.Len()) || len(result.SHA256) != 64 {
		t.Fatalf("artifact result = %+v", result)
	}
	if _, err := os.Stat(path + ".upload.partial"); !os.IsNotExist(err) {
		t.Fatalf("partial artifact remains after commit: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("committed artifact permissions = %v", info.Mode().Perm())
	}
}

func TestBrokerArtifactVerificationRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.sql.gz")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.sql.gz")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	service := &brokerMySQLService{brokerMySQLBackend: &fixtureMySQLService{}}
	if err := service.VerifyArtifact(context.Background(), link, strings.Repeat("0", 64), true); err == nil {
		t.Fatal("Broker accepted a symbolic-link MySQL backup artifact")
	}
}

func TestBrokerArtifactVerificationRejectsNonRegularFile(t *testing.T) {
	service := &brokerMySQLService{brokerMySQLBackend: &fixtureMySQLService{}}
	if err := service.VerifyArtifact(context.Background(), t.TempDir(), strings.Repeat("0", 64), true); err == nil {
		t.Fatal("Broker accepted a non-regular MySQL backup artifact")
	}
}

func TestBrokerPreparesOnlyConfiguredArtifactRoot(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "broker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE mysql_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(t.TempDir(), "configured")
	if _, err := database.Exec("INSERT INTO mysql_settings(key,value) VALUES ('backup_root',?)", configured); err != nil {
		t.Fatal(err)
	}
	service := &brokerMySQLService{brokerMySQLBackend: &fixtureMySQLService{}, db: database, backupRoot: configured}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := service.PrepareArtifactRoot(context.Background(), outside); err == nil {
		t.Fatal("Broker prepared a directory other than the configured MySQL backup root")
	}
}

func TestMySQLFailureResponseSeparatesDatabaseAndHostPermissions(t *testing.T) {
	t.Parallel()

	if response := mysqlFailureResponse(errors.New("mysqldump: Error 1142 command denied")); response.ErrorCode != "mysql_permission_denied" {
		t.Fatalf("database grant failure = %#v", response)
	}
	if response := mysqlFailureResponse(errors.New("open /backups/file: permission denied")); response.ErrorCode != "mysql_artifact_permission_denied" {
		t.Fatalf("artifact permission failure = %#v", response)
	}
}

func TestMySQLRejectsPathsOutsideBackupRootBeforeExecution(t *testing.T) {
	backupRoot := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureMySQLService{artifactRoot: backupRoot}
	authorizer := &fixtureMySQLAuthorizer{actor: Actor{UserID: "administrator", Role: "administrator"}}
	server, err := NewServer(ServerOptions{Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: authorizer, Executor: &fixtureExecutor{},
		MySQL: service})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	backend := NewMySQLBackend(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}), mysqlmanager.ToolSettings{})
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "mysql-path-test"})
	instance := mysqlmanager.Instance{ID: "instance-one", Host: "db.internal", Port: 3306, Username: "operator", TLSMode: mysqlmanager.TLSRequired}
	if _, err := backend.Dump(authorized, instance, "application", filepath.Join(t.TempDir(), "escape.sql.gz")); err == nil {
		t.Fatal("Broker accepted MySQL dump path outside configured backup root")
	}
	if service.dumps != 0 {
		t.Fatal("forbidden artifact path reached MySQL execution backend")
	}
}

func TestMySQLClientCancellationStopsBrokerExecution(t *testing.T) {
	backupRoot := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureMySQLService{artifactRoot: backupRoot, dumpStarted: make(chan struct{}), blockDump: true}
	authorizer := &fixtureMySQLAuthorizer{actor: Actor{UserID: "administrator", Role: "administrator"}}
	server, err := NewServer(ServerOptions{Listener: listener, VerifyPeer: func(net.Conn) error { return nil }, Authorizer: authorizer,
		Executor: &fixtureExecutor{}, MySQL: service})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	backend := NewMySQLBackend(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}), mysqlmanager.ToolSettings{})
	ctx, cancel := context.WithCancel(WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "mysql-cancel-test"}))
	result := make(chan error, 1)
	instance := mysqlmanager.Instance{ID: "instance-one", Host: "db.internal", Port: 3306, Username: "operator", TLSMode: mysqlmanager.TLSRequired}
	path := filepath.Join(backupRoot, "cancel.sql.gz")
	go func() { _, err := backend.Dump(ctx, instance, "application", path); result <- err }()
	<-service.dumpStarted
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled MySQL operation returned success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation did not stop Broker MySQL execution")
	}
}

func TestMySQLProtocolRejectsGenericAndUnrelatedFields(t *testing.T) {
	instance := mysqlmanager.Instance{ID: "instance-one", Host: "db.internal", Port: 3306, Username: "operator", TLSMode: mysqlmanager.TLSRequired}
	valid := wireRequest{Version: ProtocolVersion, Operation: operationMySQLTest, RequestID: "mysql-protocol-test",
		SessionToken: strings.Repeat("s", 32), MySQL: &mysqlWireRequest{Instance: instance}}
	requests := []wireRequest{
		func() wireRequest {
			value := valid
			value.Parameters = json.RawMessage(`{"secret":"no"}`)
			return value
		}(),
		func() wireRequest { value := valid; value.MySQL.Password = "not-allowed"; return value }(),
		func() wireRequest { value := valid; value.SessionToken = ""; return value }(),
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("accepted invalid MySQL request: %+v", request)
		}
	}
}

func TestMySQLExecutableAllowlistRejectsGenericCommand(t *testing.T) {
	if _, err := trustedMySQLExecutable(filepath.Join(t.TempDir(), "powershell.exe"), false); err == nil {
		t.Fatal("Broker accepted a generic executable as the MySQL client")
	}
	if _, err := trustedMySQLExecutable(filepath.Join(t.TempDir(), "cmd.exe"), true); err == nil {
		t.Fatal("Broker accepted a generic executable as mysqldump")
	}
}

type fixtureMySQLService struct {
	instance        mysqlmanager.Instance
	password        string
	tests, dumps    int
	dumpStarted     chan struct{}
	blockDump       bool
	artifactRoot    string
	artifactPath    string
	artifactContent []byte
	artifactChunks  int
}

type fixtureQueryBackend struct {
	mysqlmanager.Backend
	calls []string
}

func (backend *fixtureQueryBackend) DatabasesIncludingSystem(context.Context, mysqlmanager.Instance) ([]mysqlmanager.Database, error) {
	backend.calls = append(backend.calls, "databases")
	return []mysqlmanager.Database{{Name: "system"}}, nil
}

func (backend *fixtureQueryBackend) Objects(context.Context, mysqlmanager.Instance, string) ([]mysqlmanager.DatabaseObject, error) {
	backend.calls = append(backend.calls, "objects")
	return []mysqlmanager.DatabaseObject{{Name: "player_card"}}, nil
}

func (backend *fixtureQueryBackend) ObjectDetails(context.Context, mysqlmanager.Instance, string, string) (mysqlmanager.ObjectDetails, error) {
	backend.calls = append(backend.calls, "details")
	return mysqlmanager.ObjectDetails{Object: mysqlmanager.DatabaseObject{Name: "player_card"}}, nil
}

func (backend *fixtureQueryBackend) ExecuteSQL(context.Context, mysqlmanager.Instance, mysqlmanager.SQLRequest) (mysqlmanager.SQLResult, error) {
	backend.calls = append(backend.calls, "sql")
	return mysqlmanager.SQLResult{ReturnedRows: 1}, nil
}

var _ mysqlmanager.QueryBackend = (*fixtureQueryBackend)(nil)

func (service *fixtureMySQLService) StoreCredential(_ context.Context, instance mysqlmanager.Instance, password string) error {
	service.instance, service.password = instance, password
	return nil
}
func (*fixtureMySQLService) DeleteCredential(context.Context, string) error { return nil }
func (service *fixtureMySQLService) Test(_ context.Context, instance mysqlmanager.Instance) (mysqlmanager.ConnectionTest, error) {
	service.instance, service.tests = instance, service.tests+1
	return mysqlmanager.ConnectionTest{OK: true}, nil
}
func (*fixtureMySQLService) Databases(context.Context, mysqlmanager.Instance) ([]mysqlmanager.Database, error) {
	return nil, nil
}
func (*fixtureMySQLService) DatabasesIncludingSystem(context.Context, mysqlmanager.Instance) ([]mysqlmanager.Database, error) {
	return nil, nil
}
func (*fixtureMySQLService) Objects(context.Context, mysqlmanager.Instance, string) ([]mysqlmanager.DatabaseObject, error) {
	return nil, nil
}
func (*fixtureMySQLService) ObjectDetails(context.Context, mysqlmanager.Instance, string, string) (mysqlmanager.ObjectDetails, error) {
	return mysqlmanager.ObjectDetails{}, nil
}
func (*fixtureMySQLService) ExecuteSQL(context.Context, mysqlmanager.Instance, mysqlmanager.SQLRequest) (mysqlmanager.SQLResult, error) {
	return mysqlmanager.SQLResult{}, nil
}
func (*fixtureMySQLService) Status(context.Context, mysqlmanager.Instance) (mysqlmanager.Status, error) {
	return mysqlmanager.Status{}, nil
}
func (*fixtureMySQLService) DatabaseExists(context.Context, mysqlmanager.Instance, string) (bool, error) {
	return false, nil
}
func (*fixtureMySQLService) CreateDatabase(context.Context, mysqlmanager.Instance, mysqlmanager.CreateDatabaseInput) error {
	return nil
}
func (*fixtureMySQLService) ReplaceDatabase(context.Context, mysqlmanager.Instance, string) error {
	return nil
}
func (*fixtureMySQLService) DropDatabase(context.Context, mysqlmanager.Instance, string) error {
	return nil
}
func (*fixtureMySQLService) ClearDatabase(context.Context, mysqlmanager.Instance, string) error {
	return nil
}
func (service *fixtureMySQLService) Dump(ctx context.Context, instance mysqlmanager.Instance, _, path string) (mysqlmanager.DumpResult, error) {
	service.instance, service.dumps = instance, service.dumps+1
	if service.blockDump {
		close(service.dumpStarted)
		<-ctx.Done()
		return mysqlmanager.DumpResult{}, ctx.Err()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return mysqlmanager.DumpResult{}, err
	}
	return mysqlmanager.DumpResult{}, os.WriteFile(path, []byte("dump"), 0o600)
}
func (*fixtureMySQLService) Import(context.Context, mysqlmanager.Instance, string, string) error {
	return nil
}
func (*fixtureMySQLService) PrepareArtifactRoot(context.Context, string) error { return nil }
func (*fixtureMySQLService) StoreArtifact(context.Context, string, io.Reader, bool) (mysqlmanager.ArtifactResult, error) {
	return mysqlmanager.ArtifactResult{}, nil
}
func (*fixtureMySQLService) VerifyArtifact(context.Context, string, string, bool) error { return nil }
func (*fixtureMySQLService) DeleteArtifact(context.Context, string) error               { return nil }
func (*fixtureMySQLService) CleanupArtifacts(context.Context, string) error             { return nil }
func (*fixtureMySQLService) DownloadBackup(context.Context, string, io.Writer) (string, int64, error) {
	return "fixture.sql.gz", 1, nil
}
func (*fixtureMySQLService) Tools() mysqlmanager.ToolSettings                          { return mysqlmanager.ToolSettings{} }
func (*fixtureMySQLService) SetTools(context.Context, mysqlmanager.ToolSettings) error { return nil }
func (*fixtureMySQLService) TestTools(context.Context) mysqlmanager.ToolStatus {
	return mysqlmanager.ToolStatus{}
}
func (*fixtureMySQLService) ValidateInstance(context.Context, mysqlmanager.Instance) error {
	return nil
}
func (*fixtureMySQLService) ValidateInstanceID(context.Context, string) error { return nil }
func (*fixtureMySQLService) CancelOperation(context.Context, string) error    { return nil }
func (service *fixtureMySQLService) ArtifactRoot(context.Context) (string, error) {
	return service.artifactRoot, nil
}
func (*fixtureMySQLService) ReadBackupChunk(context.Context, string, int64, int) ([]byte, int64, string, error) {
	return []byte("backup"), 6, "fixture.sql.gz", nil
}
func (service *fixtureMySQLService) StoreArtifactChunk(_ context.Context, path string, content []byte, offset int64, final bool) (mysqlmanager.ArtifactResult, error) {
	if int64(len(service.artifactContent)) != offset {
		return mysqlmanager.ArtifactResult{}, errors.New("unexpected artifact offset")
	}
	service.artifactPath = path
	service.artifactContent = append(service.artifactContent, content...)
	service.artifactChunks++
	if !final {
		return mysqlmanager.ArtifactResult{}, nil
	}
	digest := sha256.Sum256(service.artifactContent)
	return mysqlmanager.ArtifactResult{SizeBytes: int64(len(service.artifactContent)), SHA256: fmt.Sprintf("%x", digest[:])}, nil
}

var _ MySQLService = (*fixtureMySQLService)(nil)

type fixtureMySQLAuthorizer struct{ actor Actor }

func (fixture *fixtureMySQLAuthorizer) Authorize(context.Context, AuthorizationRequest) (Actor, error) {
	return fixture.actor, nil
}

func (fixture *fixtureMySQLAuthorizer) AuthorizeSession(context.Context, AuthorizationRequest) (Actor, error) {
	return fixture.actor, nil
}
