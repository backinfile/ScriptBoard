package remotewebsite

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/secretstore"
)

func TestStoreBindsCredentialToEndpointAndFetchNeverReturnsCredential(t *testing.T) {
	var authorization string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"action":"website_monitor","schema_version":1,"data":{"monitors":[],"total":0}}`))
	}))
	defer upstream.Close()
	root := t.TempDir()
	vault, err := secretstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{StateRoot: root, SecretStore: vault, Client: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	key := "sbk_0123456789abcdef." + strings.Repeat("a", 43)
	if err := service.Store(context.Background(), "source-one", upstream.URL+"/trigger?name=website-status", key); err != nil {
		t.Fatal(err)
	}
	payload, err := service.Fetch(context.Background(), "source-one", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer "+key || bytes.Contains(payload, []byte(key)) {
		t.Fatalf("authorization=%q payload=%s", authorization, payload)
	}
	body, err := os.ReadFile(filepath.Join(root, "secrets", "remote-website-connections.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(key)) || bytes.Contains(body, []byte(upstream.URL)) {
		t.Fatal("sealed connection store contains plaintext endpoint or credential")
	}
}

func TestStoreRejectsUnboundOrInvalidConnectionFields(t *testing.T) {
	service, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	validKey := "sbk_0123456789abcdef." + strings.Repeat("a", 43)
	for _, test := range []struct{ id, endpoint, key string }{
		{"", "https://example.com/trigger?name=status", validKey},
		{"source-one", "http://example.com/trigger?name=status", validKey},
		{"source-one", "https://example.com/trigger", validKey},
		{"source-one", "https://example.com/trigger?name=status", "bad"},
	} {
		if err := service.Store(context.Background(), test.id, test.endpoint, test.key); err == nil {
			t.Fatalf("accepted invalid connection: %+v", test)
		}
	}
	if _, err := service.Fetch(context.Background(), strings.Repeat("a", 161), "en-US"); err == nil {
		t.Fatal("accepted oversized connection ID")
	}
}

func TestFetchDoesNotForwardCredentialAcrossRedirect(t *testing.T) {
	var redirectedAuthorization string
	target := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirectedAuthorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"action":"website_monitor","schema_version":1,"data":{}}`))
	}))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL+"/trigger?name=status", http.StatusFound)
	}))
	defer redirect.Close()
	client := redirect.Client()
	client.Transport = trustBothServersTransport(t, redirect, target)
	service, err := New(Options{StateRoot: t.TempDir(), Client: client})
	if err != nil {
		t.Fatal(err)
	}
	key := "sbk_0123456789abcdef." + strings.Repeat("a", 43)
	if err := service.Store(context.Background(), "source-one", redirect.URL+"/trigger?name=status", key); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Fetch(context.Background(), "source-one", "en-US"); err == nil {
		t.Fatal("redirected response was accepted")
	}
	if redirectedAuthorization != "" {
		t.Fatalf("credential leaked across redirect: %q", redirectedAuthorization)
	}
}

func trustBothServersTransport(t *testing.T, servers ...*httptest.Server) http.RoundTripper {
	t.Helper()
	pool := x509.NewCertPool()
	for _, server := range servers {
		pool.AddCert(server.Certificate())
	}
	return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
}
