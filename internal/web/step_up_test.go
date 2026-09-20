package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/identity"
)

func TestRecentAuthenticationValidityIsBounded(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, test := range []struct {
		name      string
		timestamp int64
		want      bool
	}{
		{"missing", 0, false},
		{"current", now.Unix(), true},
		{"window boundary", now.Add(-identity.RecentAuthenticationWindow).Unix(), true},
		{"expired", now.Add(-identity.RecentAuthenticationWindow - time.Second).Unix(), false},
		{"clock skew", now.Add(time.Minute).Unix(), true},
		{"future", now.Add(time.Minute + time.Second).Unix(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := identity.RecentAuthenticationValid(test.timestamp, now); got != test.want {
				t.Fatalf("valid=%v, want %v", got, test.want)
			}
		})
	}
}

func TestStepUpReturnTargetRejectsExternalAndRecursiveLocations(t *testing.T) {
	for _, unsafe := range []string{"", "https://attacker.example/", "//attacker.example/", "/%2f%2fattacker.example/", "/auth/step-up?return_to=/monitor", "/safe\\unsafe", "/safe%5cunsafe", "/safe\nunsafe"} {
		if got := safeStepUpReturnTo(unsafe); got != "/monitor" {
			t.Errorf("safeStepUpReturnTo(%q)=%q", unsafe, got)
		}
	}
	if got := safeStepUpReturnTo("/settings/users?view=active"); got != "/settings/users?view=active" {
		t.Fatalf("safe local target=%q", got)
	}
}

func TestStepUpProtectedFormsDoNotBypassDialogSubmission(t *testing.T) {
	t.Parallel()

	application := &App{}
	application.routes()
	formPattern := regexp.MustCompile(`<form\b[^>]*\bdata-native\b[^>]*>`)
	actionPattern := regexp.MustCompile(`\baction="([^"]+)"`)
	templateValuePattern := regexp.MustCompile(`{{[^}]+}}`)
	templates, err := fs.Glob(webFiles, "ui/templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range templates {
		template, readErr := webFiles.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, tag := range formPattern.FindAllString(string(template), -1) {
			match := actionPattern.FindStringSubmatch(tag)
			if len(match) != 2 || match[1] == "{{.Action}}" {
				continue
			}
			action := templateValuePattern.ReplaceAllString(match[1], "test-id")
			request := httptest.NewRequest(http.MethodPost, action, nil)
			spec, ok := declaredSpecForRequest(application.routeSpecs, request)
			if ok && spec.StepUp {
				t.Errorf("%s has a native form that bypasses the shared step-up dialog: %s", name, tag)
			}
		}
	}
}

func TestStandaloneStepUpFallbackSubmitsNatively(t *testing.T) {
	t.Parallel()

	template, err := webFiles.ReadFile("ui/templates/task-page.html")
	if err != nil {
		t.Fatal(err)
	}
	stepUpBranch := strings.Index(string(template), `{{else if eq .Kind "step-up"}}`)
	if stepUpBranch < 0 {
		t.Fatal("standalone step-up fallback is missing")
	}
	formStart := strings.Index(string(template)[stepUpBranch:], "<form ")
	if formStart < 0 {
		t.Fatal("standalone step-up fallback form is missing")
	}
	formStart += stepUpBranch
	formEnd := strings.Index(string(template)[formStart:], ">")
	if formEnd < 0 {
		t.Fatal("standalone step-up fallback form opening tag is incomplete")
	}
	if tag := string(template)[formStart : formStart+formEnd+1]; !strings.Contains(tag, "data-native") {
		t.Fatalf("standalone step-up fallback is intercepted as an inline challenge and cannot follow its return target: %s", tag)
	}
}
