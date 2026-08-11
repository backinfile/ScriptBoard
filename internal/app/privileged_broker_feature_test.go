package app_test

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/privilegebroker"
)

func TestProductionHostSecurityMutationUsesSessionBoundPrivilegedBroker(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	transportOptions := privilegebroker.TransportOptions{StateRoot: stateRoot, DevelopmentCurrentUser: true}
	if runtime.GOOS == "linux" {
		transportOptions.Endpoint = filepath.Join(root, "broker.sock")
	}
	transport, err := privilegebroker.Listen(transportOptions)
	if err != nil {
		t.Fatal(err)
	}
	executor := &capturingPrivilegedExecutor{}
	authorizer := &capturingPrivilegedAuthorizer{}
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		Authorizer: authorizer, Executor: executor, Now: time.Now,
	})
	if err != nil {
		_ = transport.Close()
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() {
		_ = server.Close()
		_ = transport.Close()
	})

	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: stateRoot, PrivilegedBrokerEndpoint: transport.Endpoint,
	})
	task := getSecurityPage(t, client, serverURL+"/monitor/security/windows-firewall/rules/new")
	response, err := client.PostForm(serverURL+"/monitor/security/windows-firewall/rules", url.Values{
		"csrf_token": {formToken(t, task)}, "direction": {"in"}, "action": {"allow"},
		"protocol": {"tcp"}, "port": {"443"}, "address": {"any"}, "name": {"Broker fixture"}, "profile": {"private"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("firewall mutation status=%d", response.StatusCode)
	}
	authorizer.mu.Lock()
	authorization := authorizer.request
	authorizer.mu.Unlock()
	executor.mu.Lock()
	action := executor.action
	executor.mu.Unlock()
	if authorization.SessionToken == "" || authorization.RequestID == "" || authorization.Action != privilegebroker.ActionWindowsFirewallAdd || action != privilegebroker.ActionWindowsFirewallAdd {
		t.Fatalf("authorization=%+v executed=%q", authorization, action)
	}
}

type capturingPrivilegedAuthorizer struct {
	mu      sync.Mutex
	request privilegebroker.AuthorizationRequest
}

func (authorizer *capturingPrivilegedAuthorizer) Authorize(_ context.Context, request privilegebroker.AuthorizationRequest) (privilegebroker.Actor, error) {
	authorizer.mu.Lock()
	authorizer.request = request
	authorizer.mu.Unlock()
	return privilegebroker.Actor{UserID: "admin", Username: "admin", Role: "administrator", AuthenticationAssurance: 1}, nil
}

type capturingPrivilegedExecutor struct {
	mu     sync.Mutex
	action privilegebroker.Action
}

func (executor *capturingPrivilegedExecutor) Execute(_ context.Context, request privilegebroker.ExecutionRequest) error {
	executor.mu.Lock()
	executor.action = request.Action
	executor.mu.Unlock()
	return nil
}
