package recordnote

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	note, err := Normalize("  交接给夜班  ")
	if err != nil || note != "交接给夜班" {
		t.Fatalf("note=%q error=%v", note, err)
	}
	if _, err := Normalize(strings.Repeat("界", MaxRunes+1)); err == nil {
		t.Fatal("oversized note was accepted")
	}
}
