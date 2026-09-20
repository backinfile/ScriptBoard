package web_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 面板导入导出扩展：actions 的 quickRunId 与 flow 的 runId 是本机 ID，
// 导出附带名称、导入按名称重映射；未匹配的 action 降级丢弃，flow 未匹配拒绝导入。

func transferRemapFixture(t *testing.T) (dashboardFlowFixture, string) {
	t.Helper()
	fixture := createFlowFixture(t, false)
	db, err := sql.Open("sqlite", filepath.Join(fixture.state, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var quickRunID string
	if err := db.QueryRow("SELECT id FROM quick_runs WHERE name='flow-build'").Scan(&quickRunID); err != nil {
		t.Fatal(err)
	}
	return fixture, quickRunID
}

func createTransferCards(t *testing.T, fixture dashboardFlowFixture, quickRunID string) {
	t.Helper()
	// 卡片 A：数值卡 + quick_run 操作 + browser_http 操作。
	actions, err := json.Marshal([]map[string]any{
		{"id": "act-restart", "label": "重启服务", "kind": "quick_run", "quickRunId": quickRunID, "publicAllowed": true},
		{"id": "act-hook", "label": "触发钩子", "kind": "browser_http", "method": "POST", "url": "https://example.test/hook"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := fixture.client.PostForm(fixture.serverURL+"/config/dashboards/"+fixture.dashboard+"/cards", url.Values{
		"csrf_token": {fixture.formPageToken(t)}, "name": {"操作卡"}, "type": {"number"},
		"source_url": {"https://api.example.test/v"}, "value_path": {"value"}, "refresh_seconds": {"300"},
		"actions_json": {string(actions)},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create action card status=%d", response.StatusCode)
	}
	// 卡片 B：flow 卡。
	status, _ := fixture.createFlowCard(t, flowFixtureYAML)
	if status != http.StatusSeeOther {
		t.Fatalf("create flow card status=%d", status)
	}
}

func exportTransferBundle(t *testing.T, fixture dashboardFlowFixture) map[string]any {
	t.Helper()
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	cardIDPattern := regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)"`)
	cardIDs := []string{}
	for _, match := range cardIDPattern.FindAllStringSubmatch(string(page), -1) {
		cardIDs = append(cardIDs, match[1])
	}
	if len(cardIDs) != 2 {
		t.Fatalf("expected 2 cards, got %v", cardIDs)
	}
	values := url.Values{}
	for _, id := range cardIDs {
		values.Add("selection", id)
	}
	response, err = fixture.client.Get(fixture.serverURL + "/config/dashboards/" + fixture.dashboard + "/export?" + values.Encode())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export status=%d body=%s", response.StatusCode, body)
	}
	var bundle map[string]any
	if err := json.Unmarshal(body, &bundle); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func importTransferBundle(t *testing.T, fixture dashboardFlowFixture, dashboardID string, bundle map[string]any) *http.Response {
	t.Helper()
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("csrf_token", formToken(t, page))
	_ = writer.WriteField("dashboard_id", dashboardID)
	part, err := writer.CreateFormFile("dashboard_file", "dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(encoded)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, fixture.serverURL+"/config/dashboards/import", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func createSecondDashboard(t *testing.T, fixture dashboardFlowFixture, slug string) string {
	t.Helper()
	response, err := fixture.client.PostForm(fixture.serverURL+"/config/dashboards", url.Values{
		"csrf_token": {fixture.formPageToken(t)}, "name": {"导入目标"}, "slug": {slug},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	target, _ := url.Parse(response.Header.Get("Location"))
	id := target.Query().Get("dashboard")
	if id == "" {
		t.Fatal("second dashboard id missing")
	}
	return id
}

func importedCardConfig(t *testing.T, fixture dashboardFlowFixture, dashboardID, cardName string) map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(fixture.state, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var config string
	if err := db.QueryRow("SELECT config_json FROM custom_dashboard_cards WHERE dashboard_id=? AND name=?", dashboardID, cardName).Scan(&config); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(config), &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestCustomDashboardTransferRemapsQuickRunRefs(t *testing.T) {
	fixture, quickRunID := transferRemapFixture(t)
	createTransferCards(t, fixture, quickRunID)
	bundle := exportTransferBundle(t, fixture)

	// 导出注解：quick_run 操作带 quickRunName；flow 节点/post 带 run 名称。
	nodes, _ := bundle["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("bundle nodes=%v", nodes)
	}
	var actionCard, flowCard map[string]any
	for _, raw := range nodes {
		card := raw.(map[string]any)
		switch card["name"] {
		case "操作卡":
			actionCard = card
		case "发布流程":
			flowCard = card
		}
	}
	if actionCard == nil || flowCard == nil {
		t.Fatalf("bundle cards missing: %v", nodes)
	}
	actionConfig := actionCard["config"].(map[string]any)
	actions := actionConfig["actions"].([]any)
	firstAction := actions[0].(map[string]any)
	if firstAction["quickRunName"] != "flow-build" || firstAction["quickRunId"] != quickRunID {
		t.Fatalf("exported action=%v", firstAction)
	}
	flowConfig := flowCard["config"].(map[string]any)["flow"].(map[string]any)
	if flowConfig["nodes"].([]any)[0].(map[string]any)["run"] != "flow-build" {
		t.Fatalf("exported flow=%v", flowConfig)
	}

	// 跨实例模拟：抹掉本机 ID，只留名称，导入后应重映射回本机 ID。
	target := createSecondDashboard(t, fixture, "import-target")
	tampered := deepCopyBundle(t, bundle)
	for _, raw := range tampered["nodes"].([]any) {
		card := raw.(map[string]any)
		config, ok := card["config"].(map[string]any)
		if !ok {
			continue
		}
		if actions, ok := config["actions"].([]any); ok {
			for _, action := range actions {
				entry := action.(map[string]any)
				if _, isQuickRun := entry["quickRunId"]; isQuickRun {
					entry["quickRunId"] = "qr-foreign"
				}
			}
		}
		if flow, ok := config["flow"].(map[string]any); ok {
			for _, key := range []string{"nodes", "post"} {
				for _, entry := range flow[key].([]any) {
					entry.(map[string]any)["runId"] = "qr-foreign"
				}
			}
		}
	}
	response := importTransferBundle(t, fixture, target, tampered)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || strings.Contains(response.Header.Get("Location"), "import_error") {
		t.Fatalf("remap import status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	importedAction := importedCardConfig(t, fixture, target, "操作卡")
	importedActions := importedAction["actions"].([]any)
	if importedActions[0].(map[string]any)["quickRunId"] != quickRunID {
		t.Fatalf("imported action not remapped: %v", importedActions[0])
	}
	if _, leaked := importedActions[0].(map[string]any)["quickRunName"]; leaked {
		t.Fatal("quickRunName annotation leaked into stored config")
	}
	importedFlow := importedCardConfig(t, fixture, target, "发布流程")["flow"].(map[string]any)
	for _, key := range []string{"nodes", "post"} {
		for _, entry := range importedFlow[key].([]any) {
			if entry.(map[string]any)["runId"] == "qr-foreign" || entry.(map[string]any)["runId"] == "" {
				t.Fatalf("flow %s not remapped: %v", key, entry)
			}
		}
	}

	// 未匹配的 action 名称：降级丢弃该操作，browser_http 保留。
	target2 := createSecondDashboard(t, fixture, "import-target-2")
	unmatched := deepCopyBundle(t, bundle)
	for _, raw := range unmatched["nodes"].([]any) {
		card := raw.(map[string]any)
		config, ok := card["config"].(map[string]any)
		if !ok {
			continue
		}
		if actions, ok := config["actions"].([]any); ok {
			for _, action := range actions {
				entry := action.(map[string]any)
				if _, isQuickRun := entry["quickRunId"]; isQuickRun {
					entry["quickRunId"] = "qr-foreign"
					entry["quickRunName"] = "no-such-run"
				}
			}
		}
	}
	response = importTransferBundle(t, fixture, target2, unmatched)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || strings.Contains(response.Header.Get("Location"), "import_error") {
		t.Fatalf("degraded import status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	degraded := importedCardConfig(t, fixture, target2, "操作卡")["actions"].([]any)
	if len(degraded) != 1 || degraded[0].(map[string]any)["kind"] != "browser_http" {
		t.Fatalf("unmatched action should be dropped: %v", degraded)
	}

	// 未匹配的 flow run 名称：整个导入拒绝（DAG 不允许缺节点）。
	target3 := createSecondDashboard(t, fixture, "import-target-3")
	broken := deepCopyBundle(t, bundle)
	for _, raw := range broken["nodes"].([]any) {
		card := raw.(map[string]any)
		config, ok := card["config"].(map[string]any)
		if !ok {
			continue
		}
		if flow, ok := config["flow"].(map[string]any); ok {
			for _, entry := range flow["nodes"].([]any) {
				entry.(map[string]any)["runId"] = "qr-foreign"
				entry.(map[string]any)["run"] = "no-such-run"
			}
		}
	}
	response = importTransferBundle(t, fixture, target3, broken)
	response.Body.Close()
	if !strings.Contains(response.Header.Get("Location"), "import_error=failed") {
		t.Fatalf("broken flow import location=%q, want import_error=failed", response.Header.Get("Location"))
	}
}

func deepCopyBundle(t *testing.T, bundle map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var copied map[string]any
	if err := json.Unmarshal(encoded, &copied); err != nil {
		t.Fatal(err)
	}
	return copied
}
