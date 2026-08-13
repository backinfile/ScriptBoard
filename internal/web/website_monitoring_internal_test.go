package web

import (
	"testing"
	"time"

	"scriptboard/internal/websitemonitor"
)

func TestRemoteWebsiteMonitorEndpointAndPayloadValidation(t *testing.T) {
	for _, invalid := range []string{
		"file:///state/app.db",
		"http://example.com/trigger/monitoring/websites",
		"https://user:password@example.com/trigger/monitoring/websites",
		"https://example.com/trigger",
		"https://example.com/trigger/monitoring/websites#secret",
	} {
		if _, err := normalizeRemoteWebsiteEndpoint(invalid); err == nil {
			t.Fatalf("invalid endpoint accepted: %s", invalid)
		}
	}
	if _, err := decodeRemoteWebsiteMonitors([]byte(`{"ok":true,"action":"wrong","schema_version":1,"data":{}}`)); err == nil {
		t.Fatal("invalid remote website payload was accepted")
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
