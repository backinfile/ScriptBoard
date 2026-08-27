package mcpserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
	"scriptboard/internal/mcpaccess"
)

type fakeBackend struct{}

func (fakeBackend) HostStatus(context.Context) (any, error) {
	return map[string]any{"stale": false}, nil
}
func (fakeBackend) ListQuickRuns(context.Context, string, int) (QuickRunPage, error) {
	return QuickRunPage{}, nil
}
func (fakeBackend) GetRun(context.Context, string) (any, error) { return map[string]any{}, nil }
func (fakeBackend) GetRunLogs(context.Context, string, string, int) (RunLogPage, error) {
	return RunLogPage{}, nil
}
func (fakeBackend) StartQuickRun(context.Context, mcpaccess.Principal, StartQuickRunInput) (any, error) {
	return map[string]any{"run_id": "r1"}, nil
}
func (fakeBackend) StopRun(context.Context, mcpaccess.Principal, StopRunInput) (any, error) {
	return map[string]any{"status": "stopped"}, nil
}

type bearerTransport struct{ token string }

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(copy)
}

func issuedAccessToken(t *testing.T, role string, scopes []string) (*mcpaccess.Store, string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+role+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,username TEXT NOT NULL,role TEXT NOT NULL,enabled INTEGER NOT NULL,auth_version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range mcpaccess.SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users VALUES('u1','user',?,1,1)`, role); err != nil {
		t.Fatal(err)
	}
	store := mcpaccess.NewStore(db, nil)
	client, err := store.RegisterClient(context.Background(), "test", []string{"https://client.example/callback"}, "dcr", "")
	if err != nil {
		t.Fatal(err)
	}
	verifier := "a-valid-verifier-value-with-more-than-forty-three-characters"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	code, err := store.IssueCode(context.Background(), "u1", client.ClientID, "https://client.example/callback", "https://panel.example/mcp", challenge, scopes)
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.ExchangeCode(context.Background(), code, client.ClientID, "https://client.example/callback", "https://panel.example/mcp", verifier)
	if err != nil {
		t.Fatal(err)
	}
	return store, set.AccessToken
}

func toolNames(t *testing.T, store *mcpaccess.Store, token string) map[string]bool {
	t.Helper()
	server := httptest.NewServer(New(store, fakeBackend{}, "https://panel.example/mcp"))
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerTransport{token: token}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]bool{}
	for _, tool := range listed.Tools {
		result[tool.Name] = true
	}
	return result
}

func TestToolCatalogueIsFilteredByCurrentScope(t *testing.T) {
	store, token := issuedAccessToken(t, "viewer", []string{mcpaccess.ScopeObserve})
	names := toolNames(t, store, token)
	if len(names) != 4 || names["scriptboard.start_quick_run"] || names["scriptboard.stop_run"] {
		t.Fatalf("viewer tools=%v", names)
	}
	store2, token2 := issuedAccessToken(t, "operator", []string{mcpaccess.ScopeExecute})
	names = toolNames(t, store2, token2)
	if len(names) != 6 || !names["scriptboard.start_quick_run"] || !names["scriptboard.stop_run"] {
		t.Fatalf("operator tools=%v", names)
	}
}
