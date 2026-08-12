package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCustomDashboardCanBeExportedAndImported(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/dashboards")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"迁移测试"}, "slug": {"transfer-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	dashboardLocation := response.Header.Get("Location")
	dashboardURL, _ := url.Parse(dashboardLocation)
	dashboardID := dashboardURL.Query().Get("dashboard")
	if dashboardID == "" {
		t.Fatal("created dashboard id missing")
	}
	response, err = client.Get(serverURL + dashboardLocation)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"服务额度"}, "type": {"quota"},
		"source_url": {"https://api.example.test/usage"}, "value_path": {"usage.used"},
		"secondary_path": {"usage.remaining"}, "unit": {"GB"}, "refresh_seconds": {"300"},
		"headers": {"Authorization: Bearer test-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"请求次数"}, "type": {"number"},
		"source_url": {"https://api.example.test/requests"}, "value_path": {"metrics.count"},
		"unit": {"次"}, "refresh_seconds": {"60"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = client.Get(serverURL + dashboardLocation)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	cardMatches := regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)"`).FindAllStringSubmatch(string(page), -1)
	if len(cardMatches) != 2 {
		t.Fatalf("expected two selectable cards, got %d", len(cardMatches))
	}
	if rendered := string(page); !strings.Contains(rendered, `data-dashboard-drawer-name="export"`) || !strings.Contains(rendered, `data-dashboard-import-selection`) || strings.Count(rendered, `name="selection"`) != 2 ||
		!strings.Contains(rendered, `data-dashboard-open-drawer="export"><span data-lucide="upload"`) ||
		!strings.Contains(rendered, `data-dashboard-open-drawer="import"><span data-lucide="download"`) ||
		!strings.Contains(rendered, `data-lucide="upload" aria-hidden="true"></span>导出所选节点`) ||
		!strings.Contains(rendered, `data-lucide="download" aria-hidden="true"></span>导入所选节点`) ||
		!strings.Contains(rendered, "导入到当前面板") || strings.Contains(rendered, "创建一个新的私有面板") {
		t.Fatal("dashboard node transfer UI is incomplete")
	}
	postExport := func(selections ...string) (*http.Response, []byte) {
		t.Helper()
		values := url.Values{"selection": selections}
		result, exportErr := client.Get(serverURL + "/config/dashboards/" + dashboardID + "/export?" + values.Encode())
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		body, _ := io.ReadAll(result.Body)
		result.Body.Close()
		return result, body
	}
	noSelectionResponse, _ := postExport()
	if noSelectionResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty export selection status=%d", noSelectionResponse.StatusCode)
	}
	response, exported := postExport(cardMatches[0][1])
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export status=%d disposition=%q", response.StatusCode, response.Header.Get("Content-Disposition"))
	}
	if strings.Contains(string(exported), "snapshot") || strings.Contains(string(exported), "last_error") {
		t.Fatal("export included runtime card state")
	}
	var bundle struct {
		Format    string          `json:"format"`
		Dashboard json.RawMessage `json:"dashboard"`
		Nodes     []struct {
			Name           string            `json:"name"`
			Headers        map[string]string `json:"headers"`
			RefreshSeconds int               `json:"refresh_seconds"`
			Config         map[string]any    `json:"config"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(exported, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Format != "scriptboard.custom-dashboard-nodes" || len(bundle.Dashboard) != 0 || len(bundle.Nodes) != 1 {
		t.Fatalf("unexpected export bundle: %#v", bundle)
	}
	if card := bundle.Nodes[0]; card.Name != "服务额度" || card.Headers["Authorization"] != "Bearer test-secret" || card.RefreshSeconds != 300 || card.Config["unit"] != "GB" {
		t.Fatalf("exported card configuration mismatch: %#v", card)
	}
	_, exportedAll := postExport(cardMatches[0][1], cardMatches[1][1])

	response, err = client.Get(serverURL + dashboardLocation)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	postImport := func(filename string, contents []byte, selections ...string) *http.Response {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("csrf_token", formToken(t, page))
		_ = writer.WriteField("dashboard_id", dashboardID)
		if len(selections) > 0 {
			_ = writer.WriteField("selection_present", "1")
			for _, selection := range selections {
				_ = writer.WriteField("selection", selection)
			}
		}
		part, createErr := writer.CreateFormFile("dashboard_file", filename)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, _ = part.Write(contents)
		if closeErr := writer.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		request, requestErr := http.NewRequest(http.MethodPost, serverURL+"/config/dashboards/import", &body)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Content-Type", writer.FormDataContentType())
		result, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return result
	}
	response = postImport("dashboard.json", exportedAll, "1")
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("import status=%d", response.StatusCode)
	}
	importLocation := response.Header.Get("Location")
	importURL, _ := url.Parse(importLocation)
	importedID := importURL.Query().Get("dashboard")
	if importedID != dashboardID {
		t.Fatalf("imported dashboard id=%q", importedID)
	}
	response, err = client.Get(serverURL + importLocation)
	if err != nil {
		t.Fatal(err)
	}
	importedPage, _ := io.ReadAll(response.Body)
	response.Body.Close()
	importedRendered := string(importedPage)
	for _, expected := range []string{"迁移测试", "私有 · 3 张卡片", "请求次数", "https://api.example.test/requests", `value="次"`, `name="slug" value="transfer-test"`} {
		if !strings.Contains(importedRendered, expected) {
			t.Fatalf("imported dashboard missing %q", expected)
		}
	}
	tabs := regexp.MustCompile(`(?s)<nav class="custom-dashboard-tabs"[^>]*>(.*?)</nav>`).FindStringSubmatch(importedRendered)
	if len(tabs) != 2 || strings.Count(tabs[1], `<a `) != 1 {
		t.Fatalf("import created an unexpected dashboard: %s", importedRendered)
	}

	response = postImport("invalid.json", []byte(`{"not":"a dashboard"}`))
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "import_error=invalid") {
		t.Fatalf("invalid import response=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	errorPage, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(errorPage), "无法识别这个节点配置文件") || !strings.Contains(string(errorPage), `data-dashboard-drawer-name="import" open`) {
		t.Fatal("invalid import did not reopen the drawer with a useful error")
	}

	legacy := []byte(`{"format":"scriptboard.custom-dashboard","version":1,"exported_at":"2026-08-12T00:00:00Z","dashboard":{"name":"旧面板","slug":"legacy","cards":[]}}`)
	response = postImport("legacy.json", legacy)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "import_error=invalid") {
		t.Fatalf("legacy import response=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	partiallyInvalid := []byte(`{"format":"scriptboard.custom-dashboard-nodes","version":1,"exported_at":"2026-08-12T00:00:00Z","nodes":[{"name":"不应保留","type":"number","source_url":"https://api.example.test/temporary","value_path":"value","refresh_seconds":60},{"name":"无效节点","type":"unsupported","refresh_seconds":60}]}`)
	response = postImport("partially-invalid.json", partiallyInvalid)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "import_error=failed") {
		t.Fatalf("partially invalid import response=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + dashboardLocation)
	if err != nil {
		t.Fatal(err)
	}
	rollbackPage, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(rollbackPage), "不应保留") || !strings.Contains(string(rollbackPage), "私有 · 3 张卡片") {
		t.Fatalf("failed node import was not rolled back: %s", rollbackPage)
	}
}

func TestCustomDashboardCanBeCreatedPublishedAndDeleted(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/dashboards")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if rendered := string(page); !strings.Contains(rendered, `data-dashboard-drawer`) || !strings.Contains(rendered, `aria-labelledby="custom-dashboard-create-title"`) {
		t.Fatal("create dashboard drawer missing")
	}
	response, err = client.PostForm(serverURL+"/config/dashboards", url.Values{"csrf_token": {formToken(t, page)}, "name": {"API 与额度"}, "slug": {"api-credits"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	dashboardURL := serverURL + response.Header.Get("Location")
	parsed, _ := url.Parse(dashboardURL)
	if parsed.Path != "/config/dashboards" {
		t.Fatalf("dashboard configuration path=%q", parsed.Path)
	}
	dashboardID := parsed.Query().Get("dashboard")
	if dashboardID == "" {
		t.Fatal("missing dashboard id")
	}
	response, err = client.Get(dashboardURL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(page), `aria-labelledby="custom-dashboard-edit-title"`) {
		t.Fatal("edit dashboard drawer missing")
	}
	if rendered := string(page); !strings.Contains(rendered, `href="/monitor/dashboard/`+dashboardID+`"`) {
		t.Fatal("created dashboard was not appended to monitor navigation")
	}
	if rendered := string(page); strings.Contains(rendered, "更多面板操作") || !strings.Contains(rendered, `data-dashboard-delete-drawer`) || !strings.Contains(rendered, `data-dashboard-delete-open`) || !strings.Contains(rendered, `data-dashboard-slug-preview`) {
		t.Fatal("dashboard settings controls do not match the drawer contract")
	}
	if rendered := string(page); strings.Count(rendered, `data-dashboard-public-action disabled`) != 2 {
		t.Fatal("private dashboard public actions should remain visible and disabled")
	}
	if rendered := string(page); strings.Contains(rendered, "键值数据") || !strings.Contains(rendered, `value="percentage"`) || !strings.Contains(rendered, `data-dashboard-card-preview="percentage"`) {
		t.Fatal("dashboard card types or mini previews are incorrect")
	}
	privateResponse, err := http.Get(serverURL + "/public/dashboard/api-credits")
	if err != nil {
		t.Fatal(err)
	}
	privateResponse.Body.Close()
	if privateResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("new dashboard was public by default: %d", privateResponse.StatusCode)
	}
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID, url.Values{"csrf_token": {formToken(t, page)}, "name": {"API 与额度"}, "slug": {"api-credits"}, "public": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	var apiFailed atomic.Bool
	var apiRequests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiRequests.Add(1)
		if apiFailed.Load() {
			http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"remaining":63.2387,"used":36.7613}`)
	}))
	defer api.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{"csrf_token": {formToken(t, page)}, "name": {"使用率"}, "type": {"percentage"}, "source_url": {api.URL}, "value_path": {"remaining / 1"}, "refresh_seconds": {"60"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("card status=%d body=%s", response.StatusCode, body)
	}
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"账户额度"}, "type": {"quota"},
		"source_url": {api.URL}, "value_path": {"used"}, "secondary_path": {"100 - used"},
		"unit": {"GB"}, "refresh_seconds": {"60"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	publicResponse, err := http.Get(serverURL + "/public/dashboard/api-credits")
	if err != nil {
		t.Fatal(err)
	}
	publicPage, _ := io.ReadAll(publicResponse.Body)
	publicResponse.Body.Close()
	rendered := string(publicPage)
	if publicResponse.StatusCode != http.StatusOK || !strings.Contains(rendered, "使用率") || !strings.Contains(rendered, "63.24") {
		t.Fatalf("public page missing card: status=%d body=%s", publicResponse.StatusCode, rendered)
	}
	if strings.Contains(rendered, api.URL) || strings.Contains(rendered, "value_path") {
		t.Fatal("public page exposed source configuration")
	}
	if !strings.Contains(rendered, `stroke-dasharray="63.24 100"`) || strings.Contains(rendered, "数据正常") {
		t.Fatal("percentage progress or normal-state presentation is incorrect")
	}
	if !strings.Contains(rendered, `class="custom-dashboard-card__quota-chart"`) || !strings.Contains(rendered, `role="progressbar"`) || !strings.Contains(rendered, `aria-valuenow="36.76"`) || !strings.Contains(rendered, "36.76") {
		t.Fatal("quota card percentage visualization is incorrect")
	}
	if !strings.Contains(rendered, `custom-dashboard-card__percentage-unit`) || strings.Contains(rendered, `custom-dashboard-card__secondary`) || strings.Contains(rendered, `custom-dashboard-card__type`) {
		t.Fatal("percentage unit, empty secondary row, or public card type presentation is incorrect")
	}

	monitorResponse, err := client.Get(serverURL + "/monitor/dashboard/" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	monitorPage, _ := io.ReadAll(monitorResponse.Body)
	monitorResponse.Body.Close()
	monitorRendered := string(monitorPage)
	if monitorResponse.StatusCode != http.StatusOK || !strings.Contains(monitorRendered, `custom-dashboard-monitor`) || !strings.Contains(monitorRendered, "63.24") {
		t.Fatalf("authenticated monitor view missing dashboard data: status=%d body=%s", monitorResponse.StatusCode, monitorRendered)
	}
	if strings.Contains(monitorRendered, "添加卡片") || strings.Contains(monitorRendered, `aria-labelledby="custom-dashboard-edit-title"`) {
		t.Fatal("authenticated monitor view exposed configuration controls")
	}

	response, err = client.Get(dashboardURL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	configRendered := string(page)
	if !strings.Contains(configRendered, api.URL) || !strings.Contains(configRendered, `name="refresh_seconds"`) {
		t.Fatal("dashboard configuration page missing card source settings")
	}
	if !strings.Contains(configRendered, `name="unit"`) {
		t.Fatal("number and quota unit setting is missing")
	}
	if strings.Contains(configRendered, "数值路径：") || strings.Contains(configRendered, "60 秒刷新") {
		t.Fatal("dashboard configuration row exposes drawer-only details")
	}
	if !strings.Contains(configRendered, `href="/monitor/dashboard/`+dashboardID+`"`) || !strings.Contains(configRendered, "打开监控页") || !strings.Contains(configRendered, `data-dashboard-card-row`) {
		t.Fatal("dashboard configuration page is missing its monitor shortcut or clickable card row")
	}
	if !strings.Contains(configRendered, `action="/config/dashboards/`+dashboardID+`/refresh"`) || !strings.Contains(configRendered, `data-lucide="refresh-cw"`) {
		t.Fatal("dashboard configuration page is missing its force refresh control")
	}
	requestsBeforeRefresh := apiRequests.Load()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/refresh", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/config/dashboards?dashboard="+dashboardID+"&refreshed=1" {
		t.Fatalf("dashboard refresh status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if got := apiRequests.Load() - requestsBeforeRefresh; got != 2 {
		t.Fatalf("dashboard refresh requested %d sources, want 2", got)
	}
	if strings.Contains(configRendered, `custom-dashboard-sites`) || strings.Contains(configRendered, `custom-dashboard-card__value`) {
		t.Fatal("dashboard configuration page rendered the live dashboard layout")
	}
	reorderResponse, err := client.Get(dashboardURL + "&reorder=1")
	if err != nil {
		t.Fatal(err)
	}
	reorderPage, _ := io.ReadAll(reorderResponse.Body)
	reorderResponse.Body.Close()
	if reorderRendered := string(reorderPage); !strings.Contains(reorderRendered, "完成排序") || !strings.Contains(reorderRendered, `/config/dashboard-cards/`) || !strings.Contains(reorderRendered, `/move`) {
		t.Fatal("dashboard card reorder mode is missing")
	}
	if regexp.MustCompile(`action="/config/dashboard-cards/[^/"]+/refresh"`).MatchString(configRendered) {
		t.Fatal("dashboard configuration row still exposes a refresh action")
	}
	refreshMatch := regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)"`).FindStringSubmatch(configRendered)
	if len(refreshMatch) != 2 {
		t.Fatal("dashboard configuration page missing card identifier")
	}
	apiFailed.Store(true)
	response, err = client.PostForm(serverURL+"/config/dashboard-cards/"+refreshMatch[1]+"/refresh", url.Values{"csrf_token": {formToken(t, page)}, "dashboard_id": {dashboardID}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	failedResponse, err := http.Get(serverURL + "/public/dashboard/api-credits")
	if err != nil {
		t.Fatal(err)
	}
	failedPage, _ := io.ReadAll(failedResponse.Body)
	failedResponse.Body.Close()
	failedRendered := string(failedPage)
	if !strings.Contains(failedRendered, "数据异常") || !strings.Contains(failedRendered, `stroke-dasharray="0.00 100"`) || strings.Contains(failedRendered, `custom-dashboard-card__quota-value">63.24`) {
		t.Fatalf("failed quota did not reset and expose its title badge: %s", failedRendered)
	}

	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/delete", url.Values{"csrf_token": {formToken(t, page)}, "confirm": {"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
	missing, err := http.Get(serverURL + "/public/dashboard/api-credits")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("public page survived delete: %d", missing.StatusCode)
	}
}
