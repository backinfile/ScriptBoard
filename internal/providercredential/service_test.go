package providercredential

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceKeepsCredentialInsideBoundProxySession(t *testing.T) {
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		_, _ = io.WriteString(response, `{"ok":true}`)
	}))
	defer upstream.Close()
	service, err := New(Options{StateRoot: t.TempDir(), SessionLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	record := Record{ID: "model-1", OwnerUserID: "owner-1", Provider: "openai-compatible", Model: "fixture-model", Endpoint: upstream.URL + "/v1"}
	if err := service.Store(context.Background(), "owner-1", record, "real-provider-secret"); err != nil {
		t.Fatal(err)
	}
	session, err := service.Start(context.Background(), "owner-1", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, session.Endpoint+"/chat/completions", strings.NewReader(`{"model":"fixture-model"}`))
	request.Header.Set("Authorization", "Bearer "+session.Capability)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || authorization != "Bearer real-provider-secret" {
		t.Fatalf("status=%d upstream authorization=%q", response.StatusCode, authorization)
	}
	if strings.Contains(session.Endpoint+session.Capability+session.Handle, "real-provider-secret") {
		t.Fatal("provider session exposed credential")
	}
	if err := service.Stop(context.Background(), session.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := http.DefaultClient.Do(request); err == nil {
		t.Fatal("stopped provider session remained reachable")
	}
}

func TestServiceBindsOwnerAndSharedVisibility(t *testing.T) {
	service, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	record := Record{ID: "private", OwnerUserID: "owner", Provider: "openai", Model: "gpt-test", Endpoint: "https://api.openai.com/v1"}
	if err := service.Store(context.Background(), "owner", record, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(context.Background(), "other", record.ID); err != ErrForbidden {
		t.Fatalf("private start error=%v", err)
	}
	record.Shared = true
	if err := service.Store(context.Background(), "owner", record, ""); err != nil {
		t.Fatal(err)
	}
	session, err := service.Start(context.Background(), "other", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = service.Stop(context.Background(), session.Handle)
	if err := service.Delete(context.Background(), "other", record.ID); err != ErrForbidden {
		t.Fatalf("cross-owner delete error=%v", err)
	}
}

func TestServiceEncryptsCredentialAndRejectsBindingChangesByOtherOwner(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	record := Record{ID: "model", OwnerUserID: "owner", Provider: "anthropic", Model: "claude-test", Endpoint: "https://api.anthropic.com"}
	if err := service.Store(context.Background(), "owner", record, "secret-never-plaintext"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "secrets", storeFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-never-plaintext") || strings.Contains(string(body), "api.anthropic.com") {
		t.Fatal("provider record was written in plaintext")
	}
	record.OwnerUserID = "other"
	if err := service.Store(context.Background(), "other", record, "replacement"); err != ErrForbidden {
		t.Fatalf("owner replacement error=%v", err)
	}
}
