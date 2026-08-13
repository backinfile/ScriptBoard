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
