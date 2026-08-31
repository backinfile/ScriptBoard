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

func TestApplicationBrandKeepsFiveCJKCharactersAndScalesLongerNames(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "five CJK characters", value: "脚本管理台", want: "brand-name--full"},
		{name: "longer CJK name", value: "脚本管理控制面板", want: "brand-name--compact"},
		{name: "default Latin name", value: "ScriptBoard", want: "brand-name--full"},
		{name: "very long name", value: strings.Repeat("界", maxApplicationNameRunes), want: "brand-name--minimum"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := brandNameSizeClass(test.value); got != test.want {
				t.Fatalf("brandNameSizeClass(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatalf("read application stylesheet: %v", err)
	}
	css := string(stylesheet)
	for _, expected := range []string{
		`.app-sidebar .brand-wordmark {`,
		`grid-template-columns: 34px minmax(0, 1fr);`,
		`.app-sidebar .brand-name--full { font-size: 1.05rem; }`,
		`.app-sidebar .brand-name--minimum { font-size: .7rem; }`,
	} {
		if !strings.Contains(css, expected) {
			t.Fatalf("application brand scaling is missing %q", expected)
		}
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

func TestDatabaseDetailStageUsesAvailableWidth(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatalf("read application stylesheet: %v", err)
	}

	css := string(stylesheet)
	for _, selector := range []string{`\.workspace\[data-database-workspace\]`} {
		pattern := regexp.MustCompile(selector + `\s*\{[^}]*width:\s*100%[^}]*max-width:\s*none`)
		if !pattern.MatchString(css) {
			t.Errorf("operational workspace %q does not fill the available application width", selector)
		}
	}
	for _, selector := range []string{`\.database-detail`} {
		pattern := regexp.MustCompile(selector + `\s*\{[^}]*width:\s*100%`)
		if !pattern.MatchString(css) {
			t.Errorf("right-hand content selector %q does not use the available width", selector)
		}
	}
}
