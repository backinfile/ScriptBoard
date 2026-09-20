package web_test

import (
	"bufio"
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
	"time"
)

// dashboardFlowFixture 搭建含快捷执行项与 flow 卡片的面板。
type dashboardFlowFixture struct {
	client    *http.Client
	serverURL string
	state     string
	managed   string
	dashboard string
	slug      string
}

func createFlowFixture(t *testing.T, slowNodes bool) dashboardFlowFixture {
	t.Helper()
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	state := filepath.Join(root, "state")
	client, base := authenticatedClient(t, managed, state)

	fastName, fastSource := "flow-step.sh", "#!/bin/sh\necho flow-step-ok\n"
	slowName, slowSource := "flow-slow.sh", "#!/bin/sh\nsleep 2\n"
	if runtime.GOOS == "windows" {
		fastName, fastSource = "flow-step.cmd", "@echo flow-step-ok\r\n"
		slowName, slowSource = "flow-slow.cmd", "@ping -n 3 127.0.0.1 >nul\r\n"
	}
	fastPath := filepath.Join(managed, fastName)
	slowPath := filepath.Join(managed, slowName)
	if err := os.WriteFile(fastPath, []byte(fastSource), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slowPath, []byte(slowSource), 0700); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(base + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	token := formToken(t, page)
	for _, quickRun := range []struct{ name, script string }{
		{"flow-build", fastPath}, {"flow-test", fastPath}, {"flow-cleanup", fastPath}, {"flow-slow", slowPath},
	} {
		response, err = client.PostForm(base+"/config/quick-runs", url.Values{
			"csrf_token": {token}, "name": {quickRun.name}, "script": {quickRun.script},
		})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("create quick run %s status=%d", quickRun.name, response.StatusCode)
		}
	}

	response, err = client.PostForm(base+"/config/dashboards", url.Values{
		"csrf_token": {token}, "name": {"流程面板"}, "slug": {"flow-board"},
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
	return dashboardFlowFixture{client: client, serverURL: base, state: state, managed: managed, dashboard: dashboardID, slug: "flow-board"}
}

func (fixture dashboardFlowFixture) formPageToken(t *testing.T) string {
	t.Helper()
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return formToken(t, page)
}

func (fixture dashboardFlowFixture) createFlowCard(t *testing.T, yamlText string) (int, []byte) {
	t.Helper()
	response, err := fixture.client.PostForm(fixture.serverURL+"/config/dashboards/"+fixture.dashboard+"/cards", url.Values{
		"csrf_token": {fixture.formPageToken(t)}, "name": {"发布流程"}, "type": {"flow"}, "flow_yaml": {yamlText},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return response.StatusCode, body
}

func (fixture dashboardFlowFixture) flowCardID(t *testing.T) string {
	t.Helper()
	// 管理页不渲染节点图，卡片 ID 取自编辑抽屉的表单 action。
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	match := regexp.MustCompile(`action="/config/dashboard-cards/([^/"]+)"`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("flow card missing on page: %s", page)
	}
	return string(match[1])
}

const flowFixtureYAML = `version: 1
flow:
  nodes:
    - id: build
      name: 构建
      run: flow-build
    - id: test
      name: 测试
      needs: [build]
      run: flow-test
  post:
    - name: 清理临时产物
      run: flow-cleanup
      when: always
`

func TestCustomDashboardFlowCardLifecycle(t *testing.T) {
	fixture := createFlowFixture(t, false)

	// 非法配置：逐条错误随 422 返回（422 渲染为错误页，引号会被 HTML 转义，断言不带引号）。
	for _, invalid := range []struct{ yaml, problem string }{
		{"version: [", "不是有效的 YAML"},
		{"version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: ghost-run}\n", "ghost-run"},
		{"version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: flow-build, needs: [b]}\n    - {id: b, name: B, run: flow-test, needs: [a]}\n", "循环"},
	} {
		status, body := fixture.createFlowCard(t, invalid.yaml)
		if status != http.StatusUnprocessableEntity || !strings.Contains(string(body), invalid.problem) {
			t.Fatalf("invalid yaml status=%d body=%s, want problem %q", status, body, invalid.problem)
		}
	}

	status, _ := fixture.createFlowCard(t, flowFixtureYAML)
	if status != http.StatusSeeOther {
		t.Fatalf("create flow card status=%d", status)
	}
	cardID := fixture.flowCardID(t)

	// 管理页：节点数徽标与 YAML 回填。
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(page), "2 nodes") || !strings.Contains(string(page), `name="flow_yaml"`) {
		t.Fatalf("manage page flow affordances missing: %s", page)
	}

	// 监控页：节点图 + 运行按钮；viewer 无按钮。
	operator := createRoleUserClient(t, fixture.client, fixture.serverURL, "flow-operator", "operator")
	response, err = operator.Get(fixture.serverURL + "/monitor/dashboard/" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(page), "data-dashboard-flow-run") || !strings.Contains(string(page), `data-flow-node="build"`) {
		t.Fatalf("monitor page flow graph missing: %s", page)
	}
	// 节点类型标识：run 节点带 data-kind 与 zap 图标，并预留消息行。
	for _, marker := range []string{`data-kind="run"`, `data-lucide="zap"`, "data-flow-node-message"} {
		if !strings.Contains(string(page), marker) {
			t.Fatalf("monitor page missing %q: %s", marker, page)
		}
	}
	monitorToken := dashboardActionToken(t, page)
	viewer := createRoleUserClient(t, fixture.client, fixture.serverURL, "flow-viewer", "viewer")
	response, err = viewer.Get(fixture.serverURL + "/monitor/dashboard/" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	viewerPage, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(viewerPage), "data-dashboard-flow-run") {
		t.Fatal("viewer must not see the flow run button")
	}

	runURL := fixture.serverURL + "/config/dashboard-cards/" + cardID + "/flow/run"
	response, err = viewer.PostForm(runURL, url.Values{"csrf_token": {"invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer flow run status=%d", response.StatusCode)
	}
	response, err = operator.PostForm(runURL, url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("flow run without csrf status=%d", response.StatusCode)
	}
	response, err = operator.PostForm(runURL, url.Values{"csrf_token": {monitorToken}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"flowRunId"`) {
		t.Fatalf("flow run status=%d body=%s", response.StatusCode, body)
	}

	// SSE：回放并推进到完成，节点与 post 状态齐全。
	events, err := fixture.client.Get(fixture.serverURL + "/config/dashboard-cards/" + cardID + "/flow/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	if !strings.HasPrefix(events.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events content-type=%q", events.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(events.Body)
	var final map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		event := readSSEEvent(t, reader, 5*time.Second)
		if event.event != "flow" {
			continue
		}
		if err := json.Unmarshal([]byte(event.data), &final); err != nil {
			t.Fatalf("bad SSE payload: %v data=%s", err, event.data)
		}
		if finished, _ := final["finished"].(bool); finished {
			break
		}
	}
	if final["status"] != "succeeded" {
		t.Fatalf("final snapshot=%v", final)
	}
	nodes, _ := final["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes=%v", final["nodes"])
	}
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if node["status"] != "succeeded" || node["runId"] == "" {
			t.Fatalf("node=%v", node)
		}
	}
	posts, _ := final["posts"].([]any)
	if len(posts) != 1 || posts[0].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("posts=%v", final["posts"])
	}

	// 公开页：只读节点结构，无运行按钮。
	status, _ = setDashboardVisibility(t, dashboardActionFixture{
		client: fixture.client, serverURL: fixture.serverURL, dashboard: fixture.dashboard,
	}, "public_read")
	if status != http.StatusSeeOther {
		t.Fatalf("public_read switch status=%d", status)
	}
	response, err = http.Get(fixture.serverURL + "/public/dashboard/" + fixture.slug)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	response.Body.Close()
	rendered := string(page)
	if !strings.Contains(rendered, `data-flow-node="build"`) {
		t.Fatalf("public page should render read-only flow structure: %s", page)
	}
	if strings.Contains(rendered, "data-dashboard-flow-run") || strings.Contains(rendered, "data-csrf-token") {
		t.Fatal("public page must not carry flow run controls or csrf tokens")
	}
}

func TestCustomDashboardFlowRunConflict(t *testing.T) {
	fixture := createFlowFixture(t, true)
	status, _ := fixture.createFlowCard(t, "version: 1\nflow:\n  nodes:\n    - {id: slow, name: 慢节点, run: flow-slow}\n")
	if status != http.StatusSeeOther {
		t.Fatalf("create slow flow card status=%d", status)
	}
	cardID := fixture.flowCardID(t)
	response, err := fixture.client.Get(fixture.serverURL + "/monitor/dashboard/" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	token := dashboardActionToken(t, page)
	runURL := fixture.serverURL + "/config/dashboard-cards/" + cardID + "/flow/run"
	response, err = fixture.client.PostForm(runURL, url.Values{"csrf_token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first run status=%d", response.StatusCode)
	}
	// 同一卡片运行期间再次触发 → 409。
	response, err = fixture.client.PostForm(runURL, url.Values{"csrf_token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("second run status=%d, want 409", response.StatusCode)
	}
	// 等慢节点跑完，避免泄漏到其它测试。
	time.Sleep(4 * time.Second)
}
