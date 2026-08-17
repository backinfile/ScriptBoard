package web

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/runmanager"
)

func TestRunListItemViewBuildsScriptDirectoryURL(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	tests := []struct {
		name       string
		scriptPath string
		want       string
	}{
		{name: "host root", scriptPath: filepath.Join(root, "inspect.sh"), want: filesURL(root)},
		{name: "nested directory", scriptPath: filepath.Join(root, "ops", "inspect.sh"), want: filesURL(filepath.Join(root, "ops"))},
		{name: "reserved path characters", scriptPath: filepath.Join(root, "ops #1", "inspect.sh"), want: filesURL(filepath.Join(root, "ops #1"))},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			views := newRunListItemViews([]runmanager.Run{{ScriptPath: test.scriptPath}}, localeEnglishUS, time.Time{})
			if views[0].ScriptDirectoryURL != test.want {
				t.Fatalf("ScriptDirectoryURL=%q, want %q", views[0].ScriptDirectoryURL, test.want)
			}
		})
	}
}

func TestRunLogStartsAtLatestOutput(t *testing.T) {
	t.Parallel()
	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatalf("read app script: %v", err)
	}
	text := string(script)
	loadLatest := strings.Index(text, "    loadHistory(true).then(connect)")
	if loadLatest < 0 {
		t.Fatalf("Run log must position at the latest output before opening its event stream")
	}
	initRun := text[strings.Index(text, "  function initRun("):strings.Index(text, "  function initStatus(")]
	if !strings.Contains(initRun, `log.scrollTo({ top, behavior: "auto" });`) || strings.Contains(initRun, `behavior: "smooth"`) {
		t.Fatal("Run log jump controls must move immediately without smooth scrolling")
	}
	for _, expected := range []string{`data-run-duration`, `root.dataset.runStartedAt`, `renderDuration()`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Run duration must update beside the last-output age; missing %q", expected)
		}
	}
}

func TestRunLogUsesSeverityInsteadOfScriptLanguage(t *testing.T) {
	t.Parallel()
	page, err := webFiles.ReadFile("ui/templates/run.html")
	if err != nil {
		t.Fatalf("read run template: %v", err)
	}
	if !strings.Contains(string(page), `data-run-log`) {
		t.Fatalf("Run log must expose its semantic output region: %s", page)
	}
	if strings.Contains(string(page), `data-highlight-language`) || strings.Contains(string(page), `data-script-preview`) || strings.Contains(string(page), `language-`) {
		t.Fatalf("Run log must not opt into file-type syntax highlighting: %s", page)
	}
	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatalf("read app script: %v", err)
	}
	if !strings.Contains(string(script), `element.closest("[data-run-log]")`) || !strings.Contains(string(script), `span.dataset.severity = payload.severity || "normal"`) {
		t.Fatal("Run logs must skip script highlighting and render their log severity")
	}
}

func TestRunLogEventViewClassifiesSeverity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want string
	}{
		{text: "request complete", want: "normal"},
		{text: "WARNING cache nearing capacity", want: "warning"},
		{text: "fatal: worker stopped after warning", want: "error"},
	}
	for _, test := range tests {
		if got := string(newRunLogEventView(runmanager.Event{Data: test.text}).Severity); got != test.want {
			t.Errorf("severity for %q = %q, want %q", test.text, got, test.want)
		}
	}
}

func TestRunListItemViewBuildsDuration(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2*time.Minute + 7*time.Second)
	loadedAt := startedAt.Add(12 * time.Second)

	tests := []struct {
		name         string
		run          runmanager.Run
		wantDuration string
		wantPresent  bool
	}{
		{name: "completed", run: runmanager.Run{Status: "succeeded", StartedAt: &startedAt, FinishedAt: &finishedAt}, wantDuration: "2m 07s", wantPresent: true},
		{name: "active", run: runmanager.Run{Status: "running", StartedAt: &startedAt}, wantDuration: "12s", wantPresent: true},
		{name: "not started", run: runmanager.Run{Status: "starting"}},
		{name: "terminal without finish", run: runmanager.Run{Status: "failed", StartedAt: &startedAt}},
		{name: "invalid timestamps", run: runmanager.Run{Status: "succeeded", StartedAt: &finishedAt, FinishedAt: &startedAt}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			view := newRunListItemViews([]runmanager.Run{test.run}, localeEnglishUS, loadedAt)[0]
			if view.HasDuration != test.wantPresent || view.Duration != test.wantDuration {
				t.Fatalf("duration=(%q, %t), want (%q, %t)", view.Duration, view.HasDuration, test.wantDuration, test.wantPresent)
			}
		})
	}
}
