package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestBuildReleaseRestoresHostTargetBeforeAssistantRuntime(t *testing.T) {
	script, err := os.ReadFile("build-release.ps1")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	runtimeCall := strings.Index(content, `& (Join-Path $PSScriptRoot "build-assistant-runtime.ps1")`)
	if runtimeCall < 0 {
		t.Fatal("assistant Runtime build call is missing")
	}
	formalBlock := strings.LastIndex(content[:runtimeCall], "if ($formalRelease) {")
	if formalBlock < 0 {
		t.Fatal("formal release block is missing")
	}
	setup := content[formalBlock:runtimeCall]
	for _, expected := range []string{
		`$env:GOOS = $originalGOOS`,
		`$env:GOARCH = $originalGOARCH`,
	} {
		if !strings.Contains(setup, expected) {
			t.Fatalf("formal Runtime build does not restore host target with %q", expected)
		}
	}
}

func TestBuildReleaseEmbedsUpdateKeyRevocations(t *testing.T) {
	script, err := os.ReadFile("build-release.ps1")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, expected := range []string{
		"SCRIPTBOARD_UPDATE_REVOKED_KEY_IDS",
		"scriptboard/internal/buildinfo.UpdateRevokedKeyIDs=$revokedKeyIDs",
		"SCRIPTBOARD_UPDATE_NEXT_SIGNING_KEY",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("release script does not contain %q", expected)
		}
	}
}

func TestWindowsSCMSecurityGateCoversManagedBoundary(t *testing.T) {
	script, err := os.ReadFile("windows-scm-security-gate.ps1")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, expected := range []string{
		`service", "verify"`,
		`Assert-ServiceDefinition "ScriptBoard" "NT AUTHORITY\LocalService" "Auto"`,
		`Assert-ServiceDefinition "ScriptBoardBroker" "LocalSystem" "Auto"`,
		`admin_password_file:`,
		`Wait-ServiceState "ScriptBoardRunner" "Stopped"`,
		`/config/quick-runs/one-time`,
		`Assert-PipeDenied`,
		`Assert-PrivateBrokerPath`,
		`Stop-Process -Id $running.ProcessId -Force`,
		`service", "uninstall"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("Windows SCM gate does not contain %q", expected)
		}
	}
	workflow, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "./scripts/windows-scm-security-gate.ps1") {
		t.Fatal("CI does not execute the Windows SCM security gate")
	}
}

func TestFormalReleaseDependsOnSecurityGates(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	content := string(workflow)
	for _, expected := range []string{
		"release-windows:",
		"go test ./... -count=1",
		"go vet ./...",
		"./scripts/windows-scm-security-gate.ps1",
		"release-race:",
		"bash ./scripts/run-race-security-gate.sh",
		"release-fuzz:",
		"bash ./scripts/run-fuzz-security-gate.sh",
		"release-browser:",
		"pnpm test",
		"release-security:",
		"golang/govulncheck-action@v1",
		"gitleaks/gitleaks-action@v2",
		"release-codeql:",
		"github/codeql-action/analyze@v4",
		"needs: [release-windows, release-race, release-fuzz, release-browser, release-security, release-codeql]",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("formal release workflow does not contain %q", expected)
		}
	}
	raceGate, err := os.ReadFile("run-race-security-gate.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"go test -race",
		"./internal/assistant/runtimehost",
		"./internal/auditnotification",
		"./internal/privilegebroker",
		"./internal/runnerhost",
		"./internal/securityevents",
		"./internal/statebackup",
		"./internal/update",
	} {
		if !strings.Contains(string(raceGate), expected) {
			t.Fatalf("race security gate does not contain %q", expected)
		}
	}
	ciWorkflow, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ciWorkflow), "bash ./scripts/run-race-security-gate.sh") {
		t.Fatal("CI and formal release do not share the race security gate")
	}
	securityWorkflow, err := os.ReadFile("../.github/workflows/security.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(securityWorkflow), "bash ./scripts/run-fuzz-security-gate.sh") {
		t.Fatal("security workflow does not execute the fuzz security gate")
	}
}
