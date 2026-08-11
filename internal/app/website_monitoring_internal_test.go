package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"scriptboard/internal/websitemonitor"
)

func TestRemoteWebsiteMonitorEndpointValidationAndRedirectProtection(t *testing.T) {
	for _, invalid := range []string{
		"file:///state/app.db",
		"http://example.com/trigger?name=websites",
		"https://user:password@example.com/trigger?name=websites",
		"https://example.com/trigger",
		"https://example.com/trigger?name=websites#secret",
	} {
		if _, err := normalizeRemoteWebsiteEndpoint(invalid); err == nil {
			t.Fatalf("invalid endpoint accepted: %s", invalid)
		}
	}
	var redirectedAuthorization string
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirectedAuthorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"action":"website_monitor","schema_version":1,"data":{"monitors":[],"alerts":[],"counts":{},"total":0,"needsCare":0}}`))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := fetchRemoteWebsiteMonitors(context.Background(), redirect.URL, "sbk_secret", localeEnglishUS); err == nil {
		t.Fatal("redirected website monitor response was accepted")
	}
	if redirectedAuthorization != "" {
		t.Fatalf("Bearer key leaked across redirect: %q", redirectedAuthorization)
	}
}

func TestWebsiteSecuritySummaryDistinguishesImminentAndExpiredCertificates(t *testing.T) {
	checkedAt := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	monitor := websitemonitor.Monitor{
		Config: websitemonitor.Config{URL: "https://status.example/"},
		Latest: websitemonitor.Evidence{
			CheckedAt: checkedAt,
			Certificate: websitemonitor.Certificate{
				NotAfter:      checkedAt.Add(6 * time.Hour),
				DaysRemaining: 0,
				Verified:      true,
			},
		},
	}
	tone, title, _ := websiteSecuritySummary(localeSimplifiedChinese, monitor)
	if tone != "warning" || title != "证书将在 24 小时内到期" {
		t.Fatalf("imminent certificate summary = %q %q", tone, title)
	}

	monitor.Latest.Certificate.NotAfter = checkedAt.Add(-time.Minute)
	tone, title, _ = websiteSecuritySummary(localeSimplifiedChinese, monitor)
	if tone != "danger" || title != "证书已过期" {
		t.Fatalf("expired certificate summary = %q %q", tone, title)
	}
}
