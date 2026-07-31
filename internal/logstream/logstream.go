package logstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidCursor = errors.New("invalid log cursor")

const cursorVersion = 1

const (
	DefaultPageLines = 500
	DefaultPageBytes = 1 << 20
	MaxEntryBytes    = 256 << 10
)

type Severity string

const (
	SeverityNormal  Severity = "normal"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type EntrySource string

const (
	SourceStdout   EntrySource = "stdout"
	SourceStderr   EntrySource = "stderr"
	SourceCombined EntrySource = "combined"
	SourceFile     EntrySource = "file"
)

type Entry struct {
	Cursor        string      `json:"cursor"`
	Time          *time.Time  `json:"time,omitempty"`
	Source        EntrySource `json:"source"`
	Severity      Severity    `json:"severity"`
	Text          string      `json:"text"`
	Continuation  bool        `json:"continuation,omitempty"`
	EncodingError bool        `json:"encodingError,omitempty"`
}

type Page struct {
	Entries       []Entry `json:"entries"`
	Before        string  `json:"before,omitempty"`
	HasMore       bool    `json:"hasMore"`
	SourceVersion string  `json:"sourceVersion"`
}

type Metadata struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Technical     string `json:"technical,omitempty"`
	SourceVersion string `json:"sourceVersion"`
	Running       bool   `json:"running"`
}

type EventKind string

const (
	EventEntry    EventKind = "entry"
	EventState    EventKind = "state"
	EventGap      EventKind = "gap"
	EventComplete EventKind = "complete"
)

type Event struct {
	Kind    EventKind `json:"kind"`
	Entry   *Entry    `json:"entry,omitempty"`
	State   string    `json:"state,omitempty"`
	Message string    `json:"message,omitempty"`
}

type Source interface {
	Metadata() Metadata
	History(context.Context, string) (Page, error)
	Follow(context.Context, string, func(Event) error) error
}

type Cursor struct {
	Kind          string      `json:"kind"`
	SourceVersion string      `json:"sourceVersion"`
	Offset        int64       `json:"offset,omitempty"`
	Time          time.Time   `json:"time,omitempty"`
	Source        EntrySource `json:"source,omitempty"`
	Digest        string      `json:"digest,omitempty"`
}

type cursorEnvelope struct {
	Version int `json:"v"`
	Cursor
}

func ClassifySeverity(line string) Severity {
	value := strings.ToLower(line)
	if strings.Contains(value, "error") ||
		strings.Contains(value, "fatal") ||
		strings.Contains(value, "panic") {
		return SeverityError
	}
	if strings.Contains(value, "warn") ||
		strings.Contains(value, "warning") {
		return SeverityWarning
	}
	return SeverityNormal
}

func NewEntry(source EntrySource, raw []byte, continuation bool) Entry {
	encodingError := !utf8.Valid(raw)
	decoded := strings.ToValidUTF8(string(raw), "\uFFFD")
	return Entry{
		Source: source, Severity: ClassifySeverity(decoded),
		Text: sanitizeControls(decoded), Continuation: continuation,
		EncodingError: encodingError,
	}
}

func EncodeCursor(cursor Cursor) (string, error) {
	if err := validateCursor(cursor); err != nil {
		return "", err
	}
	payload, err := json.Marshal(cursorEnvelope{Version: cursorVersion, Cursor: cursor})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecodeCursor(value string) (Cursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	var envelope cursorEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Cursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	if envelope.Version != cursorVersion {
		return Cursor{}, ErrInvalidCursor
	}
	if err := validateCursor(envelope.Cursor); err != nil {
		return Cursor{}, err
	}
	return envelope.Cursor, nil
}

func validateCursor(cursor Cursor) error {
	if (cursor.Kind != "file" && cursor.Kind != "docker") ||
		strings.TrimSpace(cursor.SourceVersion) == "" ||
		cursor.Offset < 0 {
		return ErrInvalidCursor
	}
	return nil
}

func sanitizeControls(value string) string {
	var result strings.Builder
	for _, character := range value {
		if character == '\t' || !unicode.IsControl(character) {
			result.WriteRune(character)
			continue
		}
		if character <= 0xff {
			_, _ = fmt.Fprintf(&result, `\x%02x`, character)
		} else {
			_, _ = fmt.Fprintf(&result, `\u%04x`, character)
		}
	}
	return result.String()
}
