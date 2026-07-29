package app_test

import (
	"bytes"
	"context"
	"io"
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

	response, err = client.PostForm(serverURL+"/monitor/websites/nginx/import", url.Values{
		"csrf_token":  {formToken(t, preview)},
		"config_path": {configPath},
		"digest":      {hiddenValue(t, preview, "digest")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor/websites" {
		t.Fatalf("import status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
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

type websiteProbeFunc func(context.Context, websitemonitor.Config) websitemonitor.Evidence

func (function websiteProbeFunc) Check(ctx context.Context, config websitemonitor.Config) websitemonitor.Evidence {
	return function(ctx, config)
}
