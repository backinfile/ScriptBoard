package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	app "scriptboard/internal/web"
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
	if !bytes.Contains(page, []byte("支持 HTTP 与 HTTPS；HTTP 请求头和响应均为明文传输。")) {
		t.Fatalf("dashboard form does not explain HTTP support: %s", page)
	}
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
		"source_url": {"http://api.example.test/requests"}, "value_path": {"metrics.count"},
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
		!strings.Contains(rendered, `data-dashboard-open-drawer="export"><span data-lucide="file-output"`) ||
		!strings.Contains(rendered, `data-dashboard-open-drawer="import"><span data-lucide="file-input"`) ||
		!strings.Contains(rendered, `data-lucide="file-output" aria-hidden="true"></span>导出所选节点`) ||
		!strings.Contains(rendered, `data-lucide="file-input" aria-hidden="true"></span>导入所选节点`) ||
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
	if card := bundle.Nodes[0]; card.Name != "服务额度" || card.Headers["Authorization"] != "[REDACTED]" || card.RefreshSeconds != 300 || card.Config["unit"] != "GB" {
		t.Fatalf("exported card configuration mismatch: %#v", card)
	}
	if strings.Contains(string(exported), "test-secret") {
		t.Fatalf("export leaked authorization secret: %s", exported)
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
	for _, expected := range []string{"迁移测试", "私有 · 3 张卡片", "请求次数", "http://api.example.test/requests", `value="次"`, `name="slug" value="transfer-test"`} {
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
	invalidLocation := response.Header.Get("Location")
	response = postImport("dashboard.json.exe", exportedAll)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "import_error=invalid") {
		t.Fatalf("active-extension import response=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + invalidLocation)
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

func TestRegistryCardCanBeConfiguredWithHTTPAndMultipleImages(t *testing.T) {
	webConfigDigest := "sha256:" + strings.Repeat("d", 64)
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "robot$board" || password != "registry-secret" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/v2/_catalog":
			_, _ = response.Write([]byte(`{"repositories":["team/web","team/api","other/db"]}`))
		case "/v2/team/api/tags/list":
			_, _ = response.Write([]byte(`{"tags":["v2.4.0","v2.5.0"]}`))
		case "/v2/team/web/tags/list":
			_, _ = response.Write([]byte(`{"tags":["1.8.1"]}`))
		case "/api/v2.0/projects/team/repositories/api/artifacts/v2.5.0":
			_, _ = response.Write([]byte(`{"push_time":"2026-08-18T10:30:00Z"}`))
		case "/api/v2.0/projects/team/repositories/web/artifacts/1.8.1":
			http.NotFound(response, request)
		case "/v2/team/web/manifests/1.8.1":
			_ = json.NewEncoder(response).Encode(map[string]any{"schemaVersion": 2, "config": map[string]any{"digest": webConfigDigest}})
		case "/v2/team/web/blobs/" + webConfigDigest:
			_, _ = response.Write([]byte(`{"created":"2026-08-17T11:45:00Z"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer registry.Close()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/dashboards")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"镜像版本"}, "slug": {"registry-images"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location, _ := url.Parse(response.Header.Get("Location"))
	dashboardID := location.Query().Get("dashboard")
	response, err = client.Get(serverURL + response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"生产镜像"}, "type": {"registry"},
		"registry_endpoint": {registry.URL}, "registry_images": {"team/*"},
		"registry_auth_mode": {"basic"}, "registry_username": {"robot$board"},
		"registry_password": {"registry-secret"}, "refresh_seconds": {"60"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create registry card status=%d", response.StatusCode)
	}
	response, err = client.Get(serverURL + "/monitor/dashboard/" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	renderedBytes, _ := io.ReadAll(response.Body)
	response.Body.Close()
	rendered := string(renderedBytes)
	for _, expected := range []string{"生产镜像", "team/api", "v2.5.0", "team/web", "1.8.1", "上传时间 2026-08-18", "构建时间 2026-08-17"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("registry card missing %q", expected)
		}
	}
	if strings.Contains(rendered, "registry-secret") || !strings.Contains(rendered, "custom-dashboard-registry-list") {
		t.Fatal("registry secret leaked or list presentation missing")
	}
	response, err = client.Get(serverURL + "/config/dashboards?dashboard=" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID, url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"镜像版本"}, "slug": {"registry-images"}, "public": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	publicResponse, err := http.Get(serverURL + "/public/dashboard/registry-images")
	if err != nil {
		t.Fatal(err)
	}
	publicPage, _ := io.ReadAll(publicResponse.Body)
	publicResponse.Body.Close()
	publicRendered := string(publicPage)
	if publicResponse.StatusCode != http.StatusOK || !strings.Contains(publicRendered, "v2.5.0") {
		t.Fatalf("public registry card missing result: status=%d", publicResponse.StatusCode)
	}
	for _, private := range []string{registry.URL, "robot$board", "registry-secret"} {
		if strings.Contains(publicRendered, private) {
			t.Fatalf("public registry card exposed %q", private)
		}
	}
}

func TestHTTPRegistryCardCanRegisterDockerInsecureRegistry(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "robot" || password != "secret" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = response.Write([]byte(`{"tags":["1.0.0"]}`))
	}))
	defer registry.Close()

	root := t.TempDir()
	dockerConfigPath := filepath.Join(root, "docker", "daemon.json")
	if err := os.MkdirAll(filepath.Dir(dockerConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerConfigPath, []byte(`{"log-level":"warn"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: root, CustomDashboardClient: registry.Client(), RegistryDockerDaemonConfigPath: dockerConfigPath,
	})
	response, err := client.Get(serverURL + "/config/dashboards")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"Registry"}, "slug": {"registry"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dashboardLocation := response.Header.Get("Location")
	_ = response.Body.Close()
	dashboardURL, _ := url.Parse(dashboardLocation)
	dashboardID := dashboardURL.Query().Get("dashboard")
	response, err = client.Get(serverURL + dashboardLocation)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"Private image"}, "type": {"registry"},
		"registry_endpoint": {registry.URL}, "registry_images": {"team/api"}, "registry_auth_mode": {"basic"},
		"registry_username": {"robot"}, "registry_password": {"secret"}, "refresh_seconds": {"60"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/config/dashboards?dashboard=" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	cardMatch := regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)/registry/insecure"`).FindSubmatch(page)
	if len(cardMatch) != 2 || !bytes.Contains(page, []byte("Register HTTP")) || bytes.Contains(page, []byte("secret")) {
		t.Fatalf("HTTP Registry registration action or credential boundary missing: %s", page)
	}
	maintainer := createRoleUserClient(t, client, serverURL, "registry-maintainer", "maintainer")
	maintainerResponse, err := maintainer.Get(serverURL + "/config/dashboards?dashboard=" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	maintainerPage, _ := io.ReadAll(maintainerResponse.Body)
	_ = maintainerResponse.Body.Close()
	if bytes.Contains(maintainerPage, []byte(`/registry/insecure`)) || !bytes.Contains(maintainerPage, []byte("HTTP registration requires a system administrator")) {
		t.Fatalf("maintainer was offered Docker Engine configuration: %s", maintainerPage)
	}
	response, err = client.PostForm(serverURL+"/config/dashboard-cards/"+string(cardMatch[1])+"/registry/insecure", url.Values{
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "registry_notice=registered") {
		t.Fatalf("register status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	_ = response.Body.Close()
	body, err := os.ReadFile(dockerConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		LogLevel           string   `json:"log-level"`
		InsecureRegistries []string `json:"insecure-registries"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	registryURL, _ := url.Parse(registry.URL)
	if document.LogLevel != "warn" || len(document.InsecureRegistries) != 1 || document.InsecureRegistries[0] != registryURL.Host {
		t.Fatalf("Docker daemon configuration=%s", body)
	}
}

func TestNumberCardRendersStringValue(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/dashboards")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"发布状态"}, "slug": {"release-status"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	dashboardLocation := response.Header.Get("Location")
	dashboardURL, err := url.Parse(dashboardLocation)
	if err != nil {
		t.Fatal(err)
	}
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
	if rendered := string(page); !strings.Contains(rendered, "显示数值或文本") || !strings.Contains(rendered, "字符串使用 JSON 路径") {
		t.Fatalf("number card string guidance missing: %s", rendered)
	}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"build":{"label":"v1.2.3-rc.1 / 稳定"}}`)
	}))
	defer api.Close()
	response, err = client.PostForm(serverURL+"/config/dashboards/"+dashboardID+"/cards", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"当前版本"}, "type": {"number"},
		"source_url": {api.URL}, "value_path": {"build.label"}, "refresh_seconds": {"60"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	response, err = client.Get(serverURL + "/monitor/dashboard/" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	monitorPage, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if rendered := string(monitorPage); response.StatusCode != http.StatusOK || !strings.Contains(rendered, "v1.2.3-rc.1 / 稳定") || !strings.Contains(rendered, "custom-dashboard-card__value--text") || strings.Contains(rendered, "custom-dashboard-card__secondary") {
		t.Fatalf("number card string value missing: status=%d body=%s", response.StatusCode, rendered)
	}
}

func TestCustomDashboardCanBeCreatedPublishedAndDeleted(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: root + string(filepath.Separator) + "state", FileTopology: testHostTopology{root: hostRoot},
		CustomDashboardClient: &http.Client{Transport: transport},
	})
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
	if rendered := string(page); strings.Contains(rendered, "键值数据") || !strings.Contains(rendered, `value="percentage"`) || !strings.Contains(rendered, `data-dashboard-card-preview="percentage"`) || !strings.Contains(rendered, `value="registry"`) || !strings.Contains(rendered, `data-dashboard-card-preview="registry"`) {
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
	if strings.Contains(rendered, `custom-dashboard-title__meta`) || strings.Contains(rendered, `/public/dashboard/`+dashboardID) {
		t.Fatal("live public dashboard exposed its path or update metadata")
	}
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
	if strings.Contains(monitorRendered, `custom-dashboard-title__meta`) || strings.Contains(monitorRendered, `/public/dashboard/api-credits`) {
		t.Fatal("live monitor dashboard exposed its path or update metadata")
	}
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
	if strings.Contains(configRendered, `custom-dashboard-title__meta`) {
		t.Fatal("dashboard heading still renders path or update metadata")
	}
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
	testResponse, err := client.PostForm(serverURL+"/config/dashboard-card-tests", url.Values{
		"csrf_token":      {formToken(t, page)},
		"name":            {"unsaved request"},
		"type":            {"number"},
		"source_url":      {api.URL},
		"value_path":      {"used"},
		"headers":         {"Authorization: Bearer test-only-secret"},
		"refresh_seconds": {"60"},
	})
	if err != nil {
		t.Fatal(err)
	}
	testPayload, _ := io.ReadAll(testResponse.Body)
	testResponse.Body.Close()
	if testResponse.StatusCode != http.StatusOK || !strings.Contains(string(testPayload), `"ok":true`) || !strings.Contains(string(testPayload), `"Authorization":"[REDACTED]"`) || strings.Contains(string(testPayload), "test-only-secret") {
		t.Fatalf("unsafe or unsuccessful test request: status=%d body=%s", testResponse.StatusCode, testPayload)
	}
	afterTestResponse, err := client.Get(dashboardURL)
	if err != nil {
		t.Fatal(err)
	}
	afterTestPage, _ := io.ReadAll(afterTestResponse.Body)
	afterTestResponse.Body.Close()
	if got := len(regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)"`).FindAllStringSubmatch(string(afterTestPage), -1)); got != 2 {
		t.Fatalf("test request mutated dashboard cards: got %d", got)
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
	moveMatches := regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)/move"`).FindAllStringSubmatch(string(reorderPage), -1)
	if len(moveMatches) != 2 {
		t.Fatalf("dashboard reorder controls=%d, want 2", len(moveMatches))
	}
	moveResponse, err := client.PostForm(serverURL+"/config/dashboard-cards/"+moveMatches[0][1]+"/move", url.Values{"csrf_token": {formToken(t, reorderPage)}, "direction": {"down"}})
	if err != nil {
		t.Fatal(err)
	}
	moveResponse.Body.Close()
	if moveResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("dashboard reorder status=%d", moveResponse.StatusCode)
	}
	moveResult, err := client.Get(serverURL + moveResponse.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	movePage, _ := io.ReadAll(moveResult.Body)
	moveResult.Body.Close()
	accountIndex, usageIndex := strings.Index(string(movePage), "账户额度"), strings.Index(string(movePage), "使用率")
	if moveResult.StatusCode != http.StatusOK || accountIndex < 0 || usageIndex < 0 || accountIndex > usageIndex {
		t.Fatalf("dashboard reorder did not move and persist the first card: status=%d", moveResult.StatusCode)
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
	if !strings.Contains(failedRendered, `custom-dashboard-card__status-badge`) || !strings.Contains(failedRendered, `>Error</span>`) || !strings.Contains(failedRendered, `stroke-dasharray="63.24 100"`) || !strings.Contains(failedRendered, `custom-dashboard-card__quota-value">63.24`) || !strings.Contains(failedRendered, `custom-dashboard-card__retained`) {
		t.Fatalf("failed card did not retain its last successful value with a generic status: %s", failedRendered)
	}
	if !strings.Contains(failedRendered, `custom-dashboard-card__refresh-time`) || !strings.Contains(failedRendered, `Refreshed `) {
		t.Fatalf("card title does not show the last successful refresh time: %s", failedRendered)
	}
	if strings.Contains(failedRendered, api.URL) || strings.Contains(failedRendered, "HTTP 503") || strings.Contains(failedRendered, `custom-dashboard-diagnostic`) || strings.Contains(failedRendered, `data-dashboard-drawer`) {
		t.Fatal("public dashboard exposed private request diagnostics")
	}
	failedMonitorResponse, err := client.Get(serverURL + "/monitor/dashboard/" + dashboardID)
	if err != nil {
		t.Fatal(err)
	}
	failedMonitorPage, _ := io.ReadAll(failedMonitorResponse.Body)
	failedMonitorResponse.Body.Close()
	failedMonitorRendered := string(failedMonitorPage)
	if !strings.Contains(failedMonitorRendered, `>Error</span>`) || strings.Contains(failedMonitorRendered, `custom-dashboard-card__status-action`) || strings.Contains(failedMonitorRendered, `custom-dashboard-diagnostic`) || strings.Contains(failedMonitorRendered, "HTTP 503") || strings.Contains(failedMonitorRendered, api.URL) {
		t.Fatalf("authenticated card exposed request diagnostics instead of a static error marker: %s", failedMonitorRendered)
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
