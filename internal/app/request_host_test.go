package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowedHostsRejectHostHeaderInjectionAndUnknownHosts(t *testing.T) {
	t.Parallel()
	application := &App{allowedHosts: map[string]struct{}{"panel.example": {}}}
	for _, host := range []string{"evil.example", "panel.example@evil.example", "panel.example\r\nX-Test: injected"} {
		if application.validRequestHost(host) {
			t.Errorf("host accepted: %q", host)
		}
	}
	if !application.validRequestHost("PANEL.EXAMPLE.:8787") {
		t.Fatal("normalized allowed host was rejected")
	}
}

func TestTrustedForwardedHostMustStillBeAllowed(t *testing.T) {
	t.Parallel()
	_, trusted, err := net.ParseCIDR("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	application := &App{
		trustedProxies: []*net.IPNet{trusted},
		allowedHosts:   map[string]struct{}{"panel.example": {}},
	}
	request := httptest.NewRequest(http.MethodPost, "http://internal.invalid/settings/locale", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("X-Forwarded-Host", "panel.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Origin", "https://panel.example")
	request = application.applyTrustedProxy(request)
	if request.Host != "panel.example" || !application.validRequestHost(request.Host) || !validRequestOrigin(request) {
		t.Fatalf("trusted proxy origin was not normalized safely: host=%q secure=%v", request.Host, isSecureRequest(request))
	}

	request = httptest.NewRequest(http.MethodGet, "http://internal.invalid/assets/app.css", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("X-Forwarded-Host", "evil.example")
	request = application.applyTrustedProxy(request)
	if application.validRequestHost(request.Host) {
		t.Fatal("trusted proxy bypassed the allowed host list")
	}
}

func FuzzNormalizeHTTPHost(f *testing.F) {
	for _, seed := range []string{"localhost", "127.0.0.1:8787", "[::1]:8787", "panel.example", "evil\r\nHost: panel"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		host, err := normalizeHTTPHost(value)
		if err == nil && host == "" {
			t.Fatal("successful normalization returned an empty host")
		}
	})
}
