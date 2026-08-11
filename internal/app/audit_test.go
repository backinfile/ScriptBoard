package app_test

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditRecordsActionWithoutVariableValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatalf("get variables: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	const sensitiveValue = "must-not-appear-in-audit"
	response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
		"name":       {"AUDITED"},
		"value":      {sensitiveValue},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("create variable: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/history/audit")
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "create_variable") || !strings.Contains(string(body), "AUDITED") {
		t.Fatalf("audit event missing: status=%d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), sensitiveValue) {
		t.Fatalf("variable value leaked into audit: %s", body)
	}
}

func TestAuditPageAndCSVDefensivelyRedactLegacySecrets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), stateRoot)
	db, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const secret = "legacy-audit-password"
	if _, err := db.Exec(`INSERT INTO audit_events (occurred_at, action, target, result, source_address)
		VALUES (?, 'legacy_secret_test', ?, 'succeeded', 'local')`, time.Now().Unix(), "password="+secret); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/history/audit", "/history/audit.csv"} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK || strings.Contains(string(body), secret) || !strings.Contains(string(body), "[REDACTED]") {
			t.Fatalf("audit output %s was not redacted: status=%d body=%s", path, response.StatusCode, body)
		}
	}
}

func TestAuditPageFiltersByInclusiveLocalDateRange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), stateRoot)
	db, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	insert := func(action string, occurredAt time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO audit_events (occurred_at, action, target, result, source_address)
			VALUES (?, ?, 'date-range-test', 'succeeded', 'test')`, occurredAt.Unix(), action); err != nil {
			t.Fatalf("insert %s: %v", action, err)
		}
	}
	localTime := func(year int, month time.Month, day, hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, time.Local)
	}
	insert("range_marker_before", localTime(2026, time.January, 9, 23, 59))
	insert("range_marker_start", localTime(2026, time.January, 10, 0, 0))
	for index := 0; index < 19; index++ {
		insert(fmt.Sprintf("range_marker_middle_%02d", index), localTime(2026, time.January, 11, 12, index))
	}
	insert("range_marker_end", localTime(2026, time.January, 12, 23, 59))
	insert("range_marker_after", localTime(2026, time.January, 13, 0, 0))

	response, err := client.Get(serverURL + "/history/audit?q=range_marker&from=2026-01-10&to=2026-01-12")
	if err != nil {
		t.Fatalf("get filtered audit: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read filtered audit: %v", err)
	}
	html := string(body)
	for _, marker := range []string{
		`class="history-filter-form"`,
		`name="q" value="range_marker"`,
		`name="from" value="2026-01-10"`,
		`name="to" value="2026-01-12"`,
		`range_marker_end`,
		`21 records`,
		`href="?from=2026-01-10&amp;page=2&amp;q=range_marker&amp;to=2026-01-12"`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("filtered audit page is missing %q: %s", marker, html)
		}
	}
	for _, excluded := range []string{"range_marker_before", "range_marker_after"} {
		if strings.Contains(html, excluded) {
			t.Fatalf("filtered audit page contains excluded event %q: %s", excluded, html)
		}
	}

	response, err = client.Get(serverURL + "/history/audit?q=range_marker&from=2026-01-10&to=2026-01-12&page=2")
	if err != nil {
		t.Fatalf("get second filtered audit page: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read second filtered audit page: %v", err)
	}
	if !strings.Contains(string(body), "range_marker_start") {
		t.Fatalf("inclusive start date event is missing from second page: %s", body)
	}
}

func TestAuditPageRejectsInvalidDateRanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	tests := []struct {
		query, message string
	}{
		{query: "from=not-a-date", message: "The date range is invalid"},
		{query: "from=2026-01-12&to=2026-01-10", message: "The start date cannot be later"},
	}
	for _, test := range tests {
		response, err := client.Get(serverURL + "/history/audit?" + test.query)
		if err != nil {
			t.Fatalf("get audit with %s: %v", test.query, err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatalf("read audit with %s: %v", test.query, err)
		}
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), test.message) {
			t.Fatalf("query %q status=%d body=%s", test.query, response.StatusCode, body)
		}
	}
}
