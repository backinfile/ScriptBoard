package web_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 配置格式说明文档下载：需登录管理权限，以附件形式返回内嵌 Markdown，
// 内容需同时覆盖导入导出 JSON 与流程 YAML 两部分说明。
func TestDashboardConfigFormatDownload(t *testing.T) {
	fixture := createFlowFixture(t, false)
	response, err := fixture.client.Get(fixture.serverURL + "/config/dashboards/config-format")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "scriptboard-dashboard-config-format.md") {
		t.Fatalf("unexpected Content-Disposition: %q", disposition)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"scriptboard.custom-dashboard-nodes", "config.flow", "on_failure"} {
		if !strings.Contains(string(body), marker) {
			t.Fatalf("downloaded doc missing marker %q", marker)
		}
	}
}

// 内嵌下载副本与仓库 docs 下的正式文档保持一致，防止两处内容漂移。
func TestDashboardConfigFormatDocInSync(t *testing.T) {
	embedded, err := os.ReadFile("ui/assets/dashboard-config-format.md")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := os.ReadFile(filepath.Join("..", "..", "docs", "DASHBOARD-CONFIG-FORMAT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(embedded) != string(canonical) {
		t.Fatal("internal/web/ui/assets/dashboard-config-format.md 与 docs/DASHBOARD-CONFIG-FORMAT.md 不一致，请同步两处内容")
	}
}
