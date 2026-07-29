package app_test

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
	"strings"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/websitemonitor"
)

func TestAdminCreatesWebsiteMonitorAndReadsItsResult(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
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
	for _, expected := range []string{"编辑网站", `action="` + location + `"`, "http://127.0.0.1:8080/health"} {
		if !bytes.Contains(editPage, []byte(expected)) {
			t.Fatalf("edit page does not contain %q: %s", expected, editPage)
		}
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
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
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
	for _, expected := range []string{"imported.local", "查找结果不会自动加入监控", "加入监控"} {
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
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
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
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
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
		"Status codes must be numbers from 100 through 599",
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
		"1 minute", "10 seconds",
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
		"Website Monitoring", "Scan Nginx", "Add Website",
		`aria-label="Complete website status ledger"`, "1 website", "0 ms",
	} {
		if !bytes.Contains(listPage, []byte(expected)) {
			t.Fatalf("English list page does not contain %q: %s", expected, listPage)
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
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
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
		if listSnapshot.Counts.Down == 1 {
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
