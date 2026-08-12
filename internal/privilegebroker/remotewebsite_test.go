package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestRemoteWebsiteUsesTypedAuthorizedBrokerOperations(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureRemoteWebsiteService{payload: json.RawMessage(`{"ok":true,"action":"website_monitor","schema_version":1,"data":{"monitors":[],"total":0}}`)}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{}, RemoteWebsites: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	remote := NewRemoteWebsite(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "remote-website-domain-test"})
	key := "sbk_0123456789abcdef." + strings.Repeat("a", 43)
	endpoint := "https://example.com/trigger?name=website-status"
	if err := remote.Store(context.Background(), "source-one", endpoint, key); err == nil {
		t.Fatal("remote website credential stored without session authorization")
	}
	if err := remote.Store(authorized, "source-one", endpoint, key); err != nil {
		t.Fatal(err)
	}
	payload, err := remote.Fetch(authorized, "source-one", "zh-CN")
	if err != nil || !json.Valid(payload) {
		t.Fatalf("payload=%s error=%v", payload, err)
	}
	if err := remote.Delete(authorized, "source-one"); err != nil {
		t.Fatal(err)
	}
	if service.id != "source-one" || service.endpoint != endpoint || service.key != key || service.locale != "zh-CN" || service.deletes != 1 {
		t.Fatalf("Broker did not preserve typed fields: %+v", service)
	}
}

func TestRemoteWebsiteAuthorizationDenialPrecedesCredentialAccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureRemoteWebsiteService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil }, Authorizer: &fixtureAuthorizer{err: errors.New("expired")},
		Executor: &fixtureExecutor{}, RemoteWebsites: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	remote := NewRemoteWebsite(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "remote-website-denied-test"})
	if _, err := remote.Fetch(authorized, "source-one", "en-US"); err == nil {
		t.Fatal("expired session fetched remote website credential")
	}
	if service.fetches != 0 {
		t.Fatalf("denied request reached credential service %d times", service.fetches)
	}
}

func TestRemoteWebsiteProtocolRejectsGenericAndUnrelatedFields(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationRemoteWebsiteFetch, RequestID: "remote-website-test", SessionToken: strings.Repeat("s", 32), RemoteWebsiteID: "source-one", RemoteWebsiteLocale: "en-US"}
	requests := []wireRequest{
		func() wireRequest {
			value := valid
			value.Parameters = json.RawMessage(`{"secret":"no"}`)
			return value
		}(),
		func() wireRequest { value := valid; value.PasskeyUserID = "administrator"; return value }(),
		func() wireRequest { value := valid; value.RemoteWebsiteKey = "not-allowed"; return value }(),
		{Version: ProtocolVersion, Operation: operationRemoteWebsiteFetch, RequestID: "remote-website-test", RemoteWebsiteID: "source-one"},
		{Version: ProtocolVersion, Operation: operationRemoteWebsiteStore, RequestID: "remote-website-test", SessionToken: strings.Repeat("s", 32), RemoteWebsiteID: "source-one"},
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("accepted invalid remote website request: %+v", request)
		}
	}
}

type fixtureRemoteWebsiteService struct {
	id, endpoint, key, locale string
	payload                   json.RawMessage
	fetches, deletes          int
}

func (service *fixtureRemoteWebsiteService) Store(_ context.Context, id, endpoint, key string) error {
	service.id, service.endpoint, service.key = id, endpoint, key
	return nil
}

func (service *fixtureRemoteWebsiteService) Delete(_ context.Context, id string) error {
	service.id, service.deletes = id, service.deletes+1
	return nil
}

func (service *fixtureRemoteWebsiteService) Fetch(_ context.Context, id, locale string) (json.RawMessage, error) {
	service.id, service.locale, service.fetches = id, locale, service.fetches+1
	return append(json.RawMessage(nil), service.payload...), nil
}
