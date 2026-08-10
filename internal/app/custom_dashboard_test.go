package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

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
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiFailed.Load() {
			http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"remaining":63.2}`)
	}))
	defer api.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{"csrf_token": {formToken(t, page)}, "name": {"使用率"}, "type": {"percentage"}, "source_url": {api.URL}, "value_path": {"remaining"}, "refresh_seconds": {"60"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("card status=%d body=%s", response.StatusCode, body)
	}
	response.Body.Close()
	publicResponse, err := http.Get(serverURL + "/public/dashboard/api-credits")
	if err != nil {
		t.Fatal(err)
	}
	publicPage, _ := io.ReadAll(publicResponse.Body)
	publicResponse.Body.Close()
	rendered := string(publicPage)
	if publicResponse.StatusCode != http.StatusOK || !strings.Contains(rendered, "使用率") || !strings.Contains(rendered, "63.2") {
		t.Fatalf("public page missing card: status=%d body=%s", publicResponse.StatusCode, rendered)
	}
	if strings.Contains(rendered, api.URL) || strings.Contains(rendered, "value_path") {
		t.Fatal("public page exposed source configuration")
	}
	if !strings.Contains(rendered, `stroke-dasharray="63.20 100"`) || strings.Contains(rendered, "数据正常") {
		t.Fatal("percentage progress or normal-state presentation is incorrect")
	}

	monitorResponse, err := client.Get(serverURL + "/monitor/dashboard/" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	monitorPage, _ := io.ReadAll(monitorResponse.Body)
	monitorResponse.Body.Close()
	monitorRendered := string(monitorPage)
	if monitorResponse.StatusCode != http.StatusOK || !strings.Contains(monitorRendered, `custom-dashboard-monitor`) || !strings.Contains(monitorRendered, "63.2") {
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
	if strings.Contains(configRendered, "数值路径：") || strings.Contains(configRendered, "60 秒刷新") {
		t.Fatal("dashboard configuration row exposes drawer-only details")
	}
	if !strings.Contains(configRendered, `href="/monitor/dashboard/`+dashboardID+`"`) || !strings.Contains(configRendered, "打开监控页") || !strings.Contains(configRendered, `data-dashboard-card-row`) {
		t.Fatal("dashboard configuration page is missing its monitor shortcut or clickable card row")
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
	refreshMatch := regexp.MustCompile(`/config/dashboard-cards/([^/"]+)/refresh`).FindStringSubmatch(configRendered)
	if len(refreshMatch) != 2 {
		t.Fatal("dashboard configuration page missing refresh action")
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
	if !strings.Contains(failedRendered, "数据异常") || !strings.Contains(failedRendered, `stroke-dasharray="0.00 100"`) || strings.Contains(failedRendered, ">63.2<") {
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
