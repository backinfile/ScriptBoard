package web_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	app "scriptboard/internal/web"
)

func TestTrustedProxySecurityBoundaryRunsBeforeApplicationHandlers(t *testing.T) {
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), TrustedProxies: []string{"127.0.0.1"},
		AllowedHosts: []string{"panel.example"}, CanonicalExternalURL: "https://panel.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/login", nil)
	request.Host = "internal.invalid"
	request.Header.Set("X-Forwarded-For", "198.51.100.24, 127.0.0.1")
	request.Header.Set("X-Forwarded-Host", "panel.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatalf("status = %d, HSTS = %q", response.StatusCode, response.Header.Get("Strict-Transport-Security"))
	}
	if !hasCookieAttributes(response.Header.Values("Set-Cookie"), "Secure", "HttpOnly", "SameSite=Strict") {
		t.Fatalf("trusted HTTPS proxy did not produce a hardened login cookie: %#v", response.Header.Values("Set-Cookie"))
	}

	request, _ = http.NewRequest(http.MethodGet, server.URL+"/login", nil)
	request.Host = "internal.invalid"
	request.Header.Set("X-Forwarded-Host", "evil.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("forwarded Host reached application handler: status = %d", response.StatusCode)
	}
}

func TestUntrustedPeerCannotSpoofForwardedHTTPS(t *testing.T) {
	application, err := app.Open(app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/login", nil)
	request.Header.Set("Forwarded", "for=198.51.100.24;host=evil.example;proto=https")
	request.Header.Set("X-Forwarded-For", "198.51.100.24")
	request.Header.Set("X-Forwarded-Host", "evil.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Strict-Transport-Security") != "" {
		t.Fatalf("untrusted proxy headers changed transport security: status = %d, HSTS = %q", response.StatusCode, response.Header.Get("Strict-Transport-Security"))
	}
	if hasCookieAttributes(response.Header.Values("Set-Cookie"), "Secure") {
		t.Fatalf("untrusted proxy headers produced a Secure cookie on HTTP: %#v", response.Header.Values("Set-Cookie"))
	}
}

func hasCookieAttributes(headers []string, attributes ...string) bool {
	for _, header := range headers {
		matches := true
		for _, attribute := range attributes {
			if !strings.Contains(header, attribute) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
