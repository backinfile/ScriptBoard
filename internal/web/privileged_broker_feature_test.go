package web_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"scriptboard/internal/mfa"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/passkey"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/providercredential"
	"scriptboard/internal/remotewebsite"
	"scriptboard/internal/secretstore"
	app "scriptboard/internal/web"
)

func TestManagedMySQLCredentialAndExecutionAreOwnedByPrivilegedBroker(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "web-state")
	brokerSecretRoot := filepath.Join(root, "broker-secrets")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	vault, err := secretstore.New(brokerSecretRoot)
	if err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(stateRoot, "database-backups", "mysql")
	localBackend, err := mysqlmanager.NewLocalBackend(mysqlmanager.Options{DB: database, StateRoot: brokerSecretRoot, BackupRoot: backupRoot, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	mysqlOperations, err := mysqlmanager.New(mysqlmanager.Options{DB: database, StateRoot: brokerSecretRoot, BackupRoot: backupRoot, Backend: localBackend})
	if err != nil {
		t.Fatal(err)
	}
	mysqlService, err := privilegebroker.NewBrokerMySQLService(database, localBackend, mysqlOperations, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	transportOptions := privilegebroker.TransportOptions{StateRoot: stateRoot, DevelopmentCurrentUser: true}
	if runtime.GOOS == "linux" {
		transportOptions.Endpoint = filepath.Join(root, "mysql-broker.sock")
	}
	transport, err := privilegebroker.Listen(transportOptions)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &capturingPrivilegedAuthorizer{}
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		Authorizer: authorizer, Executor: &capturingPrivilegedExecutor{}, MySQL: mysqlService})
	if err != nil {
		_ = transport.Close()
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() { _ = server.Close(); _ = transport.Close() })
	brokerClient := privilegebroker.NewClient(privilegebroker.ClientOptions{Dial: privilegebroker.Dial(transport.Endpoint)})
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot, PrivilegedBrokerEndpoint: transport.Endpoint,
		MySQLBackend: privilegebroker.NewMySQLBackend(brokerClient, mysqlmanager.ToolSettings{})})
	page := getBody(t, client, serverURL+"/resources/databases", http.StatusOK)
	response, err := client.PostForm(serverURL+"/resources/databases/instances", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"Broker MySQL"}, "host": {"127.0.0.1"}, "port": {"3306"},
		"username": {"scriptboard"}, "password": {"broker-mysql-secret"}, "tls_mode": {"required"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create MySQL instance status=%d", response.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(brokerSecretRoot, "secrets", "mysql-credentials.v2.enc")); err != nil {
		t.Fatalf("Broker-owned MySQL credentials missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "secrets", "mysql-credentials.v2.enc")); !os.IsNotExist(err) {
		t.Fatalf("managed Web unexpectedly owns MySQL ciphertext: %v", err)
	}
	sealed, err := os.ReadFile(filepath.Join(brokerSecretRoot, "secrets", "mysql-credentials.v2.enc"))
	if err != nil || strings.Contains(string(sealed), "broker-mysql-secret") {
		t.Fatalf("Broker MySQL ciphertext is missing or contains plaintext: %v", err)
	}
	var instance mysqlmanager.Instance
	if err := database.QueryRow(`SELECT id,name,host,port,username,tls_mode,ca_path,credential_configured FROM mysql_instances WHERE name='Broker MySQL'`).
		Scan(&instance.ID, &instance.Name, &instance.Host, &instance.Port, &instance.Username, &instance.TLSMode, &instance.CAPath, &instance.CredentialConfigured); err != nil {
		t.Fatal(err)
	}
	instance.Host = "substituted.internal"
	authorized := privilegebroker.WithAuthorization(context.Background(), privilegebroker.Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "mysql-substitution-test"})
	if _, err := privilegebroker.NewMySQLBackend(brokerClient, mysqlmanager.ToolSettings{}).Test(authorized, instance); err == nil {
		t.Fatal("Broker accepted substituted MySQL instance endpoint")
	}
	if strings.Contains(string(getBody(t, client, serverURL+"/resources/databases", http.StatusOK)), "broker-mysql-secret") {
		t.Fatal("MySQL password was rendered into the managed Web page")
	}
}

func TestManagedProviderCredentialAndProxyAreOwnedByPrivilegedBroker(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "web-state")
	brokerSecretRoot := filepath.Join(root, "broker-secrets")
	transportOptions := privilegebroker.TransportOptions{StateRoot: stateRoot, DevelopmentCurrentUser: true}
	if runtime.GOOS == "linux" {
		transportOptions.Endpoint = filepath.Join(root, "provider-broker.sock")
	}
	transport, err := privilegebroker.Listen(transportOptions)
	if err != nil {
		t.Fatal(err)
	}
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer upstream.Close()
	vault, err := secretstore.New(brokerSecretRoot)
	if err != nil {
		t.Fatal(err)
	}
	providerService, err := providercredential.New(providercredential.Options{StateRoot: brokerSecretRoot, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer, Authorizer: &capturingPrivilegedAuthorizer{},
		Executor: &capturingPrivilegedExecutor{}, Providers: providerService,
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
	providers := privilegebroker.NewProviderCredentials(brokerClient)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: stateRoot, PrivilegedBrokerEndpoint: transport.Endpoint, ProviderCredentials: providers,
	})
	page := getBody(t, client, serverURL+"/settings/ai", http.StatusOK)
	response, err := client.PostForm(serverURL+"/settings/ai/llms", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"Broker Provider"}, "provider": {"openai-compatible"},
		"model": {"fixture-model"}, "endpoint": {upstream.URL + "/v1"}, "api_key": {"provider-secret"}, "make_default": {"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create provider status=%d", response.StatusCode)
	}
	page = getBody(t, client, serverURL+"/settings/ai", http.StatusOK)
	match := regexp.MustCompile(`data-llm-id="([^"]+)"`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("provider model ID missing from page: %s", page)
	}
	authorized := privilegebroker.WithAuthorization(context.Background(), privilegebroker.Authorization{
		SessionToken: strings.Repeat("s", 32), RequestID: "managed-provider-start",
	})
	providerSession, err := providers.Start(authorized, string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer providerSession.Close(context.Background())
	request, _ := http.NewRequest(http.MethodPost, providerSession.Endpoint()+"/chat/completions", strings.NewReader(`{"model":"fixture-model"}`))
	request.Header.Set("Authorization", "Bearer "+providerSession.Capability())
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || authorization != "Bearer provider-secret" {
		t.Fatalf("proxy status=%d upstream authorization=%q", response.StatusCode, authorization)
	}
	if _, err := os.Stat(filepath.Join(brokerSecretRoot, "secrets", "assistant-provider-records.enc")); err != nil {
		t.Fatalf("Broker-owned provider state missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "secrets", "assistant-provider.enc")); !os.IsNotExist(err) {
		t.Fatalf("Web State Root unexpectedly owns provider ciphertext: %v", err)
	}
	if strings.Contains(string(page), "provider-secret") {
		t.Fatal("provider credential was rendered into the Web page")
	}
}

func TestManagedRemoteWebsiteCredentialIsOwnedAndUsedByPrivilegedBroker(t *testing.T) {
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
	var authorization string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"ok":true,"action":"website_monitor","schema_version":1,"data":{"monitors":[],"alerts":[],"counts":{},"total":0,"needsCare":0}}`)
	}))
	defer upstream.Close()
	vault, err := secretstore.New(brokerSecretRoot)
	if err != nil {
		t.Fatal(err)
	}
	brokerRemoteWebsites, err := remotewebsite.New(remotewebsite.Options{StateRoot: brokerSecretRoot, SecretStore: vault, Client: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer, Authorizer: &capturingPrivilegedAuthorizer{},
		Executor: &capturingPrivilegedExecutor{}, RemoteWebsites: brokerRemoteWebsites,
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
		RemoteWebsiteService: privilegebroker.NewRemoteWebsite(brokerClient),
	})
	page := getBody(t, client, serverURL+"/monitor/websites", http.StatusOK)
	key := "sbk_0123456789abcdef." + strings.Repeat("a", 43)
	response, err := client.PostForm(serverURL+"/monitor/websites/remotes", url.Values{
		"csrf_token": {formToken(t, page)}, "label": {"Broker branch"},
		"endpoint": {upstream.URL + "/trigger/monitoring/website-status"}, "key": {key},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	body := getBody(t, client, serverURL+"/monitor/websites", http.StatusOK)
	if !strings.Contains(string(body), "Broker branch") || authorization != "Bearer "+key {
		t.Fatalf("authorization=%q body=%s", authorization, body)
	}
	if _, err := os.Stat(filepath.Join(brokerSecretRoot, "secrets", "remote-website-connections.enc")); err != nil {
		t.Fatalf("Broker-owned remote website state missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "secrets", "remote-website-connections.enc")); !os.IsNotExist(err) {
		t.Fatalf("Web State Root unexpectedly owns remote website ciphertext: %v", err)
	}
}

func TestManagedPasskeyDomainStateIsOwnedByPrivilegedBroker(t *testing.T) {
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
	brokerPasskeys, err := passkey.New(passkey.Options{StateRoot: brokerSecretRoot, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		Authorizer: &capturingPrivilegedAuthorizer{}, Executor: &capturingPrivilegedExecutor{}, Passkeys: brokerPasskeys,
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
		PasskeyStore: privilegebroker.NewRemotePasskey(brokerClient),
	})
	if err := brokerPasskeys.Add("administrator", "Broker security key", webauthn.Credential{ID: []byte{1, 2, 3}, PublicKey: []byte{4, 5, 6}}); err != nil {
		t.Fatal(err)
	}
	page := getBody(t, client, serverURL+"/settings/account/mfa", http.StatusOK)
	if !strings.Contains(string(page), "Broker security key") {
		t.Fatalf("remote passkey is missing from account page: %s", page)
	}
	if _, err := os.Stat(filepath.Join(brokerSecretRoot, "secrets", "account-passkeys.enc")); err != nil {
		t.Fatalf("Broker-owned passkey state missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "secrets", "account-passkeys.enc")); !os.IsNotExist(err) {
		t.Fatalf("Web State Root unexpectedly owns passkey ciphertext: %v", err)
	}
}

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
	return privilegebroker.Actor{UserID: "administrator", Username: "admin", Role: "administrator", AuthenticationAssurance: 1}, nil
}

func (authorizer *capturingPrivilegedAuthorizer) AuthorizeSession(ctx context.Context, request privilegebroker.AuthorizationRequest) (privilegebroker.Actor, error) {
	return authorizer.Authorize(ctx, request)
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
