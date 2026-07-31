package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/websitemonitor"
)

func TestSummarizeWebsiteShellStatusPrioritizesConfirmedFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		states     []websitemonitor.State
		wantState  string
		wantDown   int
		wantVerify int
	}{
		{
			name:       "confirmed failures win",
			states:     []websitemonitor.State{websitemonitor.StateDown, websitemonitor.StateVerifying, websitemonitor.StateDown, websitemonitor.StateUp},
			wantState:  "down",
			wantDown:   2,
			wantVerify: 1,
		},
		{
			name:       "verification is visible before confirmation",
			states:     []websitemonitor.State{websitemonitor.StatePaused, websitemonitor.StateVerifying, websitemonitor.StateUp},
			wantState:  "verifying",
			wantVerify: 1,
		},
		{
			name:       "pending first check is visible",
			states:     []websitemonitor.State{websitemonitor.StatePending, websitemonitor.StatePaused, websitemonitor.StateUp},
			wantState:  "verifying",
			wantVerify: 1,
		},
		{
			name:      "healthy and paused states stay quiet",
			states:    []websitemonitor.State{websitemonitor.StatePaused, websitemonitor.StateUp},
			wantState: "up",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			monitors := make([]websitemonitor.Monitor, 0, len(test.states))
			for _, state := range test.states {
				monitors = append(monitors, websitemonitor.Monitor{State: state})
			}

			state, down, verifying := summarizeWebsiteShellStatus(monitors)
			if state != test.wantState || down != test.wantDown || verifying != test.wantVerify {
				t.Fatalf("summary=(%q, %d, %d), want (%q, %d, %d)", state, down, verifying, test.wantState, test.wantDown, test.wantVerify)
			}
		})
	}
}

func TestLoadShellStatusIncludesWebsiteFailuresAndVerifications(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := Open(Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })

	now := time.Now().UTC()
	nextCheckAt := now.Add(time.Hour).UnixNano()
	for index, state := range []websitemonitor.State{
		websitemonitor.StateDown,
		websitemonitor.StateDown,
		websitemonitor.StateVerifying,
		websitemonitor.StateUp,
	} {
		_, err := application.db.Exec(`INSERT INTO website_monitors (
			id, name, scope, kind, url, config_json, frequency_seconds, timeout_seconds,
			sort_order, state, next_check_at, created_at, updated_at
		) VALUES (?, ?, 'external', 'http', ?, '{}', 60, 10, ?, ?, ?, ?, ?)`,
			"shell-signal-"+string(rune('a'+index)),
			"Shell signal "+string(rune('A'+index)),
			"https://example.invalid/"+string(rune('a'+index)),
			index,
			state,
			nextCheckAt,
			now.UnixNano(),
			now.UnixNano(),
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	status, err := application.loadShellStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.WebsiteState != "down" || status.WebsiteDown != 2 || status.WebsiteVerifying != 1 {
		t.Fatalf("website status=%+v, want two down and one verifying", status)
	}
}

func TestApplicationShellRendersWebsiteSignalCopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      applicationShellData
		want      []string
		notWanted string
	}{
		{
			name: "confirmed failures",
			data: applicationShellData{
				Locale:           localeEnglishUS,
				WebsiteState:     "down",
				WebsiteDown:      2,
				WebsiteVerifying: 1,
			},
			want: []string{
				`aria-label="Current status"`,
				`data-shell-attention-item="websites"`,
				`data-state="down"`,
				`2 websites down`,
				`1 website under verification`,
			},
			notWanted: "Website monitoring normal",
		},
		{
			name: "quiet normal state",
			data: applicationShellData{
				Locale:       localeEnglishUS,
				StatusState:  "current",
				WebsiteState: "up",
			},
			want: []string{
				`aria-label="Current status"`,
				`data-shell-attention-empty`,
				`Nothing needs attention`,
				`data-shell-attention-item="websites" hidden`,
			},
			notWanted: "websites down",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var rendered bytes.Buffer
			if err := applicationShellTemplate.Execute(&rendered, test.data); err != nil {
				t.Fatal(err)
			}
			page := rendered.String()
			for _, expected := range test.want {
				if !strings.Contains(page, expected) {
					t.Fatalf("shell missing %q: %s", expected, page)
				}
			}
			if strings.Contains(page, test.notWanted) {
				t.Fatalf("shell unexpectedly contains %q: %s", test.notWanted, page)
			}
		})
	}
}
