package web_test

import (
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalResolveErrorsDoNotRevealWhetherAKeyExists(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	validToken, _ := createExternalTestKey(t, client, serverURL, "Enumeration test")
	invalidToken := "sbk_AAAAAAAAAAAAAAAA." + strings.Repeat("B", 43)

	invoke := func(token string) (int, []byte) {
		request, err := http.NewRequest(http.MethodPost, serverURL+"/trigger?name=missing", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		return response.StatusCode, body
	}
	invalidStatus, invalidBody := invoke(invalidToken)
	missingStatus, missingBody := invoke(validToken)
	if invalidStatus != missingStatus || !bytes.Equal(invalidBody, missingBody) {
		t.Fatalf("invalid key status=%d body=%s; missing entry status=%d body=%s", invalidStatus, invalidBody, missingStatus, missingBody)
	}
}

func TestExternalAuthenticationFailuresAreAuditedAndSourceLimited(t *testing.T) {
	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	invalidToken := "sbk_AAAAAAAAAAAAAAAA." + strings.Repeat("B", 43)

	for attempt := 1; attempt <= 61; attempt++ {
		request, err := http.NewRequest(http.MethodPost, serverURL+"/trigger?name=missing", strings.NewReader("message=ignored"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+invalidToken)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		want := http.StatusUnauthorized
		if attempt == 61 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("attempt %d status=%d want=%d", attempt, response.StatusCode, want)
		}
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var failures, leaked int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='external_trigger_auth' AND result='invalid_key'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='external_trigger_auth' AND target LIKE ?`, "%"+invalidToken+"%").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if failures != 60 || leaked != 0 {
		t.Fatalf("authentication audits=%d leaked-token-audits=%d", failures, leaked)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, "ignored")); err == nil {
		t.Fatal("unauthenticated action changed Host Files")
	}
}
