package logstream_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/logstream"
)

func TestClassifySeverityUsesCaseInsensitiveSubstringMatchingWithErrorPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want logstream.Severity
	}{
		{name: "normal", line: "request completed", want: logstream.SeverityNormal},
		{name: "error anywhere", line: "request returned ERROR while retrying", want: logstream.SeverityError},
		{name: "fatal", line: "FATAL worker stopped", want: logstream.SeverityError},
		{name: "panic", line: "runtime: panic in worker", want: logstream.SeverityError},
		{name: "warn abbreviation", line: "cache WARN threshold", want: logstream.SeverityWarning},
		{name: "warning", line: "A Warning was emitted", want: logstream.SeverityWarning},
		{name: "error wins", line: "warning escalated to error", want: logstream.SeverityError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := logstream.ClassifySeverity(test.line); got != test.want {
				t.Fatalf("ClassifySeverity(%q) = %q, want %q", test.line, got, test.want)
			}
		})
	}
}

func TestCursorRoundTripsAndRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	want := logstream.Cursor{
		Kind: "file", SourceVersion: "volume:42", Offset: 9182,
		Time:   time.Date(2026, 7, 30, 12, 1, 2, 345, time.UTC),
		Source: logstream.SourceFile, Digest: "boundary",
	}
	encoded, err := logstream.EncodeCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := logstream.DecodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded cursor = %#v, want %#v", got, want)
	}
	if _, err := logstream.DecodeCursor("not-a-cursor"); !errors.Is(err, logstream.ErrInvalidCursor) {
		t.Fatalf("malformed cursor error = %v, want ErrInvalidCursor", err)
	}
}

func TestNewEntryDecodesAndEscapesUntrustedLogBytes(t *testing.T) {
	t.Parallel()

	entry := logstream.NewEntry(
		logstream.SourceFile,
		[]byte{'w', 'a', 'r', 'n', '\t', '\r', '\n', 0x00, 0xff},
		false,
	)
	if entry.Source != logstream.SourceFile {
		t.Fatalf("source = %q, want %q", entry.Source, logstream.SourceFile)
	}
	if entry.Severity != logstream.SeverityWarning {
		t.Fatalf("severity = %q, want warning", entry.Severity)
	}
	if !entry.EncodingError {
		t.Fatal("invalid UTF-8 was not marked")
	}
	if !strings.Contains(entry.Text, `\x00`) ||
		!strings.Contains(entry.Text, `\x0d`) ||
		!strings.Contains(entry.Text, `\x0a`) ||
		!strings.Contains(entry.Text, "\uFFFD") {
		t.Fatalf("sanitized text = %q, want escaped controls and replacement rune", entry.Text)
	}
	if !strings.Contains(entry.Text, "\t") {
		t.Fatalf("tab was not preserved: %q", entry.Text)
	}
}
