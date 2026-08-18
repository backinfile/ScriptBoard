package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	app "scriptboard/internal/web"
	"scriptboard/internal/websitemonitor"
)

func TestWebsiteAvailabilityRendersFreshCurrentBucketAsProvisional(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 59, 59, 0, time.UTC)
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Now: func() time.Time { return now },
			Probe: websiteProbeFunc(func(_ context.Context, config websitemonitor.Config) websitemonitor.Evidence {
				if config.Name == "复核边界网站" {
					return websitemonitor.Evidence{
						ErrorCategory: "connect", Summary: "连接失败",
					}
				}
				return websitemonitor.Evidence{
					Success: true, StatusCode: http.StatusNoContent,
					Latency: 18 * time.Millisecond, Summary: "网站返回 HTTP 204",
				}
			}),
		},
	})
	setWebsiteTestLocale(t, client, serverURL, "zh-CN")

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	csrfToken := formToken(t, newPage)
	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {csrfToken},
		"name":              {"桶边界网站"},
		"scope":             {"external"},
		"kind":              {"http"},
		"url":               {"https://boundary.example/"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"10"},
		"http_method":       {"GET"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	detailPath := response.Header.Get("Location")
	waitForWebsiteCheckCount(t, client, serverURL, detailPath, 1)

	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {csrfToken},
		"name":              {"复核边界网站"},
		"scope":             {"external"},
		"kind":              {"http"},
		"url":               {"https://verifying-boundary.example/"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"10"},
		"http_method":       {"GET"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create verifying monitor status=%d", response.StatusCode)
	}
	verifyingDetailPath := response.Header.Get("Location")
	waitForWebsiteCheckCount(t, client, serverURL, verifyingDetailPath, 1)

	now = time.Date(2026, time.July, 29, 13, 0, 0, 0, time.UTC)
	stateClass := regexp.MustCompile(`class="website-availability__(?:gap|up|down|verifying)(?: website-availability__provisional)?"`)
	provisionalLabel := regexp.MustCompile(`aria-label="本时间段尚未完成检查；最近状态：正常（[^"]+）"`)
	var listPage []byte

	for _, path := range []string{"/monitor/websites", detailPath} {
		response, err = client.Get(serverURL + path)
		if err != nil {
			t.Fatal(err)
		}
		page, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		wantBuckets := 96
		if path == detailPath {
			wantBuckets = 72
		}
		if count := len(stateClass.FindAll(page, -1)); count != wantBuckets {
			t.Fatalf("%s availability buckets=%d, want %d: %s", path, count, wantBuckets, page)
		}
		wantProvisional := 1
		if path == "/monitor/websites" {
			wantProvisional = 2
			listPage = page
		}
		if count := bytes.Count(page, []byte("website-availability__provisional")); count != wantProvisional {
			t.Fatalf("%s provisional buckets=%d, want %d: %s", path, count, wantProvisional, page)
		}
		if !provisionalLabel.Match(page) {
			t.Fatalf("%s provisional accessible label missing: %s", path, page)
		}
	}
	if !bytes.Contains(listPage, []byte(
		`class="website-availability__verifying website-availability__provisional"`,
	)) || !bytes.Contains(listPage, []byte(
		`aria-label="本时间段尚未完成检查；最近状态：复核中（`,
	)) {
		t.Fatalf("verifying provisional tone or label missing: %s", listPage)
	}

	setWebsiteTestLocale(t, client, serverURL, "en-US")
	response, err = client.Get(serverURL + detailPath)
	if err != nil {
		t.Fatal(err)
	}
	englishPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(englishPage, []byte(
		`aria-label="No check has completed in this period; latest state: Up (`,
	)) {
		t.Fatalf("English provisional accessible label missing: %s", englishPage)
	}
}

func TestWebsiteMonitorConfigurationsExportSelectedAndImportSelected(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(_ context.Context, _ websitemonitor.Config) websitemonitor.Evidence {
				return websitemonitor.Evidence{Success: true, StatusCode: http.StatusOK, Summary: "ok"}
			}),
		},
	})
	setWebsiteTestLocale(t, client, serverURL, "zh-CN")

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(newPage, []byte(`name="request_headers"`)) || !bytes.Contains(newPage, []byte(`maxlength="16384"`)) {
		t.Fatalf("new monitor form missing custom request headers field: %s", newPage)
	}
	csrfToken := formToken(t, newPage)
	invalidResponse, err := client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token": {csrfToken}, "name": {"Invalid headers"}, "scope": {"external"}, "kind": {"http"},
		"url": {"https://invalid-headers.example/"}, "frequency_seconds": {"60"}, "timeout_seconds": {"10"},
		"http_method": {"GET"}, "request_headers": {"Content-Length: 10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidPage, err := io.ReadAll(invalidResponse.Body)
	_ = invalidResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if invalidResponse.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(invalidPage, []byte(`name="request_headers"`)) {
		t.Fatalf("invalid headers status=%d body=%s", invalidResponse.StatusCode, invalidPage)
	}
	create := func(name, target string) {
		t.Helper()
		response, postErr := client.PostForm(serverURL+"/monitor/websites", url.Values{
			"csrf_token": {csrfToken}, "name": {name}, "scope": {"external"}, "kind": {"http"},
			"url": {target}, "frequency_seconds": {"60"}, "timeout_seconds": {"10"},
			"http_method": {"POST"}, "http_content_type": {"application/json"},
			"http_body": {`{"probe":"ready"}`}, "http_success_mode": {"exact"},
			"request_headers":   {"Authorization: Bearer secret\nX-Tenant: north"},
			"expected_statuses": {"200;204"}, "response_keyword": {"ready"},
			"follow_redirects": {"1"}, "verify_tls": {"1"},
		})
		if postErr != nil {
			t.Fatal(postErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("create %q status=%d", name, response.StatusCode)
		}
	}
	create("导出目标", "http://export-one.example/health")
	create("不导出目标", "https://export-two.example/health")

	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	listPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range [][]byte{[]byte(`href="/monitor/websites/export"`), []byte(`href="/monitor/websites/import"`)} {
		if !bytes.Contains(listPage, expected) {
			t.Fatalf("website list missing transfer action %q: %s", expected, listPage)
		}
	}

	response, err = client.Get(serverURL + "/monitor/websites/export")
	if err != nil {
		t.Fatal(err)
	}
	exportPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	selectionPattern := regexp.MustCompile(`name="selection" value="([^"]+)"`)
	selections := selectionPattern.FindAllSubmatch(exportPage, -1)
	if len(selections) != 2 || !bytes.Contains(exportPage, []byte("全选")) {
		t.Fatalf("export selection page does not list both monitors with select-all: %s", exportPage)
	}
	if !bytes.Contains(exportPage, []byte(`data-website-selection-form data-native`)) {
		t.Fatalf("export form must use native submission so attachment responses trigger a download: %s", exportPage)
	}

	response, err = client.PostForm(serverURL+"/monitor/websites/export", url.Values{
		"csrf_token": {formToken(t, exportPage)}, "selection": {string(selections[0][1])},
	})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatalf("export response status=%d disposition=%q body=%s", response.StatusCode, response.Header.Get("Content-Disposition"), exported)
	}
	var bundle map[string]any
	if err := json.Unmarshal(exported, &bundle); err != nil {
		t.Fatal(err)
	}
	monitors, ok := bundle["monitors"].([]any)
	if !ok || len(monitors) != 1 {
		t.Fatalf("exported monitors=%#v, want one selected monitor", bundle["monitors"])
	}
	record := monitors[0].(map[string]any)
	headers, headersOK := record["request_headers"].([]any)
	if !headersOK || len(headers) != 2 {
		t.Fatalf("exported request headers = %#v", record["request_headers"])
	}
	if record["name"] != "导出目标" || record["url"] != "http://export-one.example/health" || record["http_body"] != `{"probe":"ready"}` {
		t.Fatalf("exported configuration lost selected settings: %#v", record)
	}
	if strings.Contains(string(exported), "Bearer secret") || !strings.Contains(string(exported), "[REDACTED]") {
		t.Fatalf("website monitor export did not redact authorization: %s", exported)
	}
	record["name"] = "导入副本"
	record["url"] = "http://imported-copy.example/health"
	exported, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}

	response, err = client.Get(serverURL + "/monitor/websites/import")
	if err != nil {
		t.Fatal(err)
	}
	importPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf_token", formToken(t, importPage)); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("config_file", "monitors.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(exported); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/monitor/websites/import/preview", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	previewPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	previewTokenPattern := regexp.MustCompile(`name="preview_token" value="([a-f0-9]{64})"`)
	previewToken := previewTokenPattern.FindSubmatch(previewPage)
	if response.StatusCode != http.StatusOK || len(previewToken) != 2 || !bytes.Contains(previewPage, []byte("导入副本")) || !bytes.Contains(previewPage, []byte("全选可导入项")) {
		t.Fatalf("import preview status=%d token=%q body=%s", response.StatusCode, previewToken, previewPage)
	}

	response, err = client.PostForm(serverURL+"/monitor/websites/import", url.Values{
		"csrf_token": {formToken(t, previewPage)}, "preview_token": {string(previewToken[1])}, "selection": {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor/websites/import?imported=1" {
		t.Fatalf("import status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	listPage, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(listPage, []byte("导入副本")) || !bytes.Contains(listPage, []byte("http://imported-copy.example/health")) {
		t.Fatalf("imported monitor missing from list: %s", listPage)
	}
}

func TestWebsiteMonitorExpandsVariablesInRequestHeadersWithoutPersistingSecrets(t *testing.T) {
	received := make(chan websitemonitor.Config, 1)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(_ context.Context, config websitemonitor.Config) websitemonitor.Evidence {
				received <- config
				return websitemonitor.Evidence{Success: true, StatusCode: http.StatusNoContent, Summary: "ok"}
			}),
		},
	})
	setWebsiteTestLocale(t, client, serverURL, "en-US")

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(newPage, []byte("Values may contain {{VARIABLE_NAME}}")) {
		t.Fatalf("request header variable guidance is missing: %s", newPage)
	}
	csrfToken := formToken(t, newPage)
	response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
		"csrf_token": {csrfToken}, "name": {"API_TOKEN"}, "value": {"secret-value"}, "is_password": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create variable status=%d", response.StatusCode)
	}

	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token": {csrfToken}, "name": {"Authenticated API"}, "scope": {"external"}, "kind": {"http"},
		"url": {"https://api.example.com/health"}, "frequency_seconds": {"60"}, "timeout_seconds": {"10"},
		"http_method": {"GET"}, "request_headers": {"Authorization: Bearer {{API_TOKEN}}"},
		"follow_redirects": {"1"}, "verify_tls": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create monitor status=%d", response.StatusCode)
	}
	detailPath := response.Header.Get("Location")

	select {
	case checked := <-received:
		if len(checked.RequestHeaders) != 1 || checked.RequestHeaders[0].Value != "Bearer secret-value" {
			t.Fatalf("checked request headers = %#v", checked.RequestHeaders)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for website check")
	}

	response, err = client.Get(serverURL + detailPath + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	editPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(editPage, []byte("Bearer {{API_TOKEN}}")) || bytes.Contains(editPage, []byte("secret-value")) {
		t.Fatalf("edit form did not preserve only the variable template: %s", editPage)
	}

	response, err = client.PostForm(serverURL+"/resources/variables/API_TOKEN/update", url.Values{
		"csrf_token": {csrfToken}, "name": {"RENAMED_TOKEN"}, "value": {"secret-value"}, "is_password": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename variable status=%d", response.StatusCode)
	}
	response, err = client.Get(serverURL + detailPath + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	editPage, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(editPage, []byte("Bearer {{RENAMED_TOKEN}}")) || bytes.Contains(editPage, []byte("{{API_TOKEN}}")) {
		t.Fatalf("variable rename did not update the request header template: %s", editPage)
	}

	response, err = client.PostForm(serverURL+"/resources/variables/RENAMED_TOKEN/delete", url.Values{
		"csrf_token": {csrfToken}, "confirm": {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("delete referenced variable status=%d", response.StatusCode)
	}
}

func TestAdminCreatesWebsiteMonitorAndReadsItsResult(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(_ context.Context, config websitemonitor.Config) websitemonitor.Evidence {
				return websitemonitor.Evidence{
					Success:    true,
					StatusCode: http.StatusNoContent,
					Latency:    18 * time.Millisecond,
					Summary:    "网站返回 HTTP 204",
				}
			}),
		},
	})
	setWebsiteTestLocale(t, client, serverURL, "zh-CN")

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("new page status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	for _, expected := range []string{
		"新增网站", `action="/monitor/websites"`, `name="http_method"`,
		`value="ping-pong"`, "Ping/Pong 控制帧", "最大 125 字节",
	} {
		if !bytes.Contains(newPage, []byte(expected)) {
			t.Fatalf("new monitor page does not contain %q: %s", expected, newPage)
		}
	}

	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {formToken(t, newPage)},
		"name":              {"管理入口"},
		"scope":             {"local"},
		"kind":              {"http"},
		"url":               {"http://127.0.0.1:8080/health"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"10"},
		"http_method":       {"GET"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	location := response.Header.Get("Location")
	if !strings.HasPrefix(location, "/monitor/websites/") {
		t.Fatalf("create redirect=%q", location)
	}

	var detail []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err = client.Get(serverURL + location)
		if err != nil {
			t.Fatal(err)
		}
		detail, err = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(detail, []byte("网站返回 HTTP 204")) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, expected := range []string{"管理入口", "正常", "HTTP 204", "18 ms", "证书与连接安全"} {
		if !bytes.Contains(detail, []byte(expected)) {
			t.Fatalf("detail page does not contain %q: %s", expected, detail)
		}
	}

	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	listPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`data-website-ledger`, "网站监控", "管理入口", "调整顺序",
		`href="/monitor/websites" aria-current="page"`,
	} {
		if !bytes.Contains(listPage, []byte(expected)) {
			t.Fatalf("list page does not contain %q: %s", expected, listPage)
		}
	}

	response, err = client.Get(serverURL + location + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	editPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{"编辑网站", `action="` + location + `"`, "http://127.0.0.1:8080/health", "使用 http:// 或 https:// 地址。"} {
		if !bytes.Contains(editPage, []byte(expected)) {
			t.Fatalf("edit page does not contain %q: %s", expected, editPage)
		}
	}
	if bytes.Contains(editPage, []byte("#ZgotmplZ")) {
		t.Fatalf("edit page contains an escaped URL-context placeholder: %s", editPage)
	}
	response, err = client.PostForm(serverURL+location, url.Values{
		"csrf_token":        {formToken(t, editPage)},
		"name":              {"管理入口新版"},
		"scope":             {"external"},
		"kind":              {"http"},
		"url":               {"https://status.example/health"},
		"frequency_seconds": {"300"},
		"timeout_seconds":   {"5"},
		"http_method":       {"GET"},
		"http_success_mode": {"range"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != location {
		t.Fatalf("update status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + location)
	if err != nil {
		t.Fatal(err)
	}
	updatedPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(updatedPage, []byte("管理入口新版")) || !bytes.Contains(updatedPage, []byte("https://status.example/health")) {
		t.Fatalf("updated monitor is missing: %s", updatedPage)
	}
}

func TestNginxScanPreviewsBeforeASeparateImport(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "nginx.conf")
	if err := os.WriteFile(configPath, []byte(`
		http { server { listen 8080; server_name imported.local; } }
	`), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(context.Context, websitemonitor.Config) websitemonitor.Evidence {
				return websitemonitor.Evidence{Success: true}
			}),
		},
	})
	setWebsiteTestLocale(t, client, serverURL, "zh-CN")
	response, err := client.Get(serverURL + "/monitor/websites/nginx")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/monitor/websites/nginx/scan", url.Values{
		"csrf_token":  {formToken(t, taskPage)},
		"config_path": {configPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{"imported.local", "勾选要监控的网站", "加入监控"} {
		if !bytes.Contains(preview, []byte(expected)) {
			t.Fatalf("scan preview does not contain %q: %s", expected, preview)
		}
	}
	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	beforeImport, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if bytes.Contains(beforeImport, []byte("imported.local")) {
		t.Fatalf("scan imported a candidate: %s", beforeImport)
	}

	emptyImportForm := url.Values{
		"csrf_token":  {formToken(t, preview)},
		"config_path": {configPath},
	}
	emptyImportRequest, err := http.NewRequest(
		http.MethodPost,
		serverURL+"/monitor/websites/nginx/import",
		strings.NewReader(emptyImportForm.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	emptyImportRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	emptyImportRequest.Header.Set("Accept", "application/json")
	response, err = client.Do(emptyImportRequest)
	if err != nil {
		t.Fatal(err)
	}
	var errorPayload struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&errorPayload); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity ||
		errorPayload.Error.Code != string(websitemonitor.ErrorSelectionRequired) ||
		errorPayload.Error.Field != "digest" {
		t.Fatalf("empty Nginx import status=%d payload=%#v", response.StatusCode, errorPayload)
	}

	response, err = client.PostForm(serverURL+"/monitor/websites/nginx/import", url.Values{
		"csrf_token":  {formToken(t, preview)},
		"config_path": {configPath},
		"digest":      {hiddenValue(t, preview, "digest")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/monitor/websites/nginx?imported=1" {
		t.Fatalf("import status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	successPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK ||
		!bytes.Contains(successPage, []byte(`class="nginx-import-success"`)) {
		t.Fatalf("Nginx import success page status=%d body=%s", response.StatusCode, successPage)
	}
	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	afterImport, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(afterImport, []byte("imported.local")) {
		t.Fatalf("imported candidate is missing: %s", afterImport)
	}
}

func TestNginxJSONImportAcceptsMultipartFormAndReturnsSuccessContract(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "nginx.conf")
	if err := os.WriteFile(configPath, []byte(`
		http { server { listen 8080; server_name json-import.local; } }
	`), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(context.Context, websitemonitor.Config) websitemonitor.Evidence {
				return websitemonitor.Evidence{Success: true}
			}),
		},
	})
	response, err := client.Get(serverURL + "/monitor/websites/nginx")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/monitor/websites/nginx/scan", url.Values{
		"csrf_token":  {formToken(t, taskPage)},
		"config_path": {configPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"csrf_token":  formToken(t, preview),
		"config_path": configPath,
		"digest":      hiddenValue(t, preview, "digest"),
	} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost, serverURL+"/monitor/websites/nginx/import", &body,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ImportedCount int    `json:"importedCount"`
		RedirectURL   string `json:"redirectURL"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated || payload.ImportedCount != 1 ||
		payload.RedirectURL != "/monitor/websites" {
		t.Fatalf("JSON import status=%d payload=%#v", response.StatusCode, payload)
	}
}

func TestWebsiteMonitoringLocalizesEnglishAndShowsCheckedZeroLatency(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(context.Context, websitemonitor.Config) websitemonitor.Evidence {
				return websitemonitor.Evidence{
					Success:    true,
					StatusCode: http.StatusOK,
					Latency:    0,
					Summary:    "网站返回 HTTP 200",
				}
			}),
		},
	})
	setWebsiteTestLocale(t, client, serverURL, "en-US")

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`<html lang="en-US">`, "Add Website", "Application message check",
		"Status codes or ranges", "Any HTTP response", "For example: 200;401-499;503",
		"Save and check",
	} {
		if !bytes.Contains(newPage, []byte(expected)) {
			t.Fatalf("English new monitor page does not contain %q: %s", expected, newPage)
		}
	}

	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {formToken(t, newPage)},
		"name":              {"Invalid monitor"},
		"scope":             {"local"},
		"kind":              {"http"},
		"url":               {"http://127.0.0.1/"},
		"frequency_seconds": {"7"},
		"timeout_seconds":   {"2"},
		"http_method":       {"GET"},
		"http_success_mode": {"exact"},
		"expected_statuses": {"99"},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create status=%d", response.StatusCode)
	}
	for _, expected := range []string{
		"Check frequency must be 30 seconds, 1 minute, 5 minutes, or 15 minutes",
		"Maximum wait must be 3, 5, 10, or 30 seconds",
		"Status codes and range endpoints must be from 100 through 599",
	} {
		if !bytes.Contains(invalidPage, []byte(expected)) {
			t.Fatalf("English validation page does not contain %q: %s", expected, invalidPage)
		}
	}

	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {formToken(t, invalidPage)},
		"name":              {"Fast local endpoint"},
		"scope":             {"local"},
		"kind":              {"http"},
		"url":               {"http://127.0.0.1:18803/health"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"10"},
		"http_method":       {"GET"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	location := response.Header.Get("Location")
	if !strings.HasPrefix(location, "/monitor/websites/") {
		t.Fatalf("create redirect=%q", location)
	}

	var detail []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err = client.Get(serverURL + location)
		if err != nil {
			t.Fatal(err)
		}
		detail, err = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(detail, []byte("HTTP 200")) && bytes.Contains(detail, []byte("0 ms")) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, expected := range []string{
		"Back to Website Monitoring", "Check now", "Connection security", "0 ms",
		"1 minute", "10 seconds", "Connection and success rules",
		`class="website-settings-summary"`, `class="website-settings-list"`,
		`website-security-summary website-security-summary--`,
		`class="website-security-summary__signal"`,
		`class="website-security-summary__issuer"`,
		`class="website-security-summary__valid-until"`,
		`class="website-security-summary__remaining"`,
	} {
		if !bytes.Contains(detail, []byte(expected)) {
			t.Fatalf("English detail page does not contain %q: %s", expected, detail)
		}
	}

	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	listPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Website Monitoring", "Refresh", "Scan", "Add Website",
		`aria-label="Website status list"`, "1 website", "0 ms",
	} {
		if !bytes.Contains(listPage, []byte(expected)) {
			t.Fatalf("English list page does not contain %q: %s", expected, listPage)
		}
	}
	for _, oldLabel := range []string{"Scan Nginx", "Connect ScriptBoard"} {
		if bytes.Contains(listPage, []byte(oldLabel)) {
			t.Fatalf("English list page still contains old action label %q: %s", oldLabel, listPage)
		}
	}

	response, err = client.Get(serverURL + location + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	editPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Edit Website", "Save and check again"} {
		if !bytes.Contains(editPage, []byte(expected)) {
			t.Fatalf("English edit page does not contain %q: %s", expected, editPage)
		}
	}

	response, err = client.Get(serverURL + "/monitor/websites/nginx")
	if err != nil {
		t.Fatal(err)
	}
	nginxPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Back to Website Monitoring", "Scan Nginx", "Find websites"} {
		if !bytes.Contains(nginxPage, []byte(expected)) {
			t.Fatalf("English Nginx page does not contain %q: %s", expected, nginxPage)
		}
	}
}

func TestWebsiteMonitoringDataReturnsCompletePollingAndDetailSnapshots(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(context.Context, websitemonitor.Config) websitemonitor.Evidence {
				return websitemonitor.Evidence{
					ErrorCategory: "connect",
					Summary:       "网站拒绝连接",
				}
			}),
			Tick:       5 * time.Millisecond,
			RetryDelay: 10 * time.Millisecond,
		},
	})
	setWebsiteTestLocale(t, client, serverURL, "zh-CN")

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {formToken(t, newPage)},
		"name":              {"轮询失败样本"},
		"scope":             {"local"},
		"kind":              {"http"},
		"url":               {"http://127.0.0.1:1/"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"3"},
		"http_method":       {"GET"},
		"http_success_mode": {"range"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	location := response.Header.Get("Location")

	type item struct {
		ID                string
		FailureCount      int
		NextCheckAt       time.Time
		IncidentStartedAt time.Time
	}
	var listSnapshot struct {
		Monitors []item `json:"monitors"`
		Alerts   []item `json:"alerts"`
		Counts   struct {
			Down int `json:"down"`
		} `json:"counts"`
		Total     int `json:"total"`
		NeedsCare int `json:"needsCare"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err = client.Get(serverURL + "/monitor/websites/data")
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(response.Body).Decode(&listSnapshot)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if listSnapshot.Counts.Down == 1 &&
			len(listSnapshot.Alerts) == 1 &&
			listSnapshot.Alerts[0].FailureCount >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if listSnapshot.Counts.Down != 1 || listSnapshot.NeedsCare != 1 ||
		listSnapshot.Total != 1 || len(listSnapshot.Monitors) != 1 ||
		len(listSnapshot.Alerts) != 1 {
		t.Fatalf("list snapshot = %#v", listSnapshot)
	}
	if listSnapshot.Alerts[0].FailureCount < 2 ||
		listSnapshot.Alerts[0].NextCheckAt.IsZero() ||
		listSnapshot.Alerts[0].IncidentStartedAt.IsZero() {
		t.Fatalf("alert evidence = %#v", listSnapshot.Alerts[0])
	}

	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	listPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(listPage, []byte(`<code class="website-alert__url">http://127.0.0.1:1/</code>`)) {
		t.Fatalf("attention row does not show the monitored URL: %s", listPage)
	}

	response, err = client.Get(serverURL + location + "/data")
	if err != nil {
		t.Fatal(err)
	}
	var detailSnapshot struct {
		ID                 string
		DetailAvailability []struct {
			State websitemonitor.Availability
			Title string
		}
		AvailabilityPercent float64
		RecentChecks        []websitemonitor.Evidence
		IncidentCount       int
		CurrentIncident     *websitemonitor.IncidentSnapshot
		AverageLatencyLabel string
		P95LatencyLabel     string
		AvailabilityLabel   string
	}
	if err := json.NewDecoder(response.Body).Decode(&detailSnapshot); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if detailSnapshot.ID == "" || len(detailSnapshot.DetailAvailability) != 72 ||
		len(detailSnapshot.RecentChecks) < 2 || detailSnapshot.IncidentCount != 1 ||
		detailSnapshot.CurrentIncident == nil {
		t.Fatalf("detail snapshot = %#v", detailSnapshot)
	}

	response, err = client.Get(serverURL + "/monitor/websites/missing/data")
	if err != nil {
		t.Fatal(err)
	}
	var missingPayload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&missingPayload); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound ||
		missingPayload.Error.Code != string(websitemonitor.ErrorNotFound) {
		t.Fatalf("missing detail status=%d payload=%#v", response.StatusCode, missingPayload)
	}
}

func setWebsiteTestLocale(t *testing.T, client *http.Client, serverURL, locale string) {
	t.Helper()
	baseURL, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar.SetCookies(baseURL, []*http.Cookie{{
		Name: "scriptboard_locale", Value: locale, Path: "/",
	}})
}

type websiteProbeFunc func(context.Context, websitemonitor.Config) websitemonitor.Evidence

func (function websiteProbeFunc) Check(ctx context.Context, config websitemonitor.Config) websitemonitor.Evidence {
	return function(ctx, config)
}
