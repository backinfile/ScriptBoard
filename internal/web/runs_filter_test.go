package web_test

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/hostfiles"
)

func TestRunsPageFiltersBySearchAndInclusiveLocalDateRange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	hostRoot := filepath.Join(root, "host")
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	scriptPath := filepath.Join(hostRoot, "automation", "nightly.ps1")
	db := openConcurrentAppTestDatabase(t, filepath.Join(stateRoot, "app.db"))

	insertSource := func(id, sourceType, sourceName, sourceID string, createdAt time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO runs
			(id, script_path, script_path_key, script_sha256, arguments_template, template_arguments_json, arguments_json,
			 executor, source_type, source_name, source_id, runtime_identity, status, created_at, timeout_seconds, log_path)
			VALUES (?, ?, ?, 'digest', '', '[]', '[]',
			 'pwsh', ?, ?, ?, 'test', 'succeeded', ?, 0, '')`,
			id, scriptPath, hostfiles.ComparisonKey(scriptPath), sourceType, sourceName, sourceID, createdAt.UnixNano()); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert := func(id string, createdAt time.Time) {
		insertSource(id, "scheduler", "Nightly backup", "schedule-id-1", createdAt)
	}
	localTime := func(year int, month time.Month, day, hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, time.Local)
	}
	insert("range_run_before", localTime(2026, time.January, 9, 23, 59))
	insert("range_run_start", localTime(2026, time.January, 10, 0, 0))
	for index := 0; index < 19; index++ {
		insert(fmt.Sprintf("range_run_middle_%02d", index), localTime(2026, time.January, 11, 12, index))
	}
	insert("range_run_end", localTime(2026, time.January, 12, 23, 59))
	insert("range_run_after", localTime(2026, time.January, 13, 0, 0))
	insertSource("range_run_renamed", "scheduler", "Former nightly backup", "schedule-id-1", localTime(2026, time.January, 11, 12, 30))
	insertSource("range_run_similar_name", "scheduler", "Nightly backup extended", "schedule-id-2", localTime(2026, time.January, 11, 13, 0))
	insertSource("range_run_same_source_quick", "admin/quick-run", "Nightly backup", "schedule-id-1", localTime(2026, time.January, 11, 14, 0))
	insertSource("range_run_other_schedule", "scheduler", "Nightly backup", "schedule-id-2", localTime(2026, time.January, 11, 15, 0))

	response, err := client.Get(serverURL + "/history/runs?q=Nightly+backup&schedule_id=schedule-id-1&from=2026-01-10&to=2026-01-12&focus=search")
	if err != nil {
		t.Fatalf("get filtered runs: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read filtered runs: %v", err)
	}
	html := string(body)
	for _, marker := range []string{
		`class="history-filter-form"`,
		`id="run-search"`,
		`name="q" value="Nightly backup"`,
		`name="schedule_id" value="schedule-id-1"`,
		`name="from" value="2026-01-10"`,
		`name="to" value="2026-01-12"`,
		`autofocus`,
		`range_run_end`,
		`range_run_renamed`,
		`22 records`,
		`href="?focus=search&amp;from=2026-01-10&amp;page=2&amp;q=Nightly&#43;backup&amp;schedule_id=schedule-id-1&amp;to=2026-01-12"`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("filtered runs page is missing %q: %s", marker, html)
		}
	}
	for _, excluded := range []string{"range_run_before", "range_run_after", "range_run_similar_name", "range_run_same_source_quick", "range_run_other_schedule"} {
		if strings.Contains(html, excluded) {
			t.Fatalf("filtered runs page contains excluded Run %q: %s", excluded, html)
		}
	}

	response, err = client.Get(serverURL + "/history/runs?q=Nightly+backup&schedule_id=schedule-id-1&from=2026-01-10&to=2026-01-12&page=2")
	if err != nil {
		t.Fatalf("get second filtered runs page: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read second filtered runs page: %v", err)
	}
	if !strings.Contains(string(body), "range_run_start") {
		t.Fatalf("inclusive start date Run is missing from second page: %s", body)
	}
}

func TestRunsPageFiltersByQuickRunID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	hostRoot := filepath.Join(root, "host")
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	scriptPath := filepath.Join(hostRoot, "automation", "deploy.ps1")
	db := openConcurrentAppTestDatabase(t, filepath.Join(stateRoot, "app.db"))

	insert := func(id, sourceType, sourceID string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO runs
			(id, script_path, script_path_key, script_sha256, arguments_template, template_arguments_json, arguments_json,
			 executor, source_type, source_name, source_id, runtime_identity, status, created_at, timeout_seconds, log_path)
			VALUES (?, ?, ?, 'digest', '', '[]', '[]',
			 'pwsh', ?, 'Deploy production', ?, 'test', 'succeeded', ?, 0, '')`,
			id, scriptPath, hostfiles.ComparisonKey(scriptPath), sourceType, sourceID, time.Now().UTC().UnixNano()); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("matching_admin", "admin/quick-run", "quick-run-1")
	insert("matching_assistant", "assistant/quick-run", "quick-run-1")
	insert("matching_legacy", "quick_run", "quick-run-1")
	insert("different_quick_run", "admin/quick-run", "quick-run-2")
	insert("same_id_schedule", "scheduler", "quick-run-1")

	response, err := client.Get(serverURL + "/history/runs?q=Deploy+production&quick_run_id=quick-run-1&focus=search")
	if err != nil {
		t.Fatalf("get Quick Run history: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read Quick Run history: %v", err)
	}
	html := string(body)
	for _, marker := range []string{
		`name="q" value="Deploy production"`,
		`name="quick_run_id" value="quick-run-1"`,
		`data-quick-run-filter-name="Deploy production"`,
		`matching_admin`,
		`matching_assistant`,
		`matching_legacy`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("Quick Run history is missing %q: %s", marker, html)
		}
	}
	for _, excluded := range []string{"different_quick_run", "same_id_schedule"} {
		if strings.Contains(html, excluded) {
			t.Fatalf("Quick Run history contains unrelated Run %q: %s", excluded, html)
		}
	}
}

func TestRunsPageShowsAndSearchesInitiator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	hostRoot := filepath.Join(root, "host")
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	scriptPath := filepath.Join(hostRoot, "automation", "nightly.ps1")
	db := openConcurrentAppTestDatabase(t, filepath.Join(stateRoot, "app.db"))

	insertRun := func(id, initiatorUserID, initiatorUsername string, createdAt time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO runs
			(id, script_path, script_path_key, script_sha256, arguments_template, template_arguments_json, arguments_json,
			 executor, source_type, source_name, source_id, runtime_identity, status, created_at, timeout_seconds,
			 log_path, initiated_by_user_id, initiated_by_username)
			VALUES (?, ?, ?, 'digest', '', '[]', '[]',
			 'pwsh', 'scheduler', 'Nightly backup', 'schedule-id-1', 'test', 'succeeded', ?, 0,
			 '', ?, ?)`,
			id, scriptPath, hostfiles.ComparisonKey(scriptPath), createdAt.UnixNano(), initiatorUserID, initiatorUsername); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insertRun("actor_run", "user-maintainer-jade", "maintainer-jade", time.Now().UTC())
	insertRun("system_run", "", "", time.Now().UTC().Add(-time.Minute))

	response, err := client.Get(serverURL + "/history/runs?q=maintainer-jade")
	if err != nil {
		t.Fatalf("search runs by initiator: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read initiator search: %v", err)
	}
	page := string(body)
	for _, expected := range []string{`<th>Actor</th>`, `data-label="Actor"`, `class="runs-actor"`, "Script, source, status, actor, executor, or Run ID", "actor_run", "maintainer-jade"} {
		if !strings.Contains(page, expected) {
			t.Fatalf("initiator search is missing %q: %s", expected, page)
		}
	}
	if strings.Contains(page, "system_run") {
		t.Fatalf("initiator search contains unrelated scheduled Run: %s", page)
	}

	response, err = client.Get(serverURL + "/history/runs")
	if err != nil {
		t.Fatalf("get all runs: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read all runs: %v", err)
	}
	if !strings.Contains(string(body), "System schedule") {
		t.Fatalf("scheduled Run does not identify its system initiator: %s", body)
	}
}

func TestRunsPageRejectsInvalidDateRanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	tests := []struct {
		query, message string
	}{
		{query: "from=not-a-date", message: "The date range is invalid"},
		{query: "from=2026-01-12&to=2026-01-10", message: "The start date cannot be later"},
		{query: "from=1500-01-01", message: "The date range is invalid"},
	}
	for _, test := range tests {
		response, err := client.Get(serverURL + "/history/runs?" + test.query)
		if err != nil {
			t.Fatalf("get runs with %s: %v", test.query, err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatalf("read runs with %s: %v", test.query, err)
		}
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), test.message) {
			t.Fatalf("query %q status=%d body=%s", test.query, response.StatusCode, body)
		}
	}
}

func openConcurrentAppTestDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test SQLite database: %v", err)
	}
	db.SetMaxOpenConns(1)
	// The running app and the test connection can write the same WAL at once.
	// Wait out the app's short transaction instead of failing with SQLITE_BUSY.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		t.Fatalf("configure test SQLite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
