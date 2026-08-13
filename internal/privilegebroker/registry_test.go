package privilegebroker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"scriptboard/internal/registrymonitor"
)

func TestRegistryConnectionsUseAuthorizedMutationAndPeerOnlyCompletion(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureRegistryService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{}, Registry: service,
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

type fixtureRegistryService struct {
	operationID, cardID, password string
	prepares, commits             int
}

func (service *fixtureRegistryService) Prepare(_ context.Context, operationID, cardID string, _ registrymonitor.Config, password string, _ bool) error {
	service.operationID, service.cardID, service.password = operationID, cardID, password
	service.prepares++
	return nil
}
func (service *fixtureRegistryService) PrepareDelete(context.Context, string, string) error {
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
func (*fixtureRegistryService) Test(context.Context, string, registrymonitor.Config, string, bool) ([]registrymonitor.ImageResult, error) {
	return []registrymonitor.ImageResult{{Image: "team/api", Tag: "1.0.0"}}, nil
}
