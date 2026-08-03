package app

import (
	"strings"
	"testing"
)

func TestGlobalKeyboardShortcutGuardsMissingEventKey(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("web/assets/app.js")
	if err != nil {
		t.Fatalf("read application script: %v", err)
	}
	source := string(script)
	guarded := `typeof event.key === "string" && event.key.toLowerCase() === "p"`
	if !strings.Contains(source, guarded) {
		t.Fatalf("global pause shortcut does not guard a missing event.key")
	}
}
