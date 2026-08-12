package privilegebroker

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
	destination := filepath.Join(backupRoot, "instance-one", "backup.partial")
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Dump(authorized, instance, "application", destination); err != nil {
		t.Fatal(err)
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
		func() wireRequest { value := valid; value.ProviderID = "model-one"; return value }(),
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
	instance     mysqlmanager.Instance
	password     string
	tests, dumps int
	dumpStarted  chan struct{}
	blockDump    bool
	artifactRoot string
}

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
func (service *fixtureMySQLService) Dump(ctx context.Context, instance mysqlmanager.Instance, _, path string) (mysqlmanager.DumpResult, error) {
	service.instance, service.dumps = instance, service.dumps+1
	if service.blockDump {
		close(service.dumpStarted)
		<-ctx.Done()
		return mysqlmanager.DumpResult{}, ctx.Err()
	}
	return mysqlmanager.DumpResult{}, os.WriteFile(path, []byte("dump"), 0o600)
}
func (*fixtureMySQLService) Import(context.Context, mysqlmanager.Instance, string, string) error {
	return nil
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

var _ MySQLService = (*fixtureMySQLService)(nil)

type fixtureMySQLAuthorizer struct{ actor Actor }

func (fixture *fixtureMySQLAuthorizer) Authorize(context.Context, AuthorizationRequest) (Actor, error) {
	return fixture.actor, nil
}

func (fixture *fixtureMySQLAuthorizer) AuthorizeSession(context.Context, AuthorizationRequest) (Actor, error) {
	return fixture.actor, nil
}
