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
	request, err = application.applyTrustedProxy(request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Host != "panel.example" || !application.validRequestHost(request.Host) || !validRequestOrigin(request) {
		t.Fatalf("trusted proxy origin was not normalized safely: host=%q secure=%v", request.Host, isSecureRequest(request))
	}

	request = httptest.NewRequest(http.MethodGet, "http://internal.invalid/assets/app.css", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("X-Forwarded-Host", "evil.example")
	request, err = application.applyTrustedProxy(request)
	if err != nil {
		t.Fatal(err)
	}
	if application.validRequestHost(request.Host) {
		t.Fatal("trusted proxy bypassed the allowed host list")
	}
}

func TestTrustedProxyRejectsAmbiguousMalformedAndOversizedChains(t *testing.T) {
	t.Parallel()
	_, trusted, err := net.ParseCIDR("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	application := &App{trustedProxies: []*net.IPNet{trusted}}
	tests := []struct{ name, header, value string }{
		{name: "standard-forwarded", header: "Forwarded", value: "for=198.51.100.1;proto=https"},
		{name: "invalid-address", header: "X-Forwarded-For", value: "198.51.100.1, unknown"},
		{name: "invalid-proto", header: "X-Forwarded-Proto", value: "https, ftp"},
		{name: "invalid-host", header: "X-Forwarded-Host", value: "panel.example, user@evil.example"},
		{name: "chain-too-long", header: "X-Forwarded-For", value: "1.1.1.1,2.2.2.2,3.3.3.3,4.4.4.4,5.5.5.5,6.6.6.6,7.7.7.7,8.8.8.8,9.9.9.9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://panel.example/login", nil)
			request.RemoteAddr = "127.0.0.1:50000"
			request.Header.Set(test.header, test.value)
			if _, err := application.applyTrustedProxy(request); err == nil {
				t.Fatal("unsafe proxy chain was accepted")
			}
		})
	}
	t.Run("duplicate-header", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "http://panel.example/login", nil)
		request.RemoteAddr = "127.0.0.1:50000"
		request.Header.Add("X-Forwarded-For", "198.51.100.1")
		request.Header.Add("X-Forwarded-For", "198.51.100.2")
		if _, err := application.applyTrustedProxy(request); err == nil {
			t.Fatal("duplicate forwarded header was accepted")
		}
	})
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
