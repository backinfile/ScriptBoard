package web_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/logstream"
	"scriptboard/internal/servicelogs"
	app "scriptboard/internal/web"
)

func TestServiceLogsPageAndExportKeepFixedFiltersAndRedaction(t *testing.T) {
	t.Parallel()
	fixture := &serviceLogsFixture{report: servicelogs.Report{
		Supported: true, Provider: "Windows System Event Log", CollectedAt: time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC),
		Entries: []servicelogs.Entry{{Time: time.Date(2026, 8, 12, 7, 59, 0, 0, time.UTC), Service: "runner", Severity: logstream.SeverityError, EventID: "7036", Source: "Service Control Manager", Message: "runner failed token=<redacted>"}},
	}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), ServiceLogs: fixture})
	page := getSecurityPage(t, client, serverURL+"/history/audit/service-logs?service=runner&range=7d&severity=error&q=failed")
	for _, expected := range [][]byte{[]byte("Service logs"), []byte("Windows System Event Log"), []byte("Run Worker"), []byte("7036"), []byte("runner failed token=&lt;redacted&gt;"), []byte("scans at most 2,000 entries and returns 500"), []byte(`class="run-log-section service-log-console"`), []byte(`/history/audit/service-logs.txt?`)} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("service logs page missing %q: %s", expected, page)
		}
	}
	for _, expected := range [][]byte{[]byte(`class="audit-source-tabs"`), []byte(`href="/history/audit"`), []byte(`href="/history/audit/service-logs" aria-current="page"`)} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("service logs are not integrated into the audit workspace: missing %q", expected)
		}
	}
	fixture.mu.Lock()
	query := fixture.query
	fixture.mu.Unlock()
	if query.Service != "runner" || query.Range != "7d" || query.Severity != logstream.SeverityError || query.Search != "failed" {
		t.Fatalf("service log query = %#v", query)
	}

	response, err := client.Get(serverURL + "/history/audit/service-logs.csv?service=runner&range=7d&severity=error&q=failed")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("service logs export status=%d err=%v body=%s", response.StatusCode, err, body)
	}
	if !strings.Contains(string(body), "runner failed token=<redacted>") || strings.Contains(string(body), "super-secret-value") {
		t.Fatalf("service logs CSV = %s", body)
	}
	response, err = client.Get(serverURL + "/history/audit/service-logs.txt?service=runner&range=7d&severity=error&q=failed")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Disposition"), ".txt") || !strings.Contains(string(body), "runner failed token=<redacted>") {
		t.Fatalf("service logs TXT status=%d disposition=%q err=%v body=%s", response.StatusCode, response.Header.Get("Content-Disposition"), err, body)
	}
	legacy, err := client.Get(serverURL + "/settings/service-logs?service=runner")
	if err != nil {
		t.Fatal(err)
	}
	_ = legacy.Body.Close()
	if legacy.StatusCode != http.StatusPermanentRedirect || legacy.Header.Get("Location") != "/history/audit/service-logs?service=runner" {
		t.Fatalf("legacy service logs route status=%d location=%q", legacy.StatusCode, legacy.Header.Get("Location"))
	}
}

type serviceLogsFixture struct {
	mu     sync.Mutex
	report servicelogs.Report
	query  servicelogs.Query
}

func (fixture *serviceLogsFixture) List(_ context.Context, query servicelogs.Query) (servicelogs.Report, error) {
	fixture.mu.Lock()
	fixture.query = query
	fixture.mu.Unlock()
	return fixture.report, nil
}
