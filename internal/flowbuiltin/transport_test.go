package flowbuiltin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPTransportChoicesRemainIndependent(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	plain := httptest.NewServer(handler)
	defer plain.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	r := NewBuiltinRunner(BuiltinRunnerOptions{WorkspaceRoot: t.TempDir()})
	for _, test := range []struct {
		url, skip string
		ok        bool
	}{{plain.URL, "", true}, {secure.URL, "", false}, {secure.URL, "true", true}, {secure.URL, "false", false}} {
		_, err := r.Run(context.Background(), "card", "http-request", map[string]string{"url": test.url, "insecure_skip_verify": test.skip})
		if (err == nil) != test.ok {
			t.Fatalf("skip=%q err=%v", test.skip, err)
		}
	}
	trusted := NewBuiltinRunner(BuiltinRunnerOptions{WorkspaceRoot: t.TempDir(), HTTPClient: secure.Client()})
	if _, err := trusted.Run(context.Background(), "card", "http-request", map[string]string{"url": secure.URL}); err != nil {
		t.Fatal(err)
	}
}
