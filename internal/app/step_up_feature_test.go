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

	"scriptboard/internal/app"
)

func TestExpiredRecentAuthenticationRequiresPasswordStepUp(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	application, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	passwordBytes, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(string(passwordBytes))
	login(t, client, server.URL, password, http.StatusSeeOther)

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`UPDATE sessions SET reauthenticated_at = 0`); err != nil {
		t.Fatal(err)
	}

	pageResponse, err := client.Get(server.URL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	blocked, err := client.PostForm(server.URL+"/config/external-interfaces/control", url.Values{
		"csrf_token": {formToken(t, page)}, "enabled": {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = blocked.Body.Close()
	if blocked.StatusCode != http.StatusSeeOther || blocked.Header.Get("Location") != "/auth/step-up?return_to=%2Fconfig%2Fexternal-interfaces" {
		t.Fatalf("blocked response status=%d location=%q", blocked.StatusCode, blocked.Header.Get("Location"))
	}
	var enabled int
	if err := database.QueryRow(`SELECT enabled FROM external_trigger_control WHERE id = 1`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("blocked operation changed control: enabled=%d err=%v", enabled, err)
	}

	stepResponse, err := client.Get(server.URL + blocked.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	stepPage, _ := io.ReadAll(stepResponse.Body)
	_ = stepResponse.Body.Close()
	for _, expected := range []string{`data-task-kind="step-up"`, `name="current_password"`, `name="return_to" value="/config/external-interfaces"`} {
		if !strings.Contains(string(stepPage), expected) {
			t.Fatalf("step-up page missing %q: %s", expected, stepPage)
		}
	}

	wrong, err := client.PostForm(server.URL+"/auth/step-up", url.Values{
		"csrf_token": {formToken(t, stepPage)}, "current_password": {"wrong-password"}, "return_to": {"/config/external-interfaces"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongBody, _ := io.ReadAll(wrong.Body)
	_ = wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d", wrong.StatusCode)
	}
	if !strings.Contains(string(wrongBody), `data-task-kind="step-up"`) || !strings.Contains(string(wrongBody), `role="alert"`) {
		t.Fatalf("wrong password did not render a retryable step-up form: %s", wrongBody)
	}
	var recent, failures int64
	if err := database.QueryRow(`SELECT reauthenticated_at FROM sessions`).Scan(&recent); err != nil || recent != 0 {
		t.Fatalf("failed step-up changed timestamp=%d err=%v", recent, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='step_up_authentication' AND result='failed'`).Scan(&failures); err != nil || failures != 1 {
		t.Fatalf("failed step-up audit count=%d err=%v", failures, err)
	}

	succeeded, err := client.PostForm(server.URL+"/auth/step-up", url.Values{
		"csrf_token": {formToken(t, stepPage)}, "current_password": {password}, "return_to": {"/config/external-interfaces"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = succeeded.Body.Close()
	if succeeded.StatusCode != http.StatusSeeOther || succeeded.Header.Get("Location") != "/config/external-interfaces" {
		t.Fatalf("successful step-up status=%d location=%q", succeeded.StatusCode, succeeded.Header.Get("Location"))
	}
	if err := database.QueryRow(`SELECT reauthenticated_at FROM sessions`).Scan(&recent); err != nil || recent <= 0 {
		t.Fatalf("successful step-up timestamp=%d err=%v", recent, err)
	}

	pageResponse, err = client.Get(server.URL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	applied, err := client.PostForm(server.URL+"/config/external-interfaces/control", url.Values{
		"csrf_token": {formToken(t, page)}, "enabled": {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = applied.Body.Close()
	if applied.StatusCode != http.StatusSeeOther {
		t.Fatalf("step-up protected operation status=%d", applied.StatusCode)
	}
	if err := database.QueryRow(`SELECT enabled FROM external_trigger_control WHERE id = 1`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("step-up operation not applied: enabled=%d err=%v", enabled, err)
	}
}
