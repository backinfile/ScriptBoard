package web_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "scriptboard/internal/web"
)

func TestExpiredRecentAuthenticationOffersInlinePasswordChallenge(t *testing.T) {
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
	passwordBytes, _ := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	login(t, client, server.URL, strings.TrimSpace(string(passwordBytes)), http.StatusSeeOther)

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`UPDATE sessions SET reauthenticated_at = 0`); err != nil {
		t.Fatal(err)
	}

	page := getBody(t, client, server.URL+"/settings/name", http.StatusOK)
	csrfToken := formToken(t, page)
	form := url.Values{"csrf_token": {csrfToken}, "display_name": {"Inline challenge"}}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/settings/name", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-ScriptBoard-Step-Up", "dialog")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("inline challenge status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if leaked := response.Header.Get("X-ScriptBoard-Internal-Response-Body-Policy"); leaked != "" {
		t.Fatalf("inline challenge leaked internal response policy %q", leaked)
	}
	var challenge struct {
		Method    string `json:"method"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&challenge); err != nil {
		t.Fatal(err)
	}
	if challenge.Method != "password" {
		t.Fatalf("inline challenge method=%q", challenge.Method)
	}
	if challenge.CSRFToken != csrfToken {
		t.Fatalf("inline challenge csrf_token=%q, want current session token", challenge.CSRFToken)
	}
	var displayName string
	if err := database.QueryRow(`SELECT display_name FROM instance_settings WHERE singleton = 1`).Scan(&displayName); err != sql.ErrNoRows {
		t.Fatalf("challenge executed protected action: name=%q err=%v", displayName, err)
	}
}

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

	pageResponse, err := client.Get(server.URL + "/settings/name")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	blocked, err := client.PostForm(server.URL+"/settings/name", url.Values{
		"csrf_token": {formToken(t, page)}, "display_name": {"Changed after step-up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = blocked.Body.Close()
	if blocked.StatusCode != http.StatusSeeOther || blocked.Header.Get("Location") != "/auth/step-up?return_to=%2Fsettings%2Fname" {
		t.Fatalf("blocked response status=%d location=%q", blocked.StatusCode, blocked.Header.Get("Location"))
	}
	var displayName string
	if err := database.QueryRow(`SELECT display_name FROM instance_settings WHERE singleton = 1`).Scan(&displayName); err != sql.ErrNoRows {
		t.Fatalf("blocked operation changed site name: name=%q err=%v", displayName, err)
	}

	stepResponse, err := client.Get(server.URL + blocked.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	stepPage, _ := io.ReadAll(stepResponse.Body)
	_ = stepResponse.Body.Close()
	for _, expected := range []string{`data-task-kind="step-up"`, `name="current_password"`, `name="return_to" value="/settings/name"`} {
		if !strings.Contains(string(stepPage), expected) {
			t.Fatalf("step-up page missing %q: %s", expected, stepPage)
		}
	}

	wrong, err := client.PostForm(server.URL+"/auth/step-up", url.Values{
		"csrf_token": {formToken(t, stepPage)}, "current_password": {"wrong-password"}, "return_to": {"/settings/name"},
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
		"csrf_token": {formToken(t, stepPage)}, "current_password": {password}, "return_to": {"/settings/name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = succeeded.Body.Close()
	if succeeded.StatusCode != http.StatusSeeOther || succeeded.Header.Get("Location") != "/settings/name" {
		t.Fatalf("successful step-up status=%d location=%q", succeeded.StatusCode, succeeded.Header.Get("Location"))
	}
	if err := database.QueryRow(`SELECT reauthenticated_at FROM sessions`).Scan(&recent); err != nil || recent <= 0 {
		t.Fatalf("successful step-up timestamp=%d err=%v", recent, err)
	}
	var assurance string
	if err := database.QueryRow(`SELECT authentication_assurance FROM audit_events
		WHERE action='step_up_authentication' AND result='succeeded'`).Scan(&assurance); err != nil || assurance != "aal1+step-up" {
		t.Fatalf("successful step-up assurance=%q err=%v", assurance, err)
	}

	pageResponse, err = client.Get(server.URL + "/settings/name")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	applied, err := client.PostForm(server.URL+"/settings/name", url.Values{
		"csrf_token": {formToken(t, page)}, "display_name": {"Changed after step-up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = applied.Body.Close()
	if applied.StatusCode != http.StatusSeeOther {
		t.Fatalf("step-up protected operation status=%d", applied.StatusCode)
	}
	if err := database.QueryRow(`SELECT display_name FROM instance_settings WHERE singleton = 1`).Scan(&displayName); err != nil || displayName != "Changed after step-up" {
		t.Fatalf("step-up operation not applied: name=%q err=%v", displayName, err)
	}
}
