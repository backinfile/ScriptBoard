package registryconnection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/registrymonitor"
)

func TestDefaultRegistryClientAllowsConfiguredPrivateAddressAndPort(t *testing.T) {
	var receivedTagsRequest bool
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "test-user" || password != "test-only-password" {
			response.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.URL.Path != "/v2/test/team-api/tags/list" {
			http.NotFound(response, request)
			return
		}
		receivedTagsRequest = true
		_ = json.NewEncoder(response).Encode(map[string]any{
			"name": "test/team-api",
			"tags": []string{"latest", "v1.8.0", "v2.3.1"},
		})
	}))
	defer registry.Close()

	service, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	config := registrymonitor.Config{
		Endpoint: registry.URL,
		Images:   []string{"test/team-api"},
		AuthMode: "basic",
		Username: "test-user",
	}
	results, err := service.Test(context.Background(), "", config, "test-only-password", false)
	if err != nil {
		t.Fatal(err)
	}
	if !receivedTagsRequest || len(results) != 1 || results[0].Tag != "v2.3.1" || results[0].Error != "" {
		t.Fatalf("received=%v results=%#v", receivedTagsRequest, results)
	}
}

func TestPreparedConnectionIsInvisibleUntilCommitAndCommitIsIdempotent(t *testing.T) {
	var password string
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, password, _ = request.BasicAuth()
		_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"1.2.3"}})
	}))
	defer registry.Close()
	root := t.TempDir()
	service, err := New(Options{StateRoot: root, Client: registry.Client()})
	if err != nil {
		t.Fatal(err)
	}
	config := registrymonitor.Config{Endpoint: registry.URL, Images: []string{"team/api"}, AuthMode: "basic", Username: "robot"}
	if err := service.Prepare(context.Background(), "operation-1", "card-1", config, "secret", false); err != nil {
		t.Fatal(err)
	}
	if configured, err := service.Configured(context.Background(), "card-1"); err != nil || configured {
		t.Fatalf("prepared configured=%v err=%v", configured, err)
	}
	if _, err := service.Inspect(context.Background(), "card-1"); err != ErrNotFound {
		t.Fatalf("prepared inspect error=%v", err)
	}
	if err := service.Commit(context.Background(), "operation-1"); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "operation-1"); err != nil {
		t.Fatalf("idempotent commit: %v", err)
	}
	results, err := service.Inspect(context.Background(), "card-1")
	if err != nil || len(results) != 1 || results[0].Tag != "1.2.3" || password != "secret" {
		t.Fatalf("results=%#v password=%q err=%v", results, password, err)
	}
	body, err := os.ReadFile(filepath.Join(root, "secrets", storeFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret") || strings.Contains(string(body), registry.URL) {
		t.Fatal("Registry connection was stored in plaintext")
	}
}

func TestAbortAndDeleteNeverExposePendingState(t *testing.T) {
	service, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	config := registrymonitor.Config{Endpoint: "http://registry.test", Images: []string{"team/api"}, AuthMode: "anonymous"}
	if err := service.Prepare(context.Background(), "create", "card", config, "", false); err != nil {
		t.Fatal(err)
	}
	if err := service.Abort(context.Background(), "create"); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "create"); err != ErrNotFound {
		t.Fatalf("aborted commit error=%v", err)
	}
	if err := service.Prepare(context.Background(), "create-2", "card", config, "", false); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "create-2"); err != nil {
		t.Fatal(err)
	}
	if err := service.PrepareDelete(context.Background(), "delete", "card"); err != nil {
		t.Fatal(err)
	}
	state, err := service.load()
	if err != nil || state.Active["card"].Revision != "create-2" {
		t.Fatalf("pending delete hid active connection: state=%#v err=%v", state, err)
	}
	if err := service.Commit(context.Background(), "delete"); err != nil {
		t.Fatal(err)
	}
	if configured, _ := service.Configured(context.Background(), "card"); configured {
		t.Fatal("committed delete retained active connection")
	}
}

func TestPreparePreservesCredentialOnlyInsideExistingBinding(t *testing.T) {
	service, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	config := registrymonitor.Config{Endpoint: "http://registry.test", Images: []string{"team/api"}, AuthMode: "basic", Username: "robot"}
	if err := service.Prepare(context.Background(), "missing", "card", config, "", true); err != ErrNotFound {
		t.Fatalf("missing preserved credential error=%v", err)
	}
	if err := service.Prepare(context.Background(), "create", "card", config, "old-secret", false); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "create"); err != nil {
		t.Fatal(err)
	}
	config.Endpoint = "http://registry-new.test"
	if err := service.Prepare(context.Background(), "update", "card", config, "", true); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "update"); err != nil {
		t.Fatal(err)
	}
	state, err := service.load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active["card"].Password != "old-secret" || state.Active["card"].Config.Endpoint != "http://registry-new.test" {
		t.Fatalf("active record=%#v", state.Active["card"])
	}
}

func TestPasswordsPreserveLeadingAndTrailingWhitespace(t *testing.T) {
	var received string
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, received, _ = request.BasicAuth()
		_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"latest"}})
	}))
	defer registry.Close()
	service, err := New(Options{StateRoot: t.TempDir(), Client: registry.Client()})
	if err != nil {
		t.Fatal(err)
	}
	config := registrymonitor.Config{Endpoint: registry.URL, Images: []string{"team/api"}, AuthMode: "basic", Username: "robot"}
	const password = "  exact secret  "
	if err := service.Prepare(context.Background(), "whitespace", "card", config, password, false); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "whitespace"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inspect(context.Background(), "card"); err != nil {
		t.Fatal(err)
	}
	if received != password {
		t.Fatalf("stored password = %q, want exact %q", received, password)
	}
	if _, err := service.Test(context.Background(), "", config, password, false); err != nil {
		t.Fatal(err)
	}
	if received != password {
		t.Fatalf("tested password = %q, want exact %q", received, password)
	}
}

func TestCommitReceiptSurvivesUntilAcknowledged(t *testing.T) {
	service, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	config := registrymonitor.Config{Endpoint: "http://registry.test", Images: []string{"team/api"}, AuthMode: "anonymous"}
	if err := service.Prepare(context.Background(), "receipt", "card", config, "", false); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "receipt"); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(context.Background(), "receipt"); err != nil {
		t.Fatalf("durable receipt did not make Commit idempotent: %v", err)
	}
	if err := service.Acknowledge(context.Background(), "receipt"); err != nil {
		t.Fatal(err)
	}
	if err := service.Acknowledge(context.Background(), "receipt"); err != nil {
		t.Fatalf("acknowledgement is not idempotent: %v", err)
	}
	if err := service.Commit(context.Background(), "receipt"); err != ErrNotFound {
		t.Fatalf("acknowledged receipt remained durable: %v", err)
	}
}

func TestRegisterInsecureRegistryPreservesDockerConfigurationAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "docker", "daemon.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"log-driver":"json-file","insecure-registries":["existing.lan:5000"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{StateRoot: filepath.Join(root, "state"), DockerDaemonConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := service.InsecureConfigured(context.Background(), "http://registry.lan:5000")
	if err != nil || configured {
		t.Fatalf("initial configured=%v err=%v", configured, err)
	}
	changed, err := service.RegisterInsecure(context.Background(), "http://registry.lan:5000/")
	if err != nil || !changed {
		t.Fatalf("register changed=%v err=%v", changed, err)
	}
	changed, err = service.RegisterInsecure(context.Background(), "http://REGISTRY.lan:5000")
	if err != nil || changed {
		t.Fatalf("idempotent register changed=%v err=%v", changed, err)
	}
	changed, err = service.RegisterInsecure(context.Background(), "http://prefixed.lan:5000/registry")
	if err != nil || !changed {
		t.Fatalf("prefixed Registry register changed=%v err=%v", changed, err)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		LogDriver          string   `json:"log-driver"`
		InsecureRegistries []string `json:"insecure-registries"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.LogDriver != "json-file" || strings.Join(document.InsecureRegistries, ",") != "existing.lan:5000,registry.lan:5000,prefixed.lan:5000" {
		t.Fatalf("Docker configuration=%s", body)
	}
	configured, err = service.InsecureConfigured(context.Background(), "http://registry.lan:5000")
	if err != nil || !configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
}

func TestRegisterInsecureRegistryRejectsHTTPSAndInvalidDockerConfiguration(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "daemon.json")
	service, err := New(Options{StateRoot: filepath.Join(root, "state"), DockerDaemonConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterInsecure(context.Background(), "https://registry.example"); err == nil {
		t.Fatal("HTTPS Registry was accepted as insecure")
	}
	if err := os.WriteFile(configPath, []byte(`{"insecure-registries":"not-an-array"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterInsecure(context.Background(), "http://registry.example"); err == nil {
		t.Fatal("invalid Docker configuration was overwritten")
	}
}
