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
	showLatest := strings.Index(text, "    showLatestLog();")
	openEvents := strings.Index(text, "    if (!window.EventSource) return;")
	if showLatest < 0 || openEvents < 0 || showLatest > openEvents {
		t.Fatalf("Run log must position at the latest output before opening its event stream")
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
