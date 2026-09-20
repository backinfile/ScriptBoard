package web_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 权限闸门：含 script/uses 节点的流程卡片需要执行管理权限。
// operator 无卡片管理权限由路由层拒绝（403）；maintainer 具备执行管理权限可保存。
func TestCustomDashboardFlowScriptNodePermissionGate(t *testing.T) {
	fixture := createFlowFixture(t, false)
	scriptYAML := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: 脚本\n      language: " + flowWebLanguage() + "\n      script: echo hi\n"
	createURL := fixture.serverURL + "/config/dashboards/" + fixture.dashboard + "/cards"
	post := func(client *http.Client, values url.Values) (int, []byte) {
		t.Helper()
		token := dashboardActionTokenFromPage(t, client, fixture.serverURL+"/config/dashboards?dashboard="+fixture.dashboard)
		values.Set("csrf_token", token)
		response, err := client.PostForm(createURL, values)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response.StatusCode, body
	}

	// operator 保存 script 流程卡片 → 403。
	operator := createRoleUserClient(t, fixture.client, fixture.serverURL, "gate-operator", "operator")
	status, _ := post(operator, url.Values{"name": {"脚本流程"}, "type": {"flow"}, "flow_yaml": {scriptYAML}})
	if status != http.StatusForbidden {
		t.Fatalf("operator script flow status=%d", status)
	}

	// maintainer 保存 script 流程卡片成功；更新也成功。
	maintainer := createRoleUserClient(t, fixture.client, fixture.serverURL, "gate-maintainer", "maintainer")
	status, body := post(maintainer, url.Values{"name": {"脚本流程"}, "type": {"flow"}, "flow_yaml": {scriptYAML}})
	if status != http.StatusSeeOther {
		t.Fatalf("maintainer script flow status=%d body=%s", status, body)
	}
	cardID := fixture.flowCardID(t)
	response, err := maintainer.PostForm(fixture.serverURL+"/config/dashboard-cards/"+cardID, url.Values{
		"csrf_token": {dashboardActionTokenFromPage(t, maintainer, fixture.serverURL+"/config/dashboards?dashboard="+fixture.dashboard)},
		"name":       {"脚本流程"}, "type": {"flow"}, "flow_yaml": {scriptYAML},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("maintainer update status=%d", response.StatusCode)
	}
}

// flowWebLanguage 返回当前平台可用的脚本语言。
func flowWebLanguage() string {
	if runtime.GOOS == "windows" {
		return "batch"
	}
	return "shell"
}

// dashboardActionTokenFromPage 从指定页面提取 CSRF token。
func dashboardActionTokenFromPage(t *testing.T, client *http.Client, pageURL string) string {
	t.Helper()
	response, err := client.Get(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return formToken(t, page)
}

// run 节点的 with 应作为执行参数（SCRIPTBOARD_PARAM_*）传入快捷执行项。
func TestCustomDashboardFlowRunNodeWithParams(t *testing.T) {
	fixture := createFlowFixture(t, false)
	outPath := filepath.Join(fixture.managed, "flow-param-out.txt")
	var script string
	name := "flow-params"
	if runtime.GOOS == "windows" {
		name += ".cmd"
		script = "@echo %SCRIPTBOARD_PARAM_TARGET%>\"" + outPath + "\"\r\n"
	} else {
		name += ".sh"
		script = "#!/bin/sh\nprintf %s \"$SCRIPTBOARD_PARAM_TARGET\" > \"" + filepath.ToSlash(outPath) + "\"\n"
	}
	scriptPath := filepath.Join(fixture.managed, name)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	token := fixture.formPageToken(t)
	response, err := fixture.client.PostForm(fixture.serverURL+"/config/quick-runs", url.Values{
		"csrf_token": {token}, "name": {"flow-params"}, "script": {scriptPath},
		"params_json": {`[{"name":"target","type":"string","default":"from-default"}]`},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create params quick run status=%d", response.StatusCode)
	}

	status, _ := fixture.createFlowCard(t, "version: 1\nflow:\n  nodes:\n    - id: a\n      name: 参数\n      run: flow-params\n      with: {target: from-flow}\n")
	if status != http.StatusSeeOther {
		t.Fatalf("create flow card status=%d", status)
	}
	cardID := fixture.flowCardID(t)
	response, err = fixture.client.Get(fixture.serverURL + "/monitor/dashboard/" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	runToken := dashboardActionToken(t, page)
	response, err = fixture.client.PostForm(fixture.serverURL+"/config/dashboard-cards/"+cardID+"/flow/run", url.Values{"csrf_token": {runToken}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("flow run status=%d", response.StatusCode)
	}

	events, err := fixture.client.Get(fixture.serverURL + "/config/dashboard-cards/" + cardID + "/flow/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	reader := bufio.NewReader(events.Body)
	var final map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		event := readSSEEvent(t, reader, 5*time.Second)
		if event.event != "flow" {
			continue
		}
		if err := json.Unmarshal([]byte(event.data), &final); err != nil {
			t.Fatalf("bad SSE payload: %v", err)
		}
		if finished, _ := final["finished"].(bool); finished {
			break
		}
	}
	if final["status"] != "succeeded" {
		t.Fatalf("final snapshot=%v", final)
	}
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != "from-flow" {
		t.Fatalf("param value=%q, want from-flow", content)
	}
}

// script/uses 节点端到端：一次性脚本与内置节点都写入卡片工作区。
func TestCustomDashboardFlowScriptAndUsesEndToEnd(t *testing.T) {
	fixture := createFlowFixture(t, false)
	var script string
	if runtime.GOOS == "windows" {
		script = "@echo from-script> script-out.txt\r\n"
	} else {
		script = "#!/bin/sh\necho from-script > script-out.txt\n"
	}
	yamlText := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: 脚本\n      language: " + flowWebLanguage() + "\n      script: |\n        " + strings.ReplaceAll(script, "\n", "\n        ") + "\n    - id: b\n      name: 内置\n      needs: [a]\n      uses: write-file\n      with: {path: builtin-out.txt, content: from-builtin}\n"
	status, body := fixture.createFlowCard(t, yamlText)
	if status != http.StatusSeeOther {
		t.Fatalf("create flow card status=%d body=%s", status, body)
	}
	cardID := fixture.flowCardID(t)
	response, err := fixture.client.Get(fixture.serverURL + "/monitor/dashboard/" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	// 节点类型标识：script 用 code 图标、uses(write-file) 用 file-plus-2 图标。
	for _, marker := range []string{`data-kind="script"`, `data-lucide="code"`, `data-kind="uses"`, `data-lucide="file-plus-2"`} {
		if !strings.Contains(string(page), marker) {
			t.Fatalf("monitor page missing %q: %s", marker, page)
		}
	}
	// 管理页摘要标注含内置/脚本节点。
	manage, err := fixture.client.Get(fixture.serverURL + "/config/dashboards?dashboard=" + fixture.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	managePage, _ := io.ReadAll(manage.Body)
	manage.Body.Close()
	if !strings.Contains(string(managePage), "2 个节点 · 含内置/脚本") {
		t.Fatalf("manage page summary missing custom-node hint: %s", managePage)
	}
	response, err = fixture.client.PostForm(fixture.serverURL+"/config/dashboard-cards/"+cardID+"/flow/run", url.Values{"csrf_token": {dashboardActionToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("flow run status=%d", response.StatusCode)
	}

	events, err := fixture.client.Get(fixture.serverURL + "/config/dashboard-cards/" + cardID + "/flow/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	reader := bufio.NewReader(events.Body)
	var final map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		event := readSSEEvent(t, reader, 5*time.Second)
		if event.event != "flow" {
			continue
		}
		if err := json.Unmarshal([]byte(event.data), &final); err != nil {
			t.Fatalf("bad SSE payload: %v", err)
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
	// Both inline scripts and builtins produce auditable Runner records.
	first, _ := nodes[0].(map[string]any)
	second, _ := nodes[1].(map[string]any)
	if first["runId"] == "" || first["status"] != "succeeded" {
		t.Fatalf("script node=%v", first)
	}
	if second["status"] != "succeeded" || second["runId"] == nil || second["runId"] == "" {
		t.Fatalf("builtin node=%v", second)
	}
	workspace := filepath.Join(fixture.state, "flow-workspaces", cardID)
	for name, want := range map[string]string{"script-out.txt": "from-script", "builtin-out.txt": "from-builtin"} {
		content, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil || strings.TrimSpace(string(content)) != want {
			t.Fatalf("%s content=%q err=%v", name, content, err)
		}
	}
}

func TestFlowYAMLSaveRetainsActionButtons(t *testing.T) {
	f := createFlowFixture(t, false)
	response, err := f.client.Get(f.serverURL + "/config/dashboards?dashboard=" + f.dashboard)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	response, err = f.client.PostForm(f.serverURL+"/config/dashboards/"+f.dashboard+"/cards", url.Values{"csrf_token": {formToken(t, body)}, "name": {"flow with actions"}, "type": {"flow"}, "flow_yaml": {"flow:\n  nodes:\n    - id: wait\n      name: wait\n      uses: sleep\n      with: {seconds: \"0\"}\n"}, "actions_json": {"[{\"id\":\"hook\",\"label\":\"hook\",\"kind\":\"browser_http\",\"method\":\"POST\",\"url\":\"https://example.com/hook\"}]"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("save=%d %s", response.StatusCode, body)
	}
	response, err = f.client.Get(f.serverURL + "/config/dashboards/" + f.dashboard + "/export?selection=" + f.flowCardID(t))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "example.com/hook") {
		t.Fatalf("actions lost: %s", body)
	}
}
