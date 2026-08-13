package app_test

import (
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

	"scriptboard/internal/app"
)

func TestPasskeyCeremoniesRequireCSRFAndUserVerification(t *testing.T) {
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
	mfaPage := getBody(t, client, server.URL+"/settings/account/mfa", http.StatusOK)

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/settings/account/passkeys/register/options", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	denied, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", denied.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/settings/account/passkeys/register/options", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", formToken(t, mfaPage))
	started, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(started.Body)
	_ = started.Body.Close()
	if started.StatusCode != http.StatusOK {
		t.Fatalf("registration options status=%d body=%s", started.StatusCode, body)
	}
	var payload struct {
		CeremonyID string `json:"ceremony_id"`
		Options    struct {
			PublicKey struct {
				RP struct {
					ID string `json:"id"`
				} `json:"rp"`
				AuthenticatorSelection struct {
					ResidentKey      string `json:"residentKey"`
					UserVerification string `json:"userVerification"`
				} `json:"authenticatorSelection"`
			} `json:"publicKey"`
		} `json:"options"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CeremonyID == "" || payload.Options.PublicKey.RP.ID != "127.0.0.1" ||
		payload.Options.PublicKey.AuthenticatorSelection.UserVerification != "required" ||
		payload.Options.PublicKey.AuthenticatorSelection.ResidentKey != "preferred" {
		t.Fatalf("unsafe registration options: %s", body)
	}

	// A fresh unauthenticated client receives a syntactically identical challenge
	// even for an unknown user, avoiding an explicit account/passkey oracle.
	unknownJar, _ := cookiejar.New(nil)
	unknown := &http.Client{Jar: unknownJar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getBody(t, unknown, server.URL+"/login", http.StatusOK)
	challenge, err := unknown.PostForm(server.URL+"/auth/passkey/options", url.Values{"username": {"missing-user"}, "csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	challengeBody, _ := io.ReadAll(challenge.Body)
	_ = challenge.Body.Close()
	if challenge.StatusCode != http.StatusOK || !strings.Contains(string(challengeBody), `"userVerification":"required"`) {
		t.Fatalf("unknown challenge status=%d body=%s", challenge.StatusCode, challengeBody)
	}
}
