package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCapabilityIsSingleUseAndBoundToExactParameters(t *testing.T) {
	authorizer := &fixtureAuthorizer{actor: Actor{UserID: "user-1", Username: "admin", Role: "administrator"}}
	executor := &fixtureExecutor{}
	server, client := brokerFixture(t, authorizer, executor)
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "request-1"})
	parameters := json.RawMessage(`{"id":"firewall-rule-1","enabled":true}`)
	capability, binding, err := client.authorize(ctx, ActionWindowsFirewallSet, "firewall-rule-1", "revision-1", parameters)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.execute(ctx, capability, binding, json.RawMessage(`{"id":"firewall-rule-1","enabled":false}`)); err == nil {
		t.Fatal("capability accepted modified parameters")
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls=%d after parameter substitution", executor.calls)
	}
	if _, err := client.execute(ctx, capability, binding, parameters); err == nil {
		t.Fatal("consumed capability was replayed")
	}
}

func TestAuthorizedCapabilityExecutesExactlyOnce(t *testing.T) {
	authorizer := &fixtureAuthorizer{actor: Actor{UserID: "user-1", Username: "admin", Role: "administrator"}}
	executor := &fixtureExecutor{}
	server, client := brokerFixture(t, authorizer, executor)
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "request-2"})
	parameters := json.RawMessage(`{"id":"firewall-rule-1","enabled":true}`)
	if err := client.Invoke(ctx, ActionWindowsFirewallSet, "firewall-rule-1", "revision-1", parameters); err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 1 || executor.calls != 1 || executor.action != ActionWindowsFirewallSet || string(executor.parameters) != string(parameters) {
		t.Fatalf("authorizer=%d executor=%d action=%q parameters=%s", authorizer.calls, executor.calls, executor.action, executor.parameters)
	}
}

func TestAuthorizationFailureNeverCreatesCapability(t *testing.T) {
	authorizer := &fixtureAuthorizer{err: errors.New("step-up expired")}
	executor := &fixtureExecutor{}
	server, client := brokerFixture(t, authorizer, executor)
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "request-3"})
	err := client.Invoke(ctx, ActionWindowsFirewallDelete, "firewall-rule-1", "revision-1", json.RawMessage(`{"id":"firewall-rule-1"}`))
	if err == nil || executor.calls != 0 {
		t.Fatalf("authorization error=%v executor calls=%d", err, executor.calls)
	}
}

func TestProtocolRejectsDuplicateKeys(t *testing.T) {
	record := `{"version":1,"version":1,"operation":"authorize"}` + "\n"
	if _, err := readWireRequest(strings.NewReader(record)); err == nil {
		t.Fatal("protocol accepted duplicate JSON keys")
	}
}

func brokerFixture(t *testing.T, authorizer Authorizer, executor Executor) (*Server, *Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Listener: listener, Authorizer: authorizer, Executor: executor,
		VerifyPeer: func(net.Conn) error { return nil }, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", listener.Addr().String())
	}})
	return server, client
}

type fixtureAuthorizer struct {
	actor Actor
	err   error
	calls int
}

func (fixture *fixtureAuthorizer) Authorize(_ context.Context, request AuthorizationRequest) (Actor, error) {
	fixture.calls++
	if fixture.err != nil {
		return Actor{}, fixture.err
	}
	return fixture.actor, nil
}

type fixtureExecutor struct {
	mu         sync.Mutex
	calls      int
	action     Action
	parameters json.RawMessage
}

func (fixture *fixtureExecutor) Execute(_ context.Context, request ExecutionRequest) error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.calls++
	fixture.action = request.Action
	fixture.parameters = append(json.RawMessage(nil), request.Parameters...)
	return nil
}
