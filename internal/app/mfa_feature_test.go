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
	"regexp"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/mfa"
)

func TestTOTPEnrollmentRequiresSecondFactorForLoginAndStepUp(t *testing.T) {
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
	password := strings.TrimSpace(string(passwordBytes))
	login(t, client, server.URL, password, http.StatusSeeOther)

	page := getBody(t, client, server.URL+"/settings/account/mfa", http.StatusOK)
	enrollmentResponse, err := client.PostForm(server.URL+"/settings/account/mfa/enroll", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	enrollmentPage, _ := io.ReadAll(enrollmentResponse.Body)
	_ = enrollmentResponse.Body.Close()
	if enrollmentResponse.StatusCode != http.StatusOK {
		t.Fatalf("enrollment status=%d body=%s", enrollmentResponse.StatusCode, enrollmentPage)
	}
	secretMatch := regexp.MustCompile(`data-mfa-secret>([A-Z2-7]+)</code>`).FindSubmatch(enrollmentPage)
	if len(secretMatch) != 2 {
		t.Fatalf("enrollment secret missing: %s", enrollmentPage)
	}
	code, err := mfa.TOTPCode(string(secretMatch[1]), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	confirmResponse, err := client.PostForm(server.URL+"/settings/account/mfa/confirm", url.Values{
		"csrf_token": {formToken(t, enrollmentPage)}, "mfa_code": {code},
	})
	if err != nil {
		t.Fatal(err)
	}
	recoveryPage, _ := io.ReadAll(confirmResponse.Body)
	_ = confirmResponse.Body.Close()
	if confirmResponse.StatusCode != http.StatusOK || !strings.Contains(string(recoveryPage), "data-task-kind=\"account-mfa\"") {
		t.Fatalf("confirm status=%d body=%s", confirmResponse.StatusCode, recoveryPage)
	}
	recoveryCodes := regexp.MustCompile(`[A-Z2-7]{5}(?:-[A-Z2-7]{5}){4}-[A-Z2-7]`).FindAllString(string(recoveryPage), -1)
	if len(recoveryCodes) != 10 {
		t.Fatalf("recovery codes=%d body=%s", len(recoveryCodes), recoveryPage)
	}

	loginPage := getBody(t, client, server.URL+"/login", http.StatusOK)
	for _, forbidden := range []string{`name="mfa_code"`, `data-passkey-login`, `name="passkey_response"`} {
		if strings.Contains(string(loginPage), forbidden) {
			t.Fatalf("first login step exposes second factor control %q: %s", forbidden, loginPage)
		}
	}
	withoutFactor, err := client.PostForm(server.URL+"/login", url.Values{
		"csrf_token": {formToken(t, loginPage)}, "username": {"admin"}, "password": {password},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = withoutFactor.Body.Close()
	if withoutFactor.StatusCode != http.StatusSeeOther || withoutFactor.Header.Get("Location") != "/login/verify" {
		t.Fatalf("password login status=%d location=%q", withoutFactor.StatusCode, withoutFactor.Header.Get("Location"))
	}

	verificationPage := getBody(t, client, server.URL+"/login/verify", http.StatusOK)
	if !strings.Contains(string(verificationPage), `name="mfa_code"`) || strings.Contains(string(verificationPage), `name="password"`) {
		t.Fatalf("second login step has incorrect fields: %s", verificationPage)
	}
	withRecovery, err := client.PostForm(server.URL+"/login/verify", url.Values{
		"csrf_token": {formToken(t, verificationPage)}, "mfa_code": {recoveryCodes[0]},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = withRecovery.Body.Close()
	if withRecovery.StatusCode != http.StatusSeeOther {
		t.Fatalf("MFA recovery login status=%d", withRecovery.StatusCode)
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var assurance int
	if err := database.QueryRow(`SELECT authentication_assurance FROM sessions`).Scan(&assurance); err != nil || assurance != 2 {
		t.Fatalf("session assurance=%d err=%v", assurance, err)
	}
	if _, err := database.Exec(`UPDATE sessions SET reauthenticated_at = 0`); err != nil {
		t.Fatal(err)
	}
	blocked, err := client.PostForm(server.URL+"/config/external-interfaces/control", url.Values{"csrf_token": {"invalid"}, "enabled": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = blocked.Body.Close()
	if blocked.StatusCode != http.StatusSeeOther || !strings.HasPrefix(blocked.Header.Get("Location"), "/auth/step-up") {
		t.Fatalf("expired step-up status=%d location=%q", blocked.StatusCode, blocked.Header.Get("Location"))
	}
	stepPage := getBody(t, client, server.URL+blocked.Header.Get("Location"), http.StatusOK)
	if !strings.Contains(string(stepPage), `name="mfa_code"`) ||
		!strings.Contains(string(stepPage), `name="verification_mode" value="second-factor"`) ||
		strings.Contains(string(stepPage), `name="current_password"`) {
		t.Fatalf("second-factor-only verification form is invalid: %s", stepPage)
	}
	stepped, err := client.PostForm(server.URL+"/auth/step-up", url.Values{
		"csrf_token": {formToken(t, stepPage)}, "verification_mode": {"second-factor"}, "mfa_code": {recoveryCodes[1]}, "return_to": {"/config/external-interfaces"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = stepped.Body.Close()
	if stepped.StatusCode != http.StatusSeeOther {
		t.Fatalf("MFA step-up status=%d", stepped.StatusCode)
	}
	var auditAssurance string
	if err := database.QueryRow(`SELECT authentication_assurance FROM audit_events WHERE action='step_up_authentication' AND result='succeeded' ORDER BY id DESC LIMIT 1`).Scan(&auditAssurance); err != nil || auditAssurance != "aal2+step-up" {
		t.Fatalf("step-up audit assurance=%q err=%v", auditAssurance, err)
	}

	resetPassword, err := application.ResetAdminCredentials("admin")
	if err != nil {
		t.Fatal(err)
	}
	login(t, client, server.URL, resetPassword, http.StatusSeeOther)
	if err := database.QueryRow(`SELECT authentication_assurance FROM sessions`).Scan(&assurance); err != nil || assurance != 1 {
		t.Fatalf("post-break-glass assurance=%d err=%v", assurance, err)
	}
}

func getBody(t *testing.T, client *http.Client, target string, want int) []byte {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("GET %s status=%d body=%s", target, response.StatusCode, body)
	}
	return body
}
