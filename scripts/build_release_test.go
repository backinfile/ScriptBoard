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
