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

func TestSecurityFirewallSortReplacesOnlyItsProgressiveSegment(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatalf("read application script: %v", err)
	}
	source := string(script)
	for _, contract := range []string{
		`event.target.closest(".security-sort-link")`,
		`event.stopPropagation()`,
		`await load("capabilities", ["firewall"], link.href, { pushState: true })`,
		`history.pushState({ pjax: true }, "", sourceURL)`,
	} {
		if !strings.Contains(source, contract) {
			t.Fatalf("security firewall sorting no longer satisfies local segment navigation contract %q", contract)
		}
	}
}

func TestMySQLMutationsRefreshOnlyTheDatabaseWorkspaceRegion(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	for _, contract := range []string{
		`form.dataset.asyncRefresh = "[data-mysql-instances-region]"`,
		`currentRegion.replaceWith(document.importNode(nextRegion, true))`,
		`[data-mysql-batch-backup-form]`,
	} {
		if !strings.Contains(source, contract) {
			t.Fatalf("MySQL local refresh contract missing %q", contract)
		}
	}
}

func TestMySQLPlanDeleteTriggerIsBoundInsideMySQLDrawerInitializer(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	start := strings.Index(source, "  function initMySQLDrawers(")
	end := strings.Index(source, "  function initExternalKeyManagers(")
	if start < 0 || end <= start {
		t.Fatal("MySQL drawer initializer boundary is missing")
	}
	initializer := source[start:end]
	for _, contract := range []string{
		`event.target.closest("[data-mysql-plan-delete-trigger]")`,
		`planDeleteDrawer.open = true`,
		`/resources/databases/plans/${encodeURIComponent(planID)}/delete`,
	} {
		if !strings.Contains(initializer, contract) {
			t.Fatalf("MySQL plan deletion is not bound inside its drawer initializer: missing %q", contract)
		}
	}
}

func TestMySQLInstanceCreationClosesItsDrawerAfterLocalRefresh(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	template, err := webFiles.ReadFile("ui/templates/mysql-databases.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), `action="/resources/databases/instances" data-async-close-drawer`) {
		t.Fatal("MySQL instance creation form does not request drawer closure after success")
	}
	if !strings.Contains(string(script), `form.hasAttribute("data-async-close-drawer")`) {
		t.Fatal("async form success does not honor drawer closure")
	}
}

func TestMySQLPlanDeleteActionLivesInsideTheEditDrawer(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	for _, contract := range []string{
		`root.querySelectorAll("[data-mysql-plan-delete-trigger]").forEach(trigger => {`,
		`const footer = editDrawer?.querySelector(".mysql-plan-form > footer")`,
		`trigger.classList.add("button--danger")`,
		`footer.prepend(trigger)`,
	} {
		if !strings.Contains(source, contract) {
			t.Fatalf("MySQL plan edit drawer does not own the delete action: missing %q", contract)
		}
	}
}

func TestConnectionTestResultsUsePopupDialogs(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	for _, contract := range []string{
		`function showConnectionTestDialog(form, message, state, returnFocus)`,
		`showConnectionTestDialog(form, message, "success", submitter)`,
		`showConnectionTestDialog(form, message, "error", submitter)`,
		`dialog.dataset.state = state`,
	} {
		if !strings.Contains(source, contract) {
			t.Fatalf("connection test popup contract missing %q", contract)
		}
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
		`const actionURL = formActionURL(form)`,
		`const result = await fetchDocument(actionURL, { method: "POST", body: data })`,
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
		`--drawer-offset: 10px`,
		`--drawer-enter-transition: 130ms ease-out`,
		`--drawer-exit-transition: 100ms ease-in`,
		`--drawer-scrim-transition: 110ms linear`,
		`transform: translateX(var(--drawer-offset))`,
		`:is(.kubernetes-drawer, .container-drawer)[open]`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("shared drawer motion is missing %q", expected)
		}
	}
	for _, obsolete := range []string{`transform:translateX(100%)`} {
		if strings.Contains(source, obsolete) {
			t.Fatalf("drawer motion still contains the exaggerated rule %q", obsolete)
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
