package web_test

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	_ "scriptboard/internal/web"
)

func TestOverviewPageRendersDrawerSurface(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	page := string(body)
	for _, expected := range []string{
		`data-overview-drawer`, `role="dialog"`, `data-overview-node-form`,
		`name="csrf_token"`, `data-overview-drawer-content`, `data-overview-drawer-full`,
		`overview-drawer-scrim`, `data-form-title="Add machine"`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("overview page missing %q", expected)
		}
	}
	if strings.Contains(page, "overview.open_full_page") {
		t.Error("untranslated key leaked")
	}
	if count := strings.Count(page, `href="/monitor/nodes/new"`); count != 1 {
		t.Errorf("overview add-machine action count=%d, want one page-level action", count)
	}
}
