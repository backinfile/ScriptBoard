package quickrun

import (
	"strings"
	"testing"
)

func TestPlatformLanguagesAreExplicit(t *testing.T) {
	windows := PlatformLanguages("windows")
	if windows[0].ID != "powershell" || windows[1].ID != "batch" {
		t.Fatalf("unexpected Windows languages: %+v", windows)
	}
	linux := PlatformLanguages("linux")
	if linux[0].ID != "shell" {
		t.Fatalf("unexpected Unix languages: %+v", linux)
	}
}

func TestSourceAndTimeoutBounds(t *testing.T) {
	if err := ValidateSource(strings.Repeat("x", MaxSourceBytes+1)); err == nil {
		t.Fatal("expected source bound")
	}
	if _, err := ParseTimeout("86401"); err == nil {
		t.Fatal("expected timeout bound")
	}
	if timeout, err := ParseTimeout("60"); err != nil || timeout != 60 {
		t.Fatalf("unexpected timeout: %d, %v", timeout, err)
	}
}
