package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"scriptboard/internal/providercredential"
)

func TestProviderUsesTypedAuthorizedBrokerOperations(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureProviderService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{}, Providers: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	providers := NewProviderCredentials(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	record := providercredential.Record{ID: "model-one", Provider: "openai", Model: "gpt-test", Endpoint: "https://api.openai.com/v1", Shared: true}
	if err := providers.Store(context.Background(), record, "secret"); err == nil {
		t.Fatal("provider credential stored without session authorization")
	}
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "provider-domain-test"})
	if err := providers.Store(authorized, record, "secret"); err != nil {
		t.Fatal(err)
	}
	session, err := providers.Start(authorized, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Endpoint() != "http://127.0.0.1:32123/v1" || session.Capability() != "runtime-capability" {
		t.Fatalf("session endpoint=%q capability=%q", session.Endpoint(), session.Capability())
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := providers.Delete(authorized, record.ID); err != nil {
		t.Fatal(err)
	}
	if service.actor != "administrator" || service.record.ID != record.ID || service.credential != "secret" || service.starts != 1 || service.stops != 1 || service.deletes != 1 {
		t.Fatalf("typed provider operations were not preserved: %+v", service)
	}
}

func TestProviderAuthorizationDenialPrecedesCredentialAccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureProviderService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil }, Authorizer: &fixtureAuthorizer{err: errors.New("expired")},
		Executor: &fixtureExecutor{}, Providers: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	providers := NewProviderCredentials(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "provider-denied-test"})
	if _, err := providers.Start(authorized, "model-one"); err == nil {
		t.Fatal("expired session started provider proxy")
	}
	if service.starts != 0 {
		t.Fatalf("denied request reached provider service %d times", service.starts)
	}
}

func TestProviderProtocolRejectsGenericAndUnrelatedFields(t *testing.T) {
	valid := wireRequest{
		Version: ProtocolVersion, Operation: operationProviderStore, RequestID: "provider-protocol-test",
		SessionToken: strings.Repeat("s", 32), ProviderID: "model-one", ProviderName: "openai",
		ProviderModel: "gpt-test", ProviderEndpoint: "https://api.openai.com/v1", ProviderCredential: "secret",
	}
	requests := []wireRequest{
		func() wireRequest {
			value := valid
			value.Parameters = json.RawMessage(`{"secret":"no"}`)
			return value
		}(),
		func() wireRequest { value := valid; value.PasskeyUserID = "administrator"; return value }(),
		func() wireRequest { value := valid; value.RemoteWebsiteID = "source"; return value }(),
		{Version: ProtocolVersion, Operation: operationProviderStart, RequestID: "provider-protocol-test", ProviderID: "model-one"},
		{Version: ProtocolVersion, Operation: operationProviderStop, RequestID: "provider-protocol-test", ProviderSessionHandle: "short"},
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("accepted invalid provider request: %+v", request)
		}
	}
}

type fixtureProviderService struct {
	actor, credential string
	record            providercredential.Record
	starts, stops     int
	deletes           int
}

func (service *fixtureProviderService) Store(_ context.Context, actor string, record providercredential.Record, credential string) error {
	service.actor, service.record, service.credential = actor, record, credential
	return nil
}

func (service *fixtureProviderService) Delete(_ context.Context, actor, id string) error {
	service.actor, service.record.ID, service.deletes = actor, id, service.deletes+1
	return nil
}

func (service *fixtureProviderService) Start(_ context.Context, actor, id string) (providercredential.Session, error) {
	service.actor, service.record.ID, service.starts = actor, id, service.starts+1
	return providercredential.Session{Endpoint: "http://127.0.0.1:32123/v1", Capability: "runtime-capability", Handle: strings.Repeat("a", 43)}, nil
}

func (service *fixtureProviderService) Stop(_ context.Context, _ string) error {
	service.stops++
	return nil
}

func (service *fixtureProviderService) Close(context.Context) error { return nil }
