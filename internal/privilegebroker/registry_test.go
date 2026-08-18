package privilegebroker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/registrymonitor"
)

func TestRegistryConnectionOperationsAcceptValidSessionWithoutRecentStepUp(t *testing.T) {
	now := time.Unix(1786957701, 0).UTC()
	database := openBrokerDatabase(t)
	security, err := NewDatabaseSecurity(database, auditlog.New(database), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := "registry-session-token-0123456789"
	insertBrokerSession(t, database, token, "maintainer", now.Add(-4*time.Hour).Unix(), now.Add(time.Hour).Unix())

	service := &fixtureRegistryService{}
	connections := registryTestConnections(t, security, service)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: token, RequestID: "registry-session-test"})
	config := registrymonitor.Config{Endpoint: "http://registry.example:5000", Images: []string{"team/api"}, AuthMode: "anonymous"}
	if err := connections.Prepare(authorized, "operation", "card", config, "", false); err != nil {
		t.Fatalf("valid session could not prepare Registry connection: %v", err)
	}
	if _, err := connections.Test(authorized, "", config, "", false); err != nil {
		t.Fatalf("valid session could not test Registry connection: %v", err)
	}
	if err := connections.PrepareDelete(authorized, "delete-operation", "card"); err != nil {
		t.Fatalf("valid session could not prepare Registry connection deletion: %v", err)
	}
	if service.prepares != 1 || service.tests != 1 || service.deletes != 1 {
		t.Fatalf("Registry operations = prepare:%d test:%d delete:%d", service.prepares, service.tests, service.deletes)
	}
}

func TestRegistryDockerConfigurationStillRequiresRecentStepUp(t *testing.T) {
	now := time.Unix(1786957701, 0).UTC()
	database := openBrokerDatabase(t)
	security, err := NewDatabaseSecurity(database, auditlog.New(database), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := "registry-docker-session-token-0123456789"
	insertBrokerSession(t, database, token, "administrator", now.Add(-4*time.Hour).Unix(), now.Add(time.Hour).Unix())

	service := &fixtureRegistryService{}
	connections := registryTestConnections(t, security, service)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: token, RequestID: "registry-docker-step-up-test"})
	if _, err := connections.RegisterInsecure(authorized, "http://registry.example:5000"); err == nil {
		t.Fatal("stale step-up changed Docker insecure Registry configuration")
	}
	if service.insecureRegistrations != 0 {
		t.Fatal("denied Docker Registry configuration reached execution")
	}
}

func TestRegistryDockerConfigurationRequiresAdministrator(t *testing.T) {
	now := time.Unix(1786957701, 0).UTC()
	database := openBrokerDatabase(t)
	security, err := NewDatabaseSecurity(database, auditlog.New(database), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := "registry-maintainer-token-0123456789"
	insertBrokerSession(t, database, token, "maintainer", now.Add(-time.Minute).Unix(), now.Add(time.Hour).Unix())

	service := &fixtureRegistryService{}
	connections := registryTestConnections(t, security, service)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: token, RequestID: "registry-docker-admin-test"})
	if _, err := connections.RegisterInsecure(authorized, "http://registry.example:5000"); err == nil {
		t.Fatal("maintainer changed Docker insecure Registry configuration")
	}
	if service.insecureRegistrations != 0 {
		t.Fatal("denied maintainer request reached Docker Registry configuration")
	}
}

func TestRegistryConnectionMutationRejectsUnprivilegedSession(t *testing.T) {
	service := &fixtureRegistryService{}
	connections := registryTestConnections(t, &fixtureAuthorizer{actor: Actor{UserID: "viewer", Role: "viewer"}}, service)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "registry-role-test"})
	config := registrymonitor.Config{Endpoint: "https://registry.example", Images: []string{"team/api"}, AuthMode: "anonymous"}
	if err := connections.Prepare(authorized, "operation", "card", config, "", false); err == nil {
		t.Fatal("viewer session prepared Registry connection")
	}
	if service.prepares != 0 {
		t.Fatal("unprivileged Registry request reached execution")
	}
}

func TestRegistryConnectionsUseAuthorizedMutationAndPeerOnlyCompletion(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureRegistryService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator", Role: "administrator"}}, Executor: &fixtureExecutor{}, Registry: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	connections := NewRegistryConnections(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	config := registrymonitor.Config{Endpoint: "https://registry.example", Images: []string{"team/api"}, AuthMode: "basic", Username: "robot"}
	if err := connections.Prepare(context.Background(), "operation", "card", config, "secret", false); err == nil {
		t.Fatal("Registry mutation succeeded without session authorization")
	}
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "registry-domain-test"})
	if err := connections.Prepare(authorized, "operation", "card", config, "secret", false); err != nil {
		t.Fatal(err)
	}
	if err := connections.Commit(context.Background(), "operation"); err != nil {
		t.Fatal(err)
	}
	configured, err := connections.Configured(context.Background(), "card")
	if err != nil || !configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
	insecureConfigured, err := connections.InsecureConfigured(context.Background(), "http://registry.example:5000")
	if err != nil || !insecureConfigured {
		t.Fatalf("insecure configured=%v err=%v", insecureConfigured, err)
	}
	if _, err := connections.RegisterInsecure(context.Background(), "http://registry.example:5000"); err == nil {
		t.Fatal("Docker Registry mutation succeeded without session authorization")
	}
	changed, err := connections.RegisterInsecure(authorized, "http://registry.example:5000")
	if err != nil || !changed {
		t.Fatalf("register insecure changed=%v err=%v", changed, err)
	}
	if service.operationID != "operation" || service.cardID != "card" || service.password != "secret" || service.commits != 1 {
		t.Fatalf("typed Registry fields not preserved: %+v", service)
	}
}

func TestRegistryAuthorizationDenialPrecedesCredentialMutation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureRegistryService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{err: errors.New("expired")}, Executor: &fixtureExecutor{}, Registry: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	connections := NewRegistryConnections(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "registry-denied-test"})
	config := registrymonitor.Config{Endpoint: "https://registry.example", Images: []string{"team/api"}, AuthMode: "anonymous"}
	if err := connections.Prepare(authorized, "operation", "card", config, "", false); err == nil {
		t.Fatal("denied Registry mutation succeeded")
	}
	if service.prepares != 0 {
		t.Fatal("denied request reached Registry connection module")
	}
}

func TestRegistryProtocolRejectsUnrelatedFieldsAndAuthorizationMixing(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationRegistryInspect, RequestID: "registry-test", Registry: &registryWireRequest{CardID: "card"}}
	requests := []wireRequest{
		func() wireRequest { value := valid; value.SessionToken = strings.Repeat("s", 32); return value }(),
		func() wireRequest { value := valid; value.PasskeyUserID = "administrator"; return value }(),
		{Version: ProtocolVersion, Operation: operationRegistryPrepare, RequestID: "registry-test", Registry: &registryWireRequest{OperationID: "operation", CardID: "card"}},
		{Version: ProtocolVersion, Operation: operationCheckpointVerify, RequestID: "registry-test", Registry: &registryWireRequest{CardID: "card"}},
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("accepted invalid Registry request: %+v", request)
		}
	}
}

func registryTestConnections(t *testing.T, authorizer Authorizer, service RegistryService) *RegistryConnections {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: authorizer, Executor: &fixtureExecutor{}, Registry: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() { _ = server.Close() })
	return NewRegistryConnections(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
}

type fixtureRegistryService struct {
	operationID, cardID, password string
	prepares, commits, tests      int
	deletes                       int
	insecureRegistrations         int
}

func (service *fixtureRegistryService) Prepare(_ context.Context, operationID, cardID string, _ registrymonitor.Config, password string, _ bool) error {
	service.operationID, service.cardID, service.password = operationID, cardID, password
	service.prepares++
	return nil
}

func (service *fixtureRegistryService) PrepareDelete(_ context.Context, operationID, cardID string) error {
	service.operationID, service.cardID = operationID, cardID
	service.deletes++
	return nil
}
func (service *fixtureRegistryService) Commit(_ context.Context, operationID string) error {
	service.operationID = operationID
	service.commits++
	return nil
}
func (*fixtureRegistryService) Acknowledge(context.Context, string) error { return nil }
func (*fixtureRegistryService) Abort(context.Context, string) error       { return nil }
func (*fixtureRegistryService) Configured(context.Context, string) (bool, error) {
	return true, nil
}
func (*fixtureRegistryService) Inspect(context.Context, string) ([]registrymonitor.ImageResult, error) {
	return []registrymonitor.ImageResult{{Image: "team/api", Tag: "1.0.0"}}, nil
}

func (service *fixtureRegistryService) Test(context.Context, string, registrymonitor.Config, string, bool) ([]registrymonitor.ImageResult, error) {
	service.tests++
	return []registrymonitor.ImageResult{{Image: "team/api", Tag: "1.0.0"}}, nil
}
func (*fixtureRegistryService) InsecureConfigured(context.Context, string) (bool, error) {
	return true, nil
}

func (service *fixtureRegistryService) RegisterInsecure(context.Context, string) (bool, error) {
	service.insecureRegistrations++
	return true, nil
}
