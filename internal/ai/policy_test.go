package ai

import (
	"net/http"
	"testing"
)

func TestEffectivePermissionIncludesQueryForSideEffects(t *testing.T) {
	effective := EffectivePermission(
		Permission{Query: true, Execute: true, Modify: true},
		Permission{Execute: true},
		Permission{Query: true, Execute: true, Modify: true},
	)
	if !effective.Query || !effective.Execute || effective.Modify {
		t.Fatalf("effective permission = %#v", effective)
	}
}

func TestValidateEndpointAllowsHTTPSAndLoopbackHTTP(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.example.com/v1",
		"http://127.0.0.1:11434/v1",
		"http://[::1]:8080",
		"http://localhost:8080",
	} {
		if err := ValidateEndpoint(endpoint); err != nil {
			t.Errorf("ValidateEndpoint(%q): %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{
		"http://api.example.com/v1",
		"file:///tmp/model",
		"https://user:password@example.com",
	} {
		if err := ValidateEndpoint(endpoint); err == nil {
			t.Errorf("ValidateEndpoint(%q) succeeded", endpoint)
		}
	}
}

func TestValidateExtraHeadersRejectsTransportHeaders(t *testing.T) {
	if err := ValidateExtraHeaders(map[string]string{"anthropic-beta": "feature"}); err != nil {
		t.Fatalf("ordinary header rejected: %v", err)
	}
	for _, name := range []string{"Host", "Content-Length", "Connection", "Transfer-Encoding", "Upgrade", "Proxy-Authorization"} {
		if err := ValidateExtraHeaders(map[string]string{name: "value"}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSameOriginRedirectPolicy(t *testing.T) {
	client := SecureHTTPClient(map[string]string{"Authorization": "Bearer override"})
	request, _ := http.NewRequest(http.MethodGet, "https://example.com/v1", nil)
	same, _ := http.NewRequest(http.MethodGet, "https://example.com/next", nil)
	if err := client.CheckRedirect(same, []*http.Request{request}); err != nil {
		t.Fatalf("same-origin redirect rejected: %v", err)
	}
	cross, _ := http.NewRequest(http.MethodGet, "https://other.example/next", nil)
	if err := client.CheckRedirect(cross, []*http.Request{request}); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
}
