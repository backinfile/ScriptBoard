package web_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// flow 的 http-request header_* 值导出时脱敏；script/uses 节点导入无需重映射。
func TestCustomDashboardFlowTransferWithBuiltinNodes(t *testing.T) {
	fixture := createFlowFixture(t, false)
	yamlText := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: 通知\n      uses: http-request\n      with: {url: \"https://example.test/hook\", header_XApiKey: \"t0psecret-plain\"}\n    - id: b\n      name: 脚本\n      needs: [a]\n      language: " + flowWebLanguage() + "\n      script: echo hi\n  post:\n    - name: 收尾\n      uses: sleep\n      with: {seconds: \"0\"}\n"
	status, body := fixture.createFlowCard(t, yamlText)
	if status != http.StatusSeeOther {
		t.Fatalf("create flow card status=%d body=%s", status, body)
	}
	cardID := fixture.flowCardID(t)

	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards/" + fixture.dashboard + "/export?selection=" + url.QueryEscape(cardID))
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export status=%d body=%s", response.StatusCode, exported)
	}
	// 脱敏：header_XApiKey 值不落盘，url 与 script 原样保留。
	if strings.Contains(string(exported), "t0psecret-plain") {
		t.Fatalf("header value leaked in export: %s", exported)
	}
	if !strings.Contains(string(exported), "[REDACTED]") || !strings.Contains(string(exported), "echo hi") {
		t.Fatalf("export should redact header and keep script: %s", exported)
	}
	var bundle map[string]any
	if err := json.Unmarshal(exported, &bundle); err != nil {
		t.Fatal(err)
	}

	// 导入到另一个面板：script/uses 节点无 runId，重映射跳过，校验通过。
	target := createSecondDashboard(t, fixture, "import-builtin")
	importResponse := importTransferBundle(t, fixture, target, bundle)
	importResponse.Body.Close()
	if importResponse.StatusCode != http.StatusSeeOther || strings.Contains(importResponse.Header.Get("Location"), "import_error") {
		t.Fatalf("import status=%d location=%q", importResponse.StatusCode, importResponse.Header.Get("Location"))
	}
	imported := importedCardConfig(t, fixture, target, "发布流程")
	flow, ok := imported["flow"].(map[string]any)
	if !ok {
		t.Fatalf("imported config=%v", imported)
	}
	nodes := flow["nodes"].([]any)
	first := nodes[0].(map[string]any)
	second := nodes[1].(map[string]any)
	if first["uses"] != "http-request" || first["runId"] != nil && first["runId"] != "" {
		t.Fatalf("imported uses node=%v", first)
	}
	if second["script"] != "echo hi\n" && second["script"] != "echo hi" {
		t.Fatalf("imported script node=%v", second)
	}
	post := flow["post"].([]any)[0].(map[string]any)
	if post["uses"] != "sleep" {
		t.Fatalf("imported post=%v", post)
	}
}
