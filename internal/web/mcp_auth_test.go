package web_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "scriptboard/internal/web"
)

func TestMCPUnauthenticatedRequestAdvertisesOAuthDiscovery(t *testing.T) {
	t.Parallel()
	enabled := true
	application, err := app.Open(app.Config{StateRoot: t.TempDir(), MCPEnabled: &enabled, CanonicalExternalURL: "https://panel.example", AllowedHosts: []string{"panel.example"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	request.Host = "panel.example"
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	want := `Bearer resource_metadata="https://panel.example/.well-known/oauth-protected-resource", scope="scriptboard.observe"`
	if got := response.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate=%q want=%q", got, want)
	}
	if strings.Contains(strings.ToLower(response.Header().Get("Content-Type")), "text/html") {
		t.Fatalf("unauthenticated MCP response is HTML: %q", response.Header().Get("Content-Type"))
	}
}

func TestMCPProtectedResourceMetadataUsesCanonicalResource(t *testing.T) {
	t.Parallel()
	enabled := true
	application, err := app.Open(app.Config{StateRoot: t.TempDir(), MCPEnabled: &enabled, CanonicalExternalURL: "https://panel.example/base", AllowedHosts: []string{"panel.example"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	request := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	request.Host = "panel.example"
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var metadata struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Resource != "https://panel.example/base/mcp" {
		t.Fatalf("resource=%q", metadata.Resource)
	}
	if len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != "https://panel.example/base" {
		t.Fatalf("authorization_servers=%v", metadata.AuthorizationServers)
	}
}

func TestMCPDisabledDoesNotRegisterProtocolRoutes(t *testing.T) {
	t.Parallel()
	disabled := false
	application, err := app.Open(app.Config{StateRoot: t.TempDir(), MCPEnabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	for _, path := range []string{"/mcp", "/.well-known/oauth-protected-resource", "/oauth/token"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "localhost"
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d, want 404", path, response.Code)
		}
	}
}

func TestOAuthAuthorizationResumesAfterLogin(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	enabled := true
	application, err := app.Open(app.Config{StateRoot: stateRoot, MCPEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authorizePath := "/oauth/authorize?response_type=code&client_id=test&redirect_uri=https%3A%2F%2Fclient.example%2Fcallback&scope=scriptboard.observe&state=s&code_challenge=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&code_challenge_method=S256&resource=%2Fmcp"
	response, err := client.Get(server.URL + authorizePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("authorize redirect status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	loginPage, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(loginPage.Body)
	_ = loginPage.Body.Close()
	password, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.PostForm(server.URL+"/login", url.Values{"username": {"admin"}, "password": {strings.TrimSpace(string(password))}, "csrf_token": {formToken(t, body)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != authorizePath {
		t.Fatalf("login did not resume OAuth: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}
