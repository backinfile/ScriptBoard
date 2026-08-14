package web

import (
	"strings"
	"testing"
)

func TestGlobalKeyboardShortcutGuardsMissingEventKey(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatalf("read application script: %v", err)
	}
	source := string(script)
	guarded := `typeof event.key === "string" && event.key.toLowerCase() === "p"`
	if !strings.Contains(source, guarded) {
		t.Fatalf("global pause shortcut does not guard a missing event.key")
	}
}

func TestImportSuccessUsesDOMConstructionAndSameOriginURLValidation(t *testing.T) {
	t.Parallel()
	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	if strings.Contains(source, "success.innerHTML=") || !strings.Contains(source, "safeInternalURL(payload?.redirectURL") {
		t.Fatal("website import success still interpolates markup or skips URL validation")
	}
}

func TestEnhancedFileUploadUsesClosableResultsDialog(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
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

func TestCustomDashboardDrawerWaitsForItsActualTransition(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatalf("read application script: %v", err)
	}
	source := string(script)
	for _, expected := range []string{
		`panel.addEventListener("transitionend", finish)`,
		`event.propertyName !== "transform"`,
		`clearDashboardDrawerCloseWait(drawer)`,
		`summaryDrawer.classList.contains("is-closing")`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("custom dashboard drawer animation is missing %q", expected)
		}
	}
	if strings.Contains(source, `const delay = window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 220`) {
		t.Fatal("custom dashboard drawer still closes on a fixed timer")
	}
}

func TestCustomDashboardDrawerStartsOpeningOnTheNextPaint(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatalf("read application script: %v", err)
	}
	source := string(script)
	if strings.Contains(source, `window.requestAnimationFrame(() => window.requestAnimationFrame(() => {`) {
		t.Fatal("custom dashboard drawer delays its entrance for two paint frames")
	}
	if !strings.Contains(source, `window.requestAnimationFrame(() => {`) {
		t.Fatal("custom dashboard drawer does not cross one paint boundary before entering")
	}
}

func TestDrawersUseSharedNonBouncingMotionTokens(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatalf("read application stylesheet: %v", err)
	}
	source := string(stylesheet)
	for _, expected := range []string{
		`--drawer-offset: 28px`,
		`--drawer-enter-transition: 180ms ease-out`,
		`--drawer-exit-transition: 140ms ease-in`,
		`--drawer-scrim-transition: 150ms linear`,
		`transform: translateX(var(--drawer-offset))`,
		`:is(.kubernetes-drawer, .container-drawer)[open]`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("shared drawer motion is missing %q", expected)
		}
	}
}

func TestActionFailureStaysInTheCurrentPageErrorDialog(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatalf("read application script: %v", err)
	}
	source := string(script)
	for _, expected := range []string{
		`if (!result.response.ok && !isTaskValidation)`,
		`showServerError(result, {`,
		`includeClientErrors: true`,
		`fallbackMessage: words().submitFailed`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("action failure does not stay in the current-page error dialog: missing %q", expected)
		}
	}
}
