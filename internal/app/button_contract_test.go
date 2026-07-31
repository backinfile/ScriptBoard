package app

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	buttonTagPattern       = regexp.MustCompile(`(?i)<button\b[^>]*>`)
	buttonTypePattern      = regexp.MustCompile(`(?i)\btype="(?:button|submit|reset)"`)
	buttonClassPattern     = regexp.MustCompile(`(?i)<(?:a|button)\b[^>]*\bclass="([^"]*)"[^>]*>`)
	buttonModifierPattern  = regexp.MustCompile(`^button--(?:primary|quiet|danger|compact)$`)
	nestedHTMLTagPattern   = regexp.MustCompile(`(?s)<[^>]+>`)
	globalButtonRuleMarker = []byte(`.button, button, input[type="submit"]`)
)

func TestWebTemplatesDeclareEveryButtonType(t *testing.T) {
	t.Parallel()

	templates, err := fs.Glob(webFiles, "web/templates/*.html")
	if err != nil {
		t.Fatalf("list web templates: %v", err)
	}
	for _, path := range templates {
		source, err := webFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, tag := range buttonTagPattern.FindAllString(string(source), -1) {
			if !buttonTypePattern.MatchString(tag) {
				t.Errorf("%s contains a button without an explicit type: %s", path, strings.TrimSpace(tag))
			}
		}
	}
}

func TestButtonModifiersRequireTheExplicitButtonBase(t *testing.T) {
	t.Parallel()

	templates, err := fs.Glob(webFiles, "web/templates/*.html")
	if err != nil {
		t.Fatalf("list web templates: %v", err)
	}
	for _, path := range templates {
		source, err := webFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range buttonClassPattern.FindAllStringSubmatch(string(source), -1) {
			classes := strings.Fields(match[1])
			hasBase, hasModifier := false, false
			for _, class := range classes {
				hasBase = hasBase || class == "button"
				hasModifier = hasModifier || buttonModifierPattern.MatchString(class)
			}
			if hasModifier && !hasBase {
				t.Errorf("%s uses a button modifier without the .button base: %s", path, match[0])
			}
		}
	}

	stylesheet, err := webFiles.ReadFile("web/assets/app.css")
	if err != nil {
		t.Fatalf("read button stylesheet: %v", err)
	}
	if strings.Contains(string(stylesheet), string(globalButtonRuleMarker)) {
		t.Errorf("button visuals still depend on the global button element selector")
	}
}

func TestIconOnlyControlsHaveAccessibleNames(t *testing.T) {
	t.Parallel()

	templates, err := fs.Glob(webFiles, "web/templates/*.html")
	if err != nil {
		t.Fatalf("list web templates: %v", err)
	}
	for _, path := range templates {
		source, err := webFiles.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, element := range []string{"a", "button", "summary"} {
			pattern := regexp.MustCompile(`(?is)<` + element + `\b([^>]*)>(.*?)</` + element + `>`)
			for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
				visibleName := strings.TrimSpace(nestedHTMLTagPattern.ReplaceAllString(match[2], ""))
				if visibleName == "" && !strings.Contains(strings.ToLower(match[1]), "aria-label=") {
					t.Errorf("%s contains an icon-only <%s> without an accessible name: %s", path, element, match[0])
				}
			}
		}
	}
}

func TestCreateGroupButtonsShareTheSecondaryContract(t *testing.T) {
	t.Parallel()

	targets := []struct {
		path string
		href string
	}{
		{"web/templates/quick-runs.html", "/config/quick-runs/groups/new"},
		{"web/templates/schedules.html", "/config/schedules/groups/new"},
	}

	var expectedClasses string
	for _, target := range targets {
		source, err := webFiles.ReadFile(target.path)
		if err != nil {
			t.Fatalf("read %s: %v", target.path, err)
		}
		anchorPattern := regexp.MustCompile(`(?i)<a\b[^>]*\bhref="` + regexp.QuoteMeta(target.href) + `"[^>]*>`)
		anchor := anchorPattern.FindString(string(source))
		if anchor == "" {
			t.Fatalf("%s does not contain the create-group control", target.path)
		}
		classMatch := regexp.MustCompile(`(?i)\bclass="([^"]*)"`).FindStringSubmatch(anchor)
		if len(classMatch) != 2 {
			t.Fatalf("%s create-group control has no class attribute: %s", target.path, anchor)
		}
		classes := strings.Fields(classMatch[1])
		slices.Sort(classes)
		normalized := strings.Join(classes, " ")
		if slices.Contains(classes, "button--primary") {
			t.Errorf("%s create-group control must remain secondary: %s", target.path, anchor)
		}
		if expectedClasses == "" {
			expectedClasses = normalized
		} else if normalized != expectedClasses {
			t.Errorf("create-group controls use inconsistent classes: want %q, got %q in %s", expectedClasses, normalized, target.path)
		}
	}
}
