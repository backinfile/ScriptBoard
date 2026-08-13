package privilegebroker

import (
	"context"
	"testing"
	"time"
)

func TestProtectedLocalTransportAcceptsConfiguredDevelopmentIdentity(t *testing.T) {
	transport, err := Listen(developmentTransportOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "administrator"}},
		Executor:   &fixtureExecutor{}, Now: time.Now,
	})
	if err != nil {
		_ = transport.Close()
		t.Fatal(err)
	}
	server.Start()
	client := NewClient(ClientOptions{Dial: Dial(transport.Endpoint)})
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "request-transport-1"})
	if err := client.Invoke(ctx, ActionWindowsFirewallDelete, "rule-1", "revision-1", []byte(`{"id":"rule-1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	transport.cleanup()
}
