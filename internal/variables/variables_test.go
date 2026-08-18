package variables_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"scriptboard/internal/variables"
)

func TestParseTextPreservesTheStoredValue(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "production", " 多行\n文本 "} {
		parsed, err := variables.Parse(variables.KindText, value)
		if err != nil {
			t.Fatalf("Parse(%q): %v", value, err)
		}
		if parsed != value {
			t.Fatalf("Parse(%q) = %q", value, parsed)
		}
	}

	if _, err := variables.Parse(variables.KindText, strings.Repeat("x", 4097)); err == nil {
		t.Fatal("oversized text was accepted")
	}
}

func TestParseIntegerAcceptsDecimalSyntaxWithoutBusinessBounds(t *testing.T) {
	t.Parallel()

	for _, value := range []any{"0", "12", "-12", json.Number("9223372036854775808")} {
		want := fmt.Sprint(value)
		got, err := variables.Parse(variables.KindInteger, value)
		if err != nil || got != want {
			t.Fatalf("Parse(%v) = %q, %v; want %q", value, got, err, want)
		}
	}

	for _, value := range []any{"", "01", "+1", "1.0", "1e3", "--1"} {
		if _, err := variables.Parse(variables.KindInteger, value); err == nil {
			t.Fatalf("invalid integer %v was accepted", value)
		}
	}
}

func TestParseBoolAcceptsOnlyCanonicalValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		raw  any
		want string
	}{{"true", "true"}, {"false", "false"}, {true, "true"}, {false, "false"}} {
		got, err := variables.Parse(variables.KindBool, test.raw)
		if err != nil || got != test.want {
			t.Fatalf("Parse(%v) = %q, %v; want %q", test.raw, got, err, test.want)
		}
	}

	for _, value := range []any{"1", "yes", "TRUE", ""} {
		if _, err := variables.Parse(variables.KindBool, value); err == nil {
			t.Fatalf("invalid bool %v was accepted", value)
		}
	}
}

func TestParseFloatAcceptsPlainDecimalSyntax(t *testing.T) {
	t.Parallel()

	for _, value := range []any{"0", "-0.5", "3.14", json.Number("12345678901234567890.25")} {
		want := fmt.Sprint(value)
		got, err := variables.Parse(variables.KindFloat, value)
		if err != nil || got != want {
			t.Fatalf("Parse(%v) = %q, %v; want %q", value, got, err, want)
		}
	}

	for _, value := range []any{"", ".5", "1.", "01.2", "+1.2", "1e3", "NaN", "Inf"} {
		if _, err := variables.Parse(variables.KindFloat, value); err == nil {
			t.Fatalf("invalid float %v was accepted", value)
		}
	}
}

func TestParseVersionRequiresThreeCanonicalNumericParts(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0.0.0", "1.7.0", "12345678901234567890.2.3"} {
		got, err := variables.Parse(variables.KindVersion, value)
		if err != nil || got != value {
			t.Fatalf("Parse(%q) = %q, %v", value, got, err)
		}
	}

	for _, value := range []string{"1", "1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.-2.3", "1.2.3-beta"} {
		if _, err := variables.Parse(variables.KindVersion, value); err == nil {
			t.Fatalf("invalid version %q was accepted", value)
		}
	}
}

func TestParseRejectsUnsupportedKindsAndInvalidUTF8(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		kind variables.Kind
		raw  any
	}{
		{variables.Kind("unknown"), "value"},
		{variables.KindText, []byte("value")},
		{variables.KindText, string([]byte{0xff})},
	} {
		if _, err := variables.Parse(test.kind, test.raw); err == nil {
			t.Fatalf("Parse(%q, %v) succeeded", test.kind, test.raw)
		}
	}
	if _, err := variables.Parse(variables.Kind("unknown"), "value"); !errors.Is(err, variables.ErrInvalidKind) {
		t.Fatalf("unsupported kind error = %v", err)
	}
}
