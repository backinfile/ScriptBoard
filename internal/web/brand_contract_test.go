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

func TestAssistantAndDatabaseDetailStagesUseAvailableWidth(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatalf("read application stylesheet: %v", err)
	}

	css := string(stylesheet)
	for _, selector := range []string{`\.assistant-workspace`, `\.workspace\[data-database-workspace\]`} {
		pattern := regexp.MustCompile(selector + `\s*\{[^}]*width:\s*100%[^}]*max-width:\s*none`)
		if !pattern.MatchString(css) {
			t.Errorf("operational workspace %q does not fill the available application width", selector)
		}
	}
	for _, selector := range []string{`\.assistant-transcript`, `\.assistant-composer-zone`, `\.assistant-message--assistant`, `\.database-detail`} {
		pattern := regexp.MustCompile(selector + `\s*\{[^}]*width:\s*100%`)
		if !pattern.MatchString(css) {
			t.Errorf("right-hand content selector %q does not use the available width", selector)
		}
	}
}
