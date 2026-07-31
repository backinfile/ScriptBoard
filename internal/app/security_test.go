package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestPrepareRootsProtectsExistingStateDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix directory permission bits")
	}

	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareRoots(managed, state); err != nil {
		t.Fatalf("prepare roots: %v", err)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state directory mode = %#o, want 0700", got)
	}
}

func TestVerifyPasswordRejectsUnboundedArgonParameters(t *testing.T) {
	encoded := "$argon2id$v=19$m=4294967295,t=4294967295,p=255$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if verifyPassword("password", encoded) {
		t.Fatal("password with unbounded Argon2 parameters was accepted")
	}
}

func TestLoginFailureTrackingHasABoundedCardinality(t *testing.T) {
	application := &App{loginFailures: make(map[string]loginFailure)}
	for index := 0; index < loginRateBucketCount+1000; index++ {
		application.recordLoginFailure(application.loginRateKey("account", "unknown-"+strconv.Itoa(index)))
	}
	if got := len(application.loginFailures); got == 0 || got > loginRateBucketCount {
		t.Fatalf("login failure entries = %d, want 1..%d", got, loginRateBucketCount)
	}
}

func TestSpreadsheetSafeCSVCell(t *testing.T) {
	tests := map[string]string{
		"plain text":              "plain text",
		"contains-hyphen":         "contains-hyphen",
		"=SUM(A1:A2)":             "'=SUM(A1:A2)",
		"+cmd|' /C calc'!A0":      "'+cmd|' /C calc'!A0",
		"-1+2":                    "'-1+2",
		"@SUM(A1:A2)":             "'@SUM(A1:A2)",
		"  =SUM(A1:A2)":           "'  =SUM(A1:A2)",
		"\t@SUM(A1:A2)":           "'\t@SUM(A1:A2)",
		"\tplain text":            "'\tplain text",
		"\ufeff=SUM(A1:A2)":       "'\ufeff=SUM(A1:A2)",
		"safe leading whitespace": "safe leading whitespace",
	}
	for input, want := range tests {
		if got := spreadsheetSafeCSVCell(input); got != want {
			t.Errorf("spreadsheetSafeCSVCell(%q) = %q, want %q", input, got, want)
		}
	}
}
