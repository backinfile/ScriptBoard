package app_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/mfa"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/secretstore"
)

func TestManagedMFADomainStateIsOwnedByPrivilegedBroker(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "web-state")
	brokerSecretRoot := filepath.Join(root, "broker-secrets")
	transportOptions := privilegebroker.TransportOptions{StateRoot: stateRoot, DevelopmentCurrentUser: true}
	if runtime.GOOS == "linux" {
		transportOptions.Endpoint = filepath.Join(root, "broker.sock")
	}
	transport, err := privilegebroker.Listen(transportOptions)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := secretstore.New(brokerSecretRoot)
	if err != nil {
		t.Fatal(err)
	}
	brokerMFA, err := mfa.New(mfa.Options{StateRoot: brokerSecretRoot, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		Authorizer: &capturingPrivilegedAuthorizer{}, Executor: &capturingPrivilegedExecutor{}, MFA: brokerMFA,
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
	brokerClient := privilegebroker.NewClient(privilegebroker.ClientOptions{Dial: privilegebroker.Dial(transport.Endpoint)})
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: stateRoot, PrivilegedBrokerEndpoint: transport.Endpoint,
		MFAStore: privilegebroker.NewRemoteMFA(brokerClient),
	})
	page := getBody(t, client, serverURL+"/settings/account/mfa", http.StatusOK)
	response, err := client.PostForm(serverURL+"/settings/account/mfa/enroll", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "data-mfa-secret") {
		t.Fatalf("remote MFA enrollment status=%d body=%s", response.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(brokerSecretRoot, "secrets", "account-mfa.enc")); err != nil {
		t.Fatalf("Broker-owned MFA state missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "secrets", "account-mfa.enc")); !os.IsNotExist(err) {
		t.Fatalf("Web State Root unexpectedly owns MFA ciphertext: %v", err)
	}
}

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
