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

func TestEnhancedFileUploadUsesClosableResultsDialog(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("web/assets/app.js")
	if err != nil {
		t.Fatalf("read application script: %v", err)
	}
	source := string(script)
	for _, expected := range []string{
		`const result = await fetchDocument(form.action, { method: "POST", body: data })`,
		`function showUploadResults(main)`,
		`dialog.className = "upload-results-dialog"`,
		`dialog.showModal()`,
		`closeTaskPanel(false, false)`,
		`navigate(state.refreshURL, false, { preserveScroll: true })`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("enhanced upload result dialog is missing %q", expected)
		}
	}
	if strings.Contains(source, `HTMLFormElement.prototype.submit.call(form)`) {
		t.Fatal("enhanced upload still forces a full-page form submission")
	}
}
