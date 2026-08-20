package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestApplicationBrandAllowsCJKGlyphHeight(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatalf("read application stylesheet: %v", err)
	}

	css := string(stylesheet)
	if !strings.Contains(css, "font: 680 1.05rem/1.25 var(--font-display);") {
		t.Fatal("application brand line height must leave room for CJK glyph metrics")
	}
}

func TestPrimaryWorkspacesShareTheApplicationContentWidth(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatalf("read application stylesheet: %v", err)
	}

	css := string(stylesheet)
	selectors := []string{
		`\.website-monitor-page`,
		`\.workspace\[data-mysql-workspace\]`,
		`\.applications-page`,
		`\.editor-page`,
		`\.custom-dashboard-workspace`,
		`\.kubernetes-page,\.kubernetes-connection-page`,
		`\.kubernetes-logs-page`,
		`\.container-page`,
	}
	for _, selector := range selectors {
		pattern := regexp.MustCompile(selector + `\s*\{[^}]*max-width:\s*var\(--content-max\)`)
		if !pattern.MatchString(css) {
			t.Errorf("primary workspace %q does not inherit --content-max", selector)
		}
	}
}
