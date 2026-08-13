package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestPrepareStateRootProtectsExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix directory permission bits")
	}

	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareStateRoot(state); err != nil {
		t.Fatalf("prepare State Root: %v", err)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state directory mode = %#o, want 0700", got)
	}
}

func TestPrepareStateRootRejectsFilesystemRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if runtime.GOOS != "windows" {
		root = string(filepath.Separator)
	}
	if _, err := prepareStateRoot(root); err == nil {
		t.Fatal("filesystem root was accepted as State Root")
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

func TestPageResponseWriterRedactsEveryErrorContentType(t *testing.T) {
	for _, contentType := range []string{"text/plain; charset=utf-8", "text/html; charset=utf-8", "application/json; charset=utf-8"} {
		t.Run(contentType, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writer := &pageResponseWriter{ResponseWriter: recorder}
			writer.Header().Set("Content-Type", contentType)
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"password=web-error-secret"}`))
			writer.finish(&App{}, httptest.NewRequest(http.MethodGet, "/public/test", nil))
			if body := recorder.Body.String(); strings.Contains(body, "web-error-secret") || !strings.Contains(body, "[REDACTED]") {
				t.Fatalf("error response was not redacted: %q", body)
			}
		})
	}
}
