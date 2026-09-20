package web_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// dashboardActionFixture 搭建带三类操作按钮（公开 quick_run、公开 browser_http、
// 非公开 quick_run）的面板与已发布的快捷执行项。
type dashboardActionFixture struct {
	client     *http.Client
	serverURL  string
	stateRoot  string
	slug       string
	dashboard  string
	card       string
	quickRun   string
	actionsArg string
}

func setupDashboardActionFixture(t *testing.T) dashboardActionFixture {
	t.Helper()
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	state := filepath.Join(root, "state")
	client, base := authenticatedClient(t, managed, state)

	name := "dashboard-action.sh"
	source := "#!/bin/sh\necho dashboard-action-ok\n"
	if runtime.GOOS == "windows" {
		name = "dashboard-action.cmd"
		source = "@echo dashboard-action-ok\r\n"
	}
	scriptPath := filepath.Join(managed, name)
	if err := os.WriteFile(scriptPath, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(base + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	response, err = client.PostForm(base+"/config/quick-runs", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"面板操作"}, "script": {scriptPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	db, err := sql.Open("sqlite", filepath.Join(state, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var quickRunID, scriptSHA string
	if err := db.QueryRow("SELECT id,script_sha256 FROM quick_runs WHERE name='面板操作'").Scan(&quickRunID, &scriptSHA); err != nil || scriptSHA == "" {
		t.Fatalf("quick run unpublished: id=%q sha=%q err=%v", quickRunID, scriptSHA, err)
	}

	response, err = client.PostForm(base+"/config/dashboards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"操作面板"}, "slug": {"action-board"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	dashboardURL, _ := url.Parse(response.Header.Get("Location"))
	dashboardID := dashboardURL.Query().Get("dashboard")
	if dashboardID == "" {
		t.Fatal("created dashboard id missing")
	}
	response, err = client.Get(base + response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()

	actionsJSON, err := json.Marshal([]map[string]any{
		{"id": "act-restart", "label": "重启服务", "kind": "quick_run", "quickRunId": quickRunID, "publicAllowed": true},
		{"id": "act-hook", "label": "触发钩子", "kind": "browser_http", "method": "POST", "url": "https://example.test/hook", "visitorCredential": "Authorization", "publicAllowed": true},
		{"id": "act-internal", "label": "内部操作", "kind": "quick_run", "quickRunId": quickRunID, "publicAllowed": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.PostForm(base+"/config/dashboards/"+dashboardID+"/cards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"服务状态"}, "type": {"number"},
		"source_url": {"https://api.example.test/status"}, "value_path": {"value"},
		"refresh_seconds": {"300"}, "actions_json": {string(actionsJSON)},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create card status=%d", response.StatusCode)
	}
	response, err = client.Get(base + "/config/dashboards?dashboard=" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	cardMatch := regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)"`).FindStringSubmatch(string(page))
	if len(cardMatch) != 2 {
		t.Fatalf("card id missing: %s", page)
	}
	if !strings.Contains(string(page), "3 actions") || !strings.Contains(string(page), "2 public actions") {
		t.Fatalf("action count badge missing: %s", page)
	}
	return dashboardActionFixture{
		client: client, serverURL: base, stateRoot: state, slug: "action-board",
		dashboard: dashboardID, card: cardMatch[1], quickRun: quickRunID, actionsArg: string(actionsJSON),
	}
}

func dashboardActionToken(t *testing.T, body []byte) string {
	t.Helper()
	match := regexp.MustCompile(`data-csrf-token="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("monitor csrf token missing: %s", body)
	}
	return string(match[1])
}

func TestCustomDashboardCardActionTrigger(t *testing.T) {
	fixture := setupDashboardActionFixture(t)
	triggerURL := fixture.serverURL + "/config/dashboard-cards/" + fixture.card + "/actions/act-restart/trigger"

	viewer := createRoleUserClient(t, fixture.client, fixture.serverURL, "action-viewer", "viewer")
	response, err := viewer.PostForm(triggerURL, url.Values{"csrf_token": {"invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer trigger status=%d", response.StatusCode)
	}

	operator := createRoleUserClient(t, fixture.client, fixture.serverURL, "action-operator", "operator")
	response, err = operator.Get(fixture.serverURL + "/monitor/dashboard/" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Count(string(page), "data-dashboard-action-trigger") != 3 {
		t.Fatalf("monitor page should render all three actions: %s", page)
	}
	token := dashboardActionToken(t, page)

	response, err = operator.PostForm(triggerURL, url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("trigger without csrf status=%d", response.StatusCode)
	}

	response, err = operator.PostForm(triggerURL, url.Values{"csrf_token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("operator trigger status=%d body=%s", response.StatusCode, body)
	}
	var started struct {
		RunID  string `json:"runId"`
		RunURL string `json:"runUrl"`
	}
	if err := json.Unmarshal(body, &started); err != nil || started.RunID == "" || !strings.HasPrefix(started.RunURL, "/history/runs/") {
		t.Fatalf("trigger payload=%s err=%v", body, err)
	}

	for _, actionID := range []string{"act-hook", "act-missing"} {
		response, err = operator.PostForm(fixture.serverURL+"/config/dashboard-cards/"+fixture.card+"/actions/"+actionID+"/trigger", url.Values{"csrf_token": {token}})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s trigger status=%d, want 404", actionID, response.StatusCode)
		}
	}
}

func publicDashboardTrigger(t *testing.T, serverURL, slug, card, actionID, key string) (int, []byte) {
	t.Helper()
	values := url.Values{}
	if key != "" {
		values.Set("key", key)
	}
	response, err := http.PostForm(serverURL+"/public/dashboard/"+slug+"/cards/"+card+"/actions/"+actionID+"/trigger", values)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return response.StatusCode, body
}

func setDashboardVisibility(t *testing.T, fixture dashboardActionFixture, visibility string) (int, []byte) {
	t.Helper()
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	response, err = fixture.client.PostForm(fixture.serverURL+"/config/dashboards/"+fixture.dashboard+"/visibility", url.Values{
		"csrf_token": {formToken(t, page)}, "visibility": {visibility},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return response.StatusCode, body
}

func oneTimeDashboardKey(t *testing.T, body []byte) string {
	t.Helper()
	match := regexp.MustCompile(`data-dashboard-onetime-key>([^<]+)<`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("one-time key missing: %s", body)
	}
	return string(match[1])
}

func TestPublicDashboardCardActionTrigger(t *testing.T) {
	fixture := setupDashboardActionFixture(t)

	status, body := setDashboardVisibility(t, fixture, "public_operate")
	if status != http.StatusOK {
		t.Fatalf("visibility switch should render one-time key page, status=%d", status)
	}
	key := oneTimeDashboardKey(t, body)

	response, err := http.Get(fixture.serverURL + "/public/dashboard/" + fixture.slug)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	rendered := string(page)
	if strings.Count(rendered, "data-dashboard-action-trigger") != 2 ||
		!strings.Contains(rendered, `data-action-id="act-restart"`) || !strings.Contains(rendered, `data-action-id="act-hook"`) {
		t.Fatalf("public_operate page actions mismatch: %s", page)
	}
	if strings.Contains(rendered, fixture.quickRun) || strings.Contains(rendered, "act-internal") {
		t.Fatal("public page leaked quickRunId or non-public action")
	}

	if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", ""); status != http.StatusNotFound {
		t.Fatalf("trigger without key status=%d", status)
	}
	if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", "wrong-key"); status != http.StatusNotFound {
		t.Fatalf("trigger with wrong key status=%d", status)
	}
	status, body = publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", key)
	if status != http.StatusOK || !strings.Contains(string(body), `"runId"`) {
		t.Fatalf("trigger with key status=%d body=%s", status, body)
	}
	for _, actionID := range []string{"act-hook", "act-internal"} {
		if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, actionID, key); status != http.StatusNotFound {
			t.Fatalf("%s public trigger status=%d, want 404", actionID, status)
		}
	}

	status, _ = setDashboardVisibility(t, fixture, "public_read")
	if status != http.StatusSeeOther {
		t.Fatalf("public_read switch status=%d", status)
	}
	response, err = http.Get(fixture.serverURL + "/public/dashboard/" + fixture.slug)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(page), "data-dashboard-action-trigger") {
		t.Fatal("public_read page must not render action buttons")
	}
	if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", key); status != http.StatusNotFound {
		t.Fatalf("public_read trigger status=%d", status)
	}

	status, _ = setDashboardVisibility(t, fixture, "anonymous_operate")
	if status != http.StatusSeeOther {
		t.Fatalf("anonymous_operate switch status=%d", status)
	}
	response, err = http.Get(fixture.serverURL + "/public/dashboard/" + fixture.slug)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	rendered = string(page)
	if !strings.Contains(rendered, `data-action-id="act-hook"`) || strings.Contains(rendered, `data-action-id="act-restart"`) {
		t.Fatalf("anonymous_operate page should only render browser_http actions: %s", page)
	}
	if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", ""); status != http.StatusNotFound {
		t.Fatalf("anonymous_operate server trigger status=%d", status)
	}
}

func TestCustomDashboardAccessKeyRotation(t *testing.T) {
	fixture := setupDashboardActionFixture(t)
	status, body := setDashboardVisibility(t, fixture, "public_operate")
	if status != http.StatusOK {
		t.Fatalf("visibility switch status=%d", status)
	}
	oldKey := oneTimeDashboardKey(t, body)

	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(page), "dk_") {
		t.Fatal("access key hint missing on dashboard page")
	}

	response, err = fixture.client.PostForm(fixture.serverURL+"/config/dashboards/"+fixture.dashboard+"/access-key/rotate", url.Values{
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("rotate status=%d", response.StatusCode)
	}
	newKey := oneTimeDashboardKey(t, rotated)
	if newKey == oldKey {
		t.Fatal("rotate returned the same key")
	}
	if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", oldKey); status != http.StatusNotFound {
		t.Fatalf("old key after rotate status=%d", status)
	}
	if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", newKey); status != http.StatusOK {
		t.Fatalf("new key after rotate status=%d", status)
	}

	response, err = fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	response, err = fixture.client.PostForm(fixture.serverURL+"/config/dashboards/"+fixture.dashboard+"/access-key/revoke", url.Values{
		"csrf_token": {formToken(t, page)}, "confirm": {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke status=%d", response.StatusCode)
	}
	if status, _ := publicDashboardTrigger(t, fixture.serverURL, fixture.slug, fixture.card, "act-restart", newKey); status != http.StatusNotFound {
		t.Fatalf("revoked key status=%d", status)
	}
}

func TestCustomDashboardCardActionsValidation(t *testing.T) {
	fixture := setupDashboardActionFixture(t)
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	token := formToken(t, page)

	post := func(actions string, want int) {
		t.Helper()
		result, postErr := fixture.client.PostForm(fixture.serverURL+"/config/dashboards/"+fixture.dashboard+"/cards", url.Values{
			"csrf_token": {token}, "name": {"校验"}, "type": {"number"},
			"source_url": {"https://api.example.test/v"}, "value_path": {"value"},
			"refresh_seconds": {"300"}, "actions_json": {actions},
		})
		if postErr != nil {
			t.Fatal(postErr)
		}
		result.Body.Close()
		if result.StatusCode != want {
			t.Fatalf("actions_json=%s status=%d, want %d", actions, result.StatusCode, want)
		}
	}
	post(`not-json`, http.StatusUnprocessableEntity)
	post(`{"label":"x"}`, http.StatusUnprocessableEntity)
	post(`[{"id":"b1","label":"坏地址","kind":"browser_http","method":"GET","url":"ftp://example.test/x"}]`, http.StatusUnprocessableEntity)
	post(`[{"id":"b2","label":"缺失执行项","kind":"quick_run","quickRunId":"missing-quick-run"}]`, http.StatusUnprocessableEntity)
	post(`[{"id":"ok1","label":"浏览器钩子","kind":"browser_http","method":"POST","url":"https://example.test/hook"}]`, http.StatusSeeOther)
}

func TestPublicDashboardStatusIsScopedAndRevoked(t *testing.T) {
	f := setupDashboardActionFixture(t)
	_, body := setDashboardVisibility(t, f, "public_operate")
	key := oneTimeDashboardKey(t, body)
	status, body := publicDashboardTrigger(t, f.serverURL, f.slug, f.card, "act-restart", key)
	if status != 200 {
		t.Fatalf("trigger %d %s", status, body)
	}
	var result map[string]string
	json.Unmarshal(body, &result)
	check := func(key, run string, want int) {
		t.Helper()
		response, err := http.PostForm(f.serverURL+result["statusUrl"], url.Values{"key": {key}, "run_id": {run}})
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("status=%d want=%d body=%s", response.StatusCode, want, data)
		}
		if want == 200 && (!strings.Contains(string(data), "status") || strings.Contains(string(data), "scriptPath")) {
			t.Fatalf("unexpected status payload: %s", data)
		}
	}
	check(key, result["runId"], 200)
	check("wrong", result["runId"], 404)
	check(key, "unrelated", 404)
	setDashboardVisibility(t, f, "public_read")
	check(key, result["runId"], 404)
}
