package managedfiles_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/logstream"
	"scriptboard/internal/managedfiles"
)

func TestLogSourceReadsTheLatestPageThenOlderLines(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var content strings.Builder
	for line := 1; line <= 650; line++ {
		switch line {
		case 151:
			_, _ = fmt.Fprintln(&content, "line 151 warning")
		case 650:
			_, _ = fmt.Fprintln(&content, "line 650 ERROR")
		default:
			_, _ = fmt.Fprintf(&content, "line %03d\n", line)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "service.log"), []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	source, err := managedfiles.Open(root).OpenLogSource("service.log")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Entries) != logstream.DefaultPageLines || !latest.HasMore || latest.Before == "" {
		t.Fatalf("latest page = %d entries, hasMore=%v before=%q", len(latest.Entries), latest.HasMore, latest.Before)
	}
	if latest.Entries[0].Text != "line 151 warning" || latest.Entries[0].Severity != logstream.SeverityWarning {
		t.Fatalf("first latest entry = %#v", latest.Entries[0])
	}
	if latest.Entries[len(latest.Entries)-1].Text != "line 650 ERROR" ||
		latest.Entries[len(latest.Entries)-1].Severity != logstream.SeverityError {
		t.Fatalf("last latest entry = %#v", latest.Entries[len(latest.Entries)-1])
	}

	older, err := source.History(context.Background(), latest.Before)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 150 || older.HasMore || older.Entries[0].Text != "line 001" ||
		older.Entries[len(older.Entries)-1].Text != "line 150" {
		t.Fatalf("older page = %#v", older)
	}
}

func TestLogHistoryDoesNotMapAnOldCursorToAReplacementFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "service.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := managedfiles.Open(root).OpenLogSource("service.log")
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(root, "service.log.1")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := source.History(context.Background(), page.Before); !errors.Is(err, managedfiles.ErrLogSourceChanged) {
		t.Fatalf("replacement history error = %v, want ErrLogSourceChanged", err)
	}
}

func TestLogSourceHistoryHandlesEmptyCRLFPartialAndLongLines(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := managedfiles.Open(root)
	if err := os.WriteFile(filepath.Join(root, "empty.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty, err := store.OpenLogSource("empty.log")
	if err != nil {
		t.Fatal(err)
	}
	page, err := empty.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 || page.HasMore || page.Before != "" {
		t.Fatalf("empty page = %#v", page)
	}

	if err := os.WriteFile(filepath.Join(root, "formats.log"), []byte("alpha\r\nbeta WARNING"), 0o644); err != nil {
		t.Fatal(err)
	}
	formats, err := store.OpenLogSource("formats.log")
	if err != nil {
		t.Fatal(err)
	}
	page, err = formats.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Text != "alpha" ||
		page.Entries[1].Text != "beta WARNING" ||
		page.Entries[1].Severity != logstream.SeverityWarning {
		t.Fatalf("format page = %#v", page)
	}

	longLine := strings.Repeat("x", logstream.MaxEntryBytes+17)
	if err := os.WriteFile(filepath.Join(root, "long.log"), []byte(longLine), 0o644); err != nil {
		t.Fatal(err)
	}
	longSource, err := store.OpenLogSource("long.log")
	if err != nil {
		t.Fatal(err)
	}
	page, err = longSource.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || len(page.Entries[0].Text) != 17 ||
		page.Entries[0].Continuation || !page.Entries[1].Continuation ||
		len(page.Entries[1].Text) != logstream.MaxEntryBytes {
		t.Fatalf("long-line page = %#v", page)
	}

	var bounded strings.Builder
	for line := 0; line < 5; line++ {
		bounded.WriteString(strings.Repeat("b", 220<<10))
		bounded.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(root, "bounded.log"), []byte(bounded.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	boundedSource, err := store.OpenLogSource("bounded.log")
	if err != nil {
		t.Fatal(err)
	}
	page, err = boundedSource.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	totalBytes := 0
	for _, entry := range page.Entries {
		totalBytes += len(entry.Text)
	}
	if totalBytes > logstream.DefaultPageBytes || !page.HasMore {
		t.Fatalf("bounded page bytes=%d hasMore=%v entries=%d", totalBytes, page.HasMore, len(page.Entries))
	}
}

func TestLogSourceFollowsAppendedLinesFromAHistoryCursor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "service.log")
	if err := os.WriteFile(path, []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := managedfiles.Open(root).OpenLogSource("service.log")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	after := latest.Entries[len(latest.Entries)-1].Cursor

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan logstream.Event, 4)
	done := make(chan error, 1)
	go func() {
		done <- source.Follow(ctx, after, func(event logstream.Event) error {
			events <- event
			return nil
		})
	}()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("worker warning\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind != logstream.EventEntry {
				continue
			}
			if event.Entry == nil || event.Entry.Text != "worker warning" ||
				event.Entry.Severity != logstream.SeverityWarning {
				t.Fatalf("event = %#v", event)
			}
			goto received
		case <-timeout:
			t.Fatal("timed out waiting for appended log line")
		}
	}
received:
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("follow error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not stop after cancellation")
	}
}

func TestLogSourceDetectsFastTruncationBeforeReadingTheNewFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "service.log")
	if err := os.WriteFile(path, []byte("0123456789\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := managedfiles.Open(root).OpenLogSource("service.log")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan logstream.Event, 8)
	go func() {
		_ = source.Follow(ctx, latest.Entries[len(latest.Entries)-1].Cursor, func(event logstream.Event) error {
			events <- event
			return nil
		})
	}()
	select {
	case event := <-events:
		if event.Kind != logstream.EventState || event.State != "live" {
			t.Fatalf("initial event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not become live")
	}

	if err := os.WriteFile(path, []byte("after truncate WARNING and more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(4 * time.Second)
	sawTruncation := false
	var observed []logstream.Event
	for {
		select {
		case event := <-events:
			observed = append(observed, event)
			if event.Kind == logstream.EventState && event.State == "truncated" {
				sawTruncation = true
			}
			if event.Kind == logstream.EventEntry {
				if event.Entry == nil || event.Entry.Text != "after truncate WARNING and more" ||
					event.Entry.Severity != logstream.SeverityWarning || !sawTruncation {
					t.Fatalf("truncation events lost content: event=%#v sawTruncation=%v", event, sawTruncation)
				}
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for truncated log content: %#v", observed)
		}
	}
}

func TestLogSourceReportsAGapWhenTruncationHappenedWhileDisconnected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "service.log")
	if err := os.WriteFile(path, []byte("old boundary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := managedfiles.Open(root).OpenLogSource("service.log")
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	after := page.Entries[len(page.Entries)-1].Cursor
	if err := os.WriteFile(path, []byte("replacement ERROR is longer than the old boundary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan logstream.Event, 6)
	go func() {
		_ = source.Follow(ctx, after, func(event logstream.Event) error {
			events <- event
			return nil
		})
	}()
	timeout := time.After(3 * time.Second)
	sawGap := false
	for {
		select {
		case event := <-events:
			if event.Kind == logstream.EventGap {
				sawGap = true
			}
			if event.Kind == logstream.EventEntry && event.Entry != nil {
				if !sawGap || event.Entry.Text != "replacement ERROR is longer than the old boundary" ||
					event.Entry.Severity != logstream.SeverityError {
					t.Fatalf("disconnected truncation events: gap=%v entry=%#v", sawGap, event.Entry)
				}
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for disconnected truncation recovery")
		}
	}
}

func TestLogSourceFollowsAReplacementAtTheSamePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "service.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := managedfiles.Open(root).OpenLogSource("service.log")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan logstream.Event, 8)
	go func() {
		_ = source.Follow(ctx, latest.Entries[len(latest.Entries)-1].Cursor, func(event logstream.Event) error {
			events <- event
			return nil
		})
	}()
	select {
	case event := <-events:
		if event.Kind != logstream.EventState || event.State != "live" {
			t.Fatalf("initial follow event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not become live")
	}

	rotatedPath := filepath.Join(root, "service.log.1")
	if err := os.Rename(path, rotatedPath); err != nil {
		t.Fatal(err)
	}
	rotated, err := os.OpenFile(rotatedPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.WriteString("old tail warning"); err != nil {
		_ = rotated.Close()
		t.Fatal(err)
	}
	if err := rotated.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new fatal line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(4 * time.Second)
	sawRotation := false
	sawDrainedTail := false
	for {
		select {
		case event := <-events:
			if event.Kind == logstream.EventEntry && event.Entry != nil &&
				event.Entry.Text == "old tail warning" &&
				event.Entry.Severity == logstream.SeverityWarning {
				sawDrainedTail = true
			}
			if event.Kind == logstream.EventState && event.State == "rotated" {
				sawRotation = true
			}
			if event.Kind == logstream.EventEntry && event.Entry != nil && event.Entry.Text == "new fatal line" {
				if !sawDrainedTail || !sawRotation || event.Entry.Severity != logstream.SeverityError {
					t.Fatalf("replacement events missing drained tail, rotation, or severity: event=%#v drained=%v rotation=%v", event, sawDrainedTail, sawRotation)
				}
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for replacement log content")
		}
	}
}

func TestLogSourceWaitsForADeletedPathAndFollowsItsRecreation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "service.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := managedfiles.Open(root).OpenLogSource("service.log")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan logstream.Event, 12)
	go func() {
		_ = source.Follow(ctx, latest.Entries[len(latest.Entries)-1].Cursor, func(event logstream.Event) error {
			events <- event
			return nil
		})
	}()
	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not become live")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitForLogState(t, events, "waiting", 3*time.Second)
	if err := os.WriteFile(path, []byte("rebuilt ERROR\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(4 * time.Second)
	sawRotation := false
	for {
		select {
		case event := <-events:
			if event.Kind == logstream.EventState && event.State == "rotated" {
				sawRotation = true
			}
			if event.Kind == logstream.EventEntry && event.Entry != nil {
				if event.Entry.Text != "rebuilt ERROR" ||
					event.Entry.Severity != logstream.SeverityError || !sawRotation {
					t.Fatalf("recreated-path events = %#v, sawRotation=%v", event, sawRotation)
				}
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for recreated log file")
		}
	}
}

func waitForLogState(
	t *testing.T,
	events <-chan logstream.Event,
	want string,
	timeout time.Duration,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind == logstream.EventState && event.State == want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for log state %q", want)
		}
	}
}
