package web_test

import (
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/hostfiles"
)

func TestRunsPageLinksEveryRecordBackToItsScriptDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	directoryName := "ops #1"
	if err := os.MkdirAll(filepath.Join(hostRoot, directoryName), 0o755); err != nil {
		t.Fatalf("create script directory: %v", err)
	}

	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	db, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	for _, fixture := range []struct {
		id, scriptPath string
	}{
		{id: "nested-run", scriptPath: filepath.Join(hostRoot, directoryName, "inspect.cmd")},
		{id: "root-run", scriptPath: filepath.Join(hostRoot, "root.cmd")},
	} {
		if _, err := db.Exec(`INSERT INTO runs
			(id, script_path, script_path_key, script_sha256, arguments_template, template_arguments_json, arguments_json,
			 executor, source_type, source_name, runtime_identity, status, created_at, timeout_seconds, log_path)
			VALUES (?, ?, ?, 'digest', '', '[]', '[]', 'cmd.exe', 'admin/manual', 'manual', 'test',
			 'succeeded', ?, 0, '')`,
			fixture.id, fixture.scriptPath, hostfiles.ComparisonKey(fixture.scriptPath), time.Now().UnixNano()); err != nil {
			t.Fatalf("insert %s: %v", fixture.id, err)
		}
	}

	runsPath := "/history/runs"
	response, err := client.Get(serverURL + runsPath)
	if err != nil {
		t.Fatalf("get runs page: %v", err)
	}
	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		runsPath = "/monitor/runs"
		response, err = client.Get(serverURL + runsPath)
		if err != nil {
			t.Fatalf("get runs page fallback: %v", err)
		}
	}
	runPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read runs page: %v", err)
	}
	runHTML := string(runPage)
	for _, expected := range []string{
		`data-script-directory-link`,
		`data-lucide="folder-open"`,
		`href="` + strings.ReplaceAll(hostFilesHrefWithQuery(filepath.Join(hostRoot, directoryName), nil), "+", "&#43;") + `"`,
		`href="` + hostFilesHrefWithQuery(hostRoot, nil) + `"`,
		`Open script directory`,
	} {
		if !strings.Contains(runHTML, expected) {
			t.Fatalf("runs page does not contain %q: %s", expected, runHTML)
		}
	}
	if count := strings.Count(runHTML, `data-script-directory-link`); count != 2 {
		t.Fatalf("script directory link count=%d, want one for each of 2 records: %s", count, runHTML)
	}

	response, err = client.Get(hostFilesRequestURL(serverURL, filepath.Join(hostRoot, directoryName)))
	if err != nil {
		t.Fatalf("open script directory: %v", err)
	}
	directoryPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read script directory: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("script directory status=%d body=%s", response.StatusCode, directoryPage)
	}
}
