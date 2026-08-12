package servicelogs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/logstream"
	"scriptboard/internal/processlaunch"
	"scriptboard/internal/secretredaction"
)

const (
	maximumSourceEntries = 2000
	maximumResultEntries = 500
	maximumMessageBytes  = 4 << 10
	maximumCommandOutput = 16 << 20
)

var serviceUnits = map[string]string{
	"web":    "scriptboard.service",
	"broker": "scriptboard-broker.service",
	"ai":     "scriptboard-ai.service",
	"runner": "scriptboard-runner.service",
}

var windowsServices = map[string]string{
	"web":    "ScriptBoard",
	"broker": "ScriptBoardBroker",
	"ai":     "ScriptBoardAI",
	"runner": "ScriptBoardRunner",
}

type Query struct {
	Service  string
	Range    string
	Severity logstream.Severity
	Search   string
}

type Entry struct {
	Time     time.Time
	Service  string
	Severity logstream.Severity
	EventID  string
	Message  string
	Source   string
}

type Report struct {
	Supported   bool
	Provider    string
	CollectedAt time.Time
	Entries     []Entry
	Truncated   bool
}

type Reader interface {
	List(context.Context, Query) (Report, error)
}

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
	LookPath(string) bool
}

type Options struct {
	GOOS   string
	Runner Runner
	Now    func() time.Time
}

type SystemReader struct {
	goos   string
	runner Runner
	now    func() time.Time
}

func New(options Options) *SystemReader {
	goos := strings.TrimSpace(options.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	runner := options.Runner
	if runner == nil {
		runner = commandRunner{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &SystemReader{goos: goos, runner: runner, now: now}
}

func (reader *SystemReader) List(ctx context.Context, raw Query) (Report, error) {
	query, err := normalizeQuery(raw)
	if err != nil {
		return Report{}, err
	}
	report := Report{CollectedAt: reader.now().UTC()}
	var entries []Entry
	switch reader.goos {
	case "linux":
		if !reader.runner.LookPath("journalctl") {
			return report, nil
		}
		report.Supported, report.Provider = true, "systemd journal"
		arguments := []string{"--no-pager", "--output=json", "--reverse", "--lines", strconv.Itoa(maximumSourceEntries), "--since", rangeSince(query.Range)}
		for _, service := range []string{"web", "broker", "ai", "runner"} {
			arguments = append(arguments, "--unit", serviceUnits[service])
		}
		output, runErr := reader.runner.Run(ctx, "journalctl", arguments...)
		if runErr != nil {
			return report, fmt.Errorf("read ScriptBoard systemd journal: %w", runErr)
		}
		entries = parseJournalEntries(output)
	case "windows":
		report.Supported, report.Provider = true, "Windows System Event Log"
		script := strings.ReplaceAll(windowsServiceLogScript, "__SINCE__", reader.now().UTC().Add(-rangeDuration(query.Range)).Format(time.RFC3339))
		output, runErr := reader.runner.Run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		if runErr != nil {
			return report, fmt.Errorf("read ScriptBoard Windows service events: %w", runErr)
		}
		entries, err = parseWindowsEntries(output)
		if err != nil {
			return report, err
		}
	default:
		return report, nil
	}
	report.Entries, report.Truncated = filterEntries(entries, query)
	return report, nil
}

func normalizeQuery(query Query) (Query, error) {
	query.Service = strings.TrimSpace(query.Service)
	if query.Service != "" {
		if _, ok := serviceUnits[query.Service]; !ok {
			return Query{}, errors.New("service log filter is invalid")
		}
	}
	if query.Range != "7d" && query.Range != "30d" {
		query.Range = "24h"
	}
	if query.Severity != "" && query.Severity != logstream.SeverityNormal && query.Severity != logstream.SeverityWarning && query.Severity != logstream.SeverityError {
		return Query{}, errors.New("service log severity filter is invalid")
	}
	query.Search = strings.TrimSpace(query.Search)
	if len(query.Search) > 128 || !utf8.ValidString(query.Search) || strings.IndexFunc(query.Search, unicode.IsControl) >= 0 {
		return Query{}, errors.New("service log search filter is invalid")
	}
	return query, nil
}

func rangeSince(value string) string {
	switch value {
	case "7d":
		return "-7 days"
	case "30d":
		return "-30 days"
	default:
		return "-24 hours"
	}
}

func rangeDuration(value string) time.Duration {
	switch value {
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

type journalRecord struct {
	Message  string `json:"MESSAGE"`
	Unit     string `json:"_SYSTEMD_UNIT"`
	Priority string `json:"PRIORITY"`
	Realtime string `json:"__REALTIME_TIMESTAMP"`
	SyslogID string `json:"SYSLOG_IDENTIFIER"`
}

func parseJournalEntries(output string) []Entry {
	entries := make([]Entry, 0)
	decoder := json.NewDecoder(strings.NewReader(output))
	for len(entries) < maximumSourceEntries {
		var record journalRecord
		if err := decoder.Decode(&record); err != nil {
			break
		}
		service := serviceForUnit(record.Unit)
		if service == "" {
			continue
		}
		message := boundedMessage(record.Message)
		entry := Entry{Service: service, Severity: severityForPriority(record.Priority), Message: message, Source: record.Unit}
		if micros, err := strconv.ParseInt(record.Realtime, 10, 64); err == nil && micros > 0 {
			entry.Time = time.UnixMicro(micros).UTC()
		}
		entries = append(entries, entry)
	}
	return entries
}

func serviceForUnit(unit string) string {
	for service, expected := range serviceUnits {
		if unit == expected {
			return service
		}
	}
	return ""
}

func severityForPriority(priority string) logstream.Severity {
	value, err := strconv.Atoi(priority)
	if err != nil {
		return logstream.SeverityNormal
	}
	if value <= 3 {
		return logstream.SeverityError
	}
	if value == 4 {
		return logstream.SeverityWarning
	}
	return logstream.SeverityNormal
}

func parseWindowsEntries(output string) ([]Entry, error) {
	entries := make([]Entry, 0)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 7 || fields[0] != "SBSERVICE" {
			return nil, errors.New("Windows service event provider returned an invalid bounded record")
		}
		decoded := make([]string, 5)
		for index := range decoded {
			body, err := base64.StdEncoding.DecodeString(fields[index+1])
			if err != nil || len(body) > maximumMessageBytes || !utf8.Valid(body) {
				return nil, errors.New("Windows service event provider returned an invalid encoded field")
			}
			decoded[index] = string(body)
		}
		when, err := time.Parse(time.RFC3339Nano, decoded[0])
		if err != nil {
			return nil, errors.New("Windows service event provider returned an invalid timestamp")
		}
		service := serviceForWindowsName(decoded[1])
		if service == "" {
			continue
		}
		severity := logstream.SeverityNormal
		switch strings.ToLower(decoded[3]) {
		case "error", "critical":
			severity = logstream.SeverityError
		case "warning":
			severity = logstream.SeverityWarning
		}
		entries = append(entries, Entry{Time: when.UTC(), Service: service, EventID: decoded[2], Severity: severity, Message: boundedMessage(decoded[4]), Source: "Service Control Manager"})
		if len(entries) == maximumSourceEntries {
			break
		}
	}
	return entries, nil
}

func serviceForWindowsName(name string) string {
	for service, expected := range windowsServices {
		if strings.EqualFold(name, expected) {
			return service
		}
	}
	return ""
}

func boundedMessage(raw string) string {
	value := secretredaction.String(strings.ToValidUTF8(raw, "\uFFFD"))
	value = strings.Map(func(character rune) rune {
		if character == '\t' || !unicode.IsControl(character) {
			return character
		}
		return ' '
	}, value)
	if len(value) > maximumMessageBytes {
		value = value[:maximumMessageBytes]
		value = strings.ToValidUTF8(value, "\uFFFD")
	}
	return strings.TrimSpace(value)
}

func filterEntries(entries []Entry, query Query) ([]Entry, bool) {
	filtered := make([]Entry, 0, min(len(entries), maximumResultEntries))
	search := strings.ToLower(query.Search)
	truncated := false
	for _, entry := range entries {
		if query.Service != "" && entry.Service != query.Service || query.Severity != "" && entry.Severity != query.Severity {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.Message+" "+entry.EventID), search) {
			continue
		}
		if len(filtered) == maximumResultEntries {
			truncated = true
			break
		}
		filtered = append(filtered, entry)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Time.After(filtered[j].Time) })
	return filtered, truncated
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, arguments ...string) (string, error) {
	command, err := processlaunch.Prepare(processlaunch.Spec{Context: ctx, Executable: name, Arguments: arguments, Environment: processlaunch.EnvironmentExact, Env: append(os.Environ(), "LC_ALL=C", "LANG=C")})
	if err != nil {
		return "", err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := command.Start(); err != nil {
		return "", err
	}
	type readResult struct {
		body   []byte
		err    error
		stderr bool
	}
	results := make(chan readResult, 2)
	read := func(source io.Reader, isStderr bool) {
		body, readErr := io.ReadAll(io.LimitReader(source, maximumCommandOutput+1))
		results <- readResult{body: body, err: readErr, stderr: isStderr}
	}
	go read(stdout, false)
	go read(stderr, true)
	var output, errorOutput bytes.Buffer
	oversized := false
	for index := 0; index < 2; index++ {
		result := <-results
		if len(result.body) > maximumCommandOutput {
			oversized = true
			_ = command.Process.Kill()
			result.body = result.body[:maximumCommandOutput]
		}
		if result.stderr {
			errorOutput.Write(result.body)
		} else {
			output.Write(result.body)
		}
		if result.err != nil {
			_ = command.Process.Kill()
		}
	}
	waitErr := command.Wait()
	if oversized {
		return "", errors.New("service log provider output exceeds its 16 MiB limit")
	}
	if waitErr != nil {
		return output.String(), fmt.Errorf("%w: %s", waitErr, boundedMessage(errorOutput.String()))
	}
	return output.String(), nil
}

func (commandRunner) LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

const windowsServiceLogScript = `$ErrorActionPreference = 'Stop'
$utf8 = New-Object System.Text.UTF8Encoding($false)
$OutputEncoding = $utf8
[Console]::OutputEncoding = $utf8
$since = [DateTime]::Parse('__SINCE__').ToUniversalTime()
$names = @('ScriptBoard','ScriptBoardBroker','ScriptBoardAI','ScriptBoardRunner')
function Encode-Field([object]$value) {[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$value))}
$count = 0
$events = @()
try {
  $events = @(Get-WinEvent -FilterHashtable @{LogName='System';ProviderName='Service Control Manager';StartTime=$since} -MaxEvents 2000 -ErrorAction Stop)
} catch {
  if ([string]$_.FullyQualifiedErrorId -notlike 'NoMatchingEventsFound*') {throw}
}
foreach ($event in $events) {
  $matched = ''
  foreach ($name in $names) {if ($event.Message.IndexOf($name,[StringComparison]::OrdinalIgnoreCase) -ge 0) {$matched=$name;break}}
  if ([string]::IsNullOrWhiteSpace($matched)) {continue}
  $message = [string]$event.Message; if ($message.Length -gt 4096) {$message=$message.Substring(0,4096)}
  'SBSERVICE|' + (Encode-Field $event.TimeCreated.ToUniversalTime().ToString('o')) + '|' + (Encode-Field $matched) + '|' + (Encode-Field $event.Id) + '|' + (Encode-Field $event.LevelDisplayName) + '|' + (Encode-Field $message) + '|0'
  $count++; if ($count -ge 2000) {break}
}
exit 0`
