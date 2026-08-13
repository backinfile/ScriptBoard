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

	app "scriptboard/internal/web"
	"scriptboard/internal/logstream"
	"scriptboard/internal/servicelogs"
)

func TestServiceLogsPageAndExportKeepFixedFiltersAndRedaction(t *testing.T) {
	t.Parallel()
	fixture := &serviceLogsFixture{report: servicelogs.Report{
		Supported: true, Provider: "Windows System Event Log", CollectedAt: time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC),
		Entries: []servicelogs.Entry{{Time: time.Date(2026, 8, 12, 7, 59, 0, 0, time.UTC), Service: "runner", Severity: logstream.SeverityError, EventID: "7036", Source: "Service Control Manager", Message: "runner failed token=<redacted>"}},
	}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), ServiceLogs: fixture})
	page := getSecurityPage(t, client, serverURL+"/settings/service-logs?service=runner&range=7d&severity=error&q=failed")
	for _, expected := range [][]byte{[]byte("Service logs"), []byte("Windows System Event Log"), []byte("Run Worker"), []byte("7036"), []byte("runner failed token=&lt;redacted&gt;"), []byte("scans at most 2,000 entries and returns 500")} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("service logs page missing %q: %s", expected, page)
		}
	}
	fixture.mu.Lock()
	query := fixture.query
	fixture.mu.Unlock()
	if query.Service != "runner" || query.Range != "7d" || query.Severity != logstream.SeverityError || query.Search != "failed" {
		t.Fatalf("service log query = %#v", query)
	}

	response, err := client.Get(serverURL + "/settings/service-logs/export?service=runner&range=7d&severity=error&q=failed")
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
