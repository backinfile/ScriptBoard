package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomDashboardCanBeCreatedPublishedAndDeleted(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/monitor/dashboards")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if rendered := string(page); !strings.Contains(rendered, `data-dashboard-drawer`) || !strings.Contains(rendered, `aria-labelledby="custom-dashboard-create-title"`) {
		t.Fatal("create dashboard drawer missing")
	}
	response, err = client.PostForm(serverURL+"/monitor/dashboards", url.Values{"csrf_token": {formToken(t, page)}, "name": {"API 与额度"}, "slug": {"api-credits"}, "public": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	dashboardURL := serverURL + response.Header.Get("Location")
	parsed, _ := url.Parse(dashboardURL)
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
	if rendered := string(page); strings.Contains(rendered, "更多面板操作") || !strings.Contains(rendered, `form="custom-dashboard-delete-form"`) || !strings.Contains(rendered, `data-dashboard-slug-preview`) {
		t.Fatal("dashboard settings controls do not match the drawer contract")
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"remaining":63.2}`) }))
	defer api.Close()
	response, err = client.PostForm(serverURL+"/monitor/dashboards/"+dashboardID+"/cards", url.Values{"csrf_token": {formToken(t, page)}, "name": {"剩余额度"}, "type": {"quota"}, "source_url": {api.URL}, "value_path": {"remaining"}, "refresh_seconds": {"60"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("card status=%d", response.StatusCode)
	}
	publicResponse, err := http.Get(serverURL + "/public/dashboard/api-credits")
	if err != nil {
		t.Fatal(err)
	}
	publicPage, _ := io.ReadAll(publicResponse.Body)
	publicResponse.Body.Close()
	rendered := string(publicPage)
	if publicResponse.StatusCode != http.StatusOK || !strings.Contains(rendered, "剩余额度") || !strings.Contains(rendered, "63.2") {
		t.Fatalf("public page missing card: status=%d body=%s", publicResponse.StatusCode, rendered)
	}
	if strings.Contains(rendered, api.URL) || strings.Contains(rendered, "value_path") {
		t.Fatal("public page exposed source configuration")
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
	if !strings.Contains(configRendered, api.URL) || !strings.Contains(configRendered, "60 秒刷新") {
		t.Fatal("dashboard configuration page missing card source settings")
	}
	if strings.Contains(configRendered, `custom-dashboard-sites`) || strings.Contains(configRendered, `custom-dashboard-card__value`) {
		t.Fatal("dashboard configuration page rendered the live dashboard layout")
	}

	response, err = client.PostForm(serverURL+"/monitor/dashboards/"+dashboardID+"/delete", url.Values{"csrf_token": {formToken(t, page)}, "confirm": {"yes"}})
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
