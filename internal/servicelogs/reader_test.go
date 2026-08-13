package servicelogs

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/logstream"
)

func TestLinuxJournalIsFixedBoundedFilteredAndRedacted(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	runner := &fixtureRunner{available: map[string]bool{"journalctl": true}, output: strings.Join([]string{
		`{"MESSAGE":"web ready token=super-secret-value","_SYSTEMD_UNIT":"scriptboard.service","PRIORITY":"6","__REALTIME_TIMESTAMP":"1786521600000000"}`,
		`{"MESSAGE":"broker warning","_SYSTEMD_UNIT":"scriptboard-broker.service","PRIORITY":"4","__REALTIME_TIMESTAMP":"1786521601000000"}`,
		`{"MESSAGE":"unrelated","_SYSTEMD_UNIT":"ssh.service","PRIORITY":"3","__REALTIME_TIMESTAMP":"1786521602000000"}`,
	}, "\n")}
	reader := New(Options{GOOS: "linux", Runner: runner, Now: func() time.Time { return now }})
	report, err := reader.List(context.Background(), Query{Service: "web", Range: "7d", Search: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Supported || report.Provider != "systemd journal" || len(report.Entries) != 1 || report.Entries[0].Service != "web" {
		t.Fatalf("report = %#v", report)
	}
	if strings.Contains(report.Entries[0].Message, "super-secret-value") {
		t.Fatalf("service log secret was not redacted: %q", report.Entries[0].Message)
	}
	call := strings.Join(runner.arguments, " ")
	for _, expected := range []string{"--lines 2000", "--since -7 days", "--unit scriptboard.service", "--unit scriptboard-runner.service"} {
		if !strings.Contains(call, expected) {
			t.Fatalf("journal call %q missing %q", call, expected)
		}
	}
}

func TestWindowsServiceEventsRequireFixedEncodedRecords(t *testing.T) {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	line := strings.Join([]string{"SBSERVICE", encode("2026-08-12T08:00:00Z"), encode("ScriptBoardRunner"), encode("7036"), encode("Error"), encode("runner failed password=hunter2"), "0"}, "|")
	entries, err := parseWindowsEntries(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Service != "runner" || entries[0].Severity != logstream.SeverityError || strings.Contains(entries[0].Message, "hunter2") {
		t.Fatalf("entries = %#v", entries)
	}
	if _, err := parseWindowsEntries(line + "\ninvalid"); err == nil {
		t.Fatal("malformed Windows service log output was accepted")
	}
}

func TestWindowsProviderTreatsNoMatchingEventsAsAnEmptySupportedReport(t *testing.T) {
	runner := &fixtureRunner{}
	reader := New(Options{GOOS: "windows", Runner: runner, Now: func() time.Time { return time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC) }})
	report, err := reader.List(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Supported || report.Provider != "Windows System Event Log" || len(report.Entries) != 0 {
		t.Fatalf("empty Windows report = %#v", report)
	}
	call := strings.Join(runner.arguments, " ")
	if !strings.Contains(call, "NoMatchingEventsFound") || !strings.Contains(call, "-MaxEvents 2000") || !strings.Contains(call, "ScriptBoardRunner") || !strings.HasSuffix(strings.TrimSpace(call), "exit 0") {
		t.Fatalf("Windows provider script is not fixed and empty-safe: %s", call)
	}
}

type fixtureRunner struct {
	available map[string]bool
	output    string
	err       error
	arguments []string
}

func (runner *fixtureRunner) Run(_ context.Context, name string, arguments ...string) (string, error) {
	runner.arguments = append([]string{name}, arguments...)
	return runner.output, runner.err
}

func (runner *fixtureRunner) LookPath(name string) bool { return runner.available[name] }
