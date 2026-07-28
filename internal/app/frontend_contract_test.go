package app_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/app"
)

func TestShellStatusEndpointReturnsAuthenticatedNoStoreVerdict(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/monitor/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	var payload struct {
		State       string `json:"state"`
		CollectedAt string `json:"collectedAt"`
		IssueCount  int    `json:"issueCount"`
		ActiveRuns  int    `json:"activeRuns"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	if cache := response.Header.Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache control=%q, want no-store", cache)
	}
	if payload.State != "current" && payload.State != "attention" && payload.State != "stale" {
		t.Fatalf("state=%q", payload.State)
	}
	if payload.CollectedAt == "" {
		t.Fatal("collectedAt is empty")
	}
}

func TestPJAXPageReturnsBusinessDocumentWithoutApplicationShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))

	fullResponse, err := client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatal(err)
	}
	fullBody, _ := io.ReadAll(fullResponse.Body)
	_ = fullResponse.Body.Close()
	if fullResponse.StatusCode != http.StatusOK {
		t.Fatalf("full page status=%d, want %d", fullResponse.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(fullBody), `class="app-sidebar"`) || !strings.Contains(string(fullBody), `data-app-shell`) {
		t.Fatalf("ordinary GET does not contain the application shell: %s", fullBody)
	}

	request, err := http.NewRequest(http.MethodGet, serverURL+"/resources/variables", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-ScriptBoard-Navigation", "pjax")
	request.Header.Set("Accept", "text/html")
	pjaxResponse, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	pjaxBody, _ := io.ReadAll(pjaxResponse.Body)
	_ = pjaxResponse.Body.Close()
	if pjaxResponse.StatusCode != http.StatusOK {
		t.Fatalf("PJAX status=%d, want %d", pjaxResponse.StatusCode, http.StatusOK)
	}
	page := string(pjaxBody)
	for _, expected := range []string{`<title>`, `lang="en-US"`, `<main id="main-content"`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("PJAX response is missing %q: %s", expected, page)
		}
	}
	if strings.Contains(page, `class="app-sidebar"`) || strings.Contains(page, `data-app-shell`) {
		t.Fatalf("PJAX response contains a duplicate application shell: %s", page)
	}
}

func TestApplicationShellAndStatusEndpointShareFiveSecondSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), stateRoot)

	pageResponse, err := client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, pageResponse.Body)
	_ = pageResponse.Body.Close()
	if pageResponse.StatusCode != http.StatusOK {
		t.Fatalf("page status=%d, want %d", pageResponse.StatusCode, http.StatusOK)
	}

	db, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO runs
		(id, script_path, script_sha256, arguments_template, arguments_json, executor, source_type, status, created_at, error, log_path)
		VALUES ('cached-shell-status', 'job.cmd', 'digest', '', '[]', 'cmd.exe', 'manual', 'running', 1, '', '')`); err != nil {
		t.Fatal(err)
	}

	statusResponse, err := client.Get(serverURL + "/monitor/status")
	if err != nil {
		t.Fatal(err)
	}
	defer statusResponse.Body.Close()
	var status shellStatusPayload
	if err := json.NewDecoder(statusResponse.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want %d", statusResponse.StatusCode, http.StatusOK)
	}
	if status.ActiveRuns != 0 {
		t.Fatalf("activeRuns=%d, want the application shell's cached value 0", status.ActiveRuns)
	}
	if cache := statusResponse.Header.Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache control=%q, want no-store", cache)
	}
}

type shellStatusPayload struct {
	State       string `json:"state"`
	CollectedAt string `json:"collectedAt"`
	IssueCount  int    `json:"issueCount"`
	ActiveRuns  int    `json:"activeRuns"`
}

func TestAuthenticatedNavigationUsesGroupedMonitorRoute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Get(serverURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor" {
		t.Fatalf("root status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `data-host-overview`) {
		t.Fatalf("monitor status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.Get(serverURL + "/overview")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy overview status=%d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func TestLoginNegotiatesSupportedWebLocale(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	for _, test := range []struct {
		name, language, langAttribute, heading string
	}{
		{name: "English", language: "en-US,en;q=0.9", langAttribute: `lang="en-US"`, heading: ">Sign in<"},
		{name: "Simplified Chinese", language: "zh-CN,zh;q=0.9,en;q=0.5", langAttribute: `lang="zh-CN"`, heading: ">登录<"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+"/login", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept-Language", test.language)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK ||
				!strings.Contains(string(body), test.langAttribute) ||
				!strings.Contains(string(body), test.heading) {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
		})
	}
}

func TestLocalePreferenceCookieOverridesBrowserLanguage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept-Language", "zh-CN")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	response, err = client.PostForm(server.URL+"/settings/locale", url.Values{
		"locale":     {"en-US"},
		"return_to":  {"/login"},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("locale status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	request, err = http.NewRequest(http.MethodGet, server.URL+"/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept-Language", "zh-CN")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), `lang="en-US"`) || !strings.Contains(string(body), ">Sign in<") {
		t.Fatalf("locale cookie was not applied: %s", body)
	}
}

func TestGroupedWebRoutesReplaceLegacyModulePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))

	for _, path := range []string{
		"/monitor/runs",
		"/resources/files/",
		"/resources/variables",
		"/resources/trash",
		"/config/quick-runs",
		"/config/schedules",
		"/history/audit",
		"/settings/account",
		"/settings/version-protection",
	} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("%s status=%d, want %d", path, response.StatusCode, http.StatusOK)
		}
	}

	for _, path := range []string{
		"/overview",
		"/runs",
		"/files/",
		"/variables",
		"/trash",
		"/quick-runs",
		"/schedules",
		"/audit",
	} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get legacy %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("legacy %s status=%d, want %d", path, response.StatusCode, http.StatusNotFound)
		}
	}
}

func TestCreateAndEditTasksHaveSemanticGETRoutes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	for _, path := range []string{
		"/resources/files/new-directory",
		"/resources/files/upload",
		"/resources/variables/new",
		"/config/schedules/new",
	} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK ||
			!strings.Contains(string(body), `data-task-page`) ||
			!strings.Contains(string(body), `data-task-close-label="Close"`) {
			t.Errorf("%s status=%d body=%s", path, response.StatusCode, body)
		}
	}
}

func TestAuthenticatedPagesExposeLocalizedGroupedApplicationShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	page := string(body)

	for _, expected := range []string{
		`lang="en-US"`,
		`class="app-sidebar"`,
		`class="brand-wordmark"`,
		`>Monitor<`,
		`>Resources<`,
		`>Configuration<`,
		`>History<`,
		`href="/monitor/runs"`,
		`href="/resources/files/"`,
		`href="/resources/variables"`,
		`href="/config/quick-runs"`,
		`href="/config/schedules"`,
		`href="/history/audit"`,
		`href="/settings/account"`,
		`action="/settings/locale"`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("shell does not contain %q: %s", expected, page)
		}
	}
	if strings.Contains(page, `class="app-header"`) || strings.Contains(page, `class="brand__mark"`) {
		t.Fatalf("legacy shell remains in page: %s", page)
	}
	if got := response.Header.Get("Content-Language"); got != "en-US" {
		t.Fatalf("content language=%q, want en-US", got)
	}
}

func TestRunsNavigationLivesInHistoryGroup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/monitor/runs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	page := string(body)

	monitorStart := strings.Index(page, "<h2>Monitor</h2>")
	resourcesStart := strings.Index(page, "<h2>Resources</h2>")
	historyStart := strings.Index(page, "<h2>History</h2>")
	runsLink := strings.Index(page, `href="/monitor/runs" aria-current="page"`)
	auditLink := strings.Index(page, `href="/history/audit"`)
	if monitorStart < 0 || resourcesStart <= monitorStart || historyStart <= resourcesStart || runsLink <= historyStart || auditLink <= runsLink {
		t.Fatalf("runs navigation is not ordered under History: %s", page)
	}
	if strings.Contains(page[monitorStart:resourcesStart], `href="/monitor/runs"`) {
		t.Fatalf("runs navigation remains in Monitor: %s", page[monitorStart:resourcesStart])
	}
}

func TestPrimaryWorkspacesRenderCompleteLocalizedDocuments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	for _, path := range []string{
		"/monitor",
		"/monitor/runs",
		"/resources/files/",
		"/resources/variables",
		"/resources/trash",
		"/config/quick-runs",
		"/config/schedules",
		"/history/audit",
		"/settings/account",
		"/settings/version-protection",
	} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		page := string(body)
		if response.StatusCode != http.StatusOK || !strings.Contains(page, "</html>") {
			t.Errorf("%s produced an incomplete document: status=%d body=%s", path, response.StatusCode, page)
		}
		if strings.Contains(page, "overview.title") || strings.Contains(page, "common.") {
			t.Errorf("%s contains an untranslated message key: %s", path, page)
		}
	}
}

func TestEachWorkspaceAndTaskExposeAtMostOnePrimaryAction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	for _, path := range []string{
		"/monitor",
		"/monitor/runs",
		"/resources/files/",
		"/resources/files/new-directory",
		"/resources/files/upload",
		"/resources/variables",
		"/resources/variables/new",
		"/resources/trash",
		"/config/quick-runs",
		"/config/quick-runs/groups/new",
		"/config/schedules",
		"/config/schedules/new",
		"/config/schedules/groups/new",
		"/history/audit",
		"/settings/account",
		"/settings/version-protection",
	} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d, want %d", path, response.StatusCode, http.StatusOK)
		}
		if count := strings.Count(string(body), "button--primary"); count > 1 {
			t.Errorf("%s renders %d primary actions, want at most one", path, count)
		}
	}
}
