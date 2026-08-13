package app_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/app"
)

func TestPrivilegedAccountPastEnrollmentDeadlineIsConfinedToMFASetup(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	application, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	passwordBytes, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatal(err)
	}
	login(t, client, server.URL, strings.TrimSpace(string(passwordBytes)), http.StatusSeeOther)

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`UPDATE users SET mfa_required_at = ? WHERE id = 'administrator'`, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}

	blocked, err := client.Get(server.URL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	_ = blocked.Body.Close()
	if blocked.StatusCode != http.StatusSeeOther || blocked.Header.Get("Location") != "/settings/account/mfa" {
		t.Fatalf("monitor status=%d location=%q", blocked.StatusCode, blocked.Header.Get("Location"))
	}

	mfaPageResponse, err := client.Get(server.URL + "/settings/account/mfa")
	if err != nil {
		t.Fatal(err)
	}
	mfaPage, _ := io.ReadAll(mfaPageResponse.Body)
	_ = mfaPageResponse.Body.Close()
	if mfaPageResponse.StatusCode != http.StatusOK {
		t.Fatalf("MFA page status=%d body=%s", mfaPageResponse.StatusCode, mfaPage)
	}

	mutation, err := client.PostForm(server.URL+"/config/external-interfaces/control", url.Values{
		"csrf_token": {formToken(t, mfaPage)}, "enabled": {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = mutation.Body.Close()
	if mutation.StatusCode != http.StatusForbidden {
		t.Fatalf("mutation status=%d", mutation.StatusCode)
	}
	var enabled int
	if err := database.QueryRow(`SELECT enabled FROM external_trigger_control WHERE id = 1`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("blocked mutation changed state: enabled=%d err=%v", enabled, err)
	}
	var blockedAudit int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'mfa_enrollment_required' AND result = 'blocked'`).Scan(&blockedAudit); err != nil || blockedAudit < 2 {
		t.Fatalf("blocked audit count=%d err=%v", blockedAudit, err)
	}
}
