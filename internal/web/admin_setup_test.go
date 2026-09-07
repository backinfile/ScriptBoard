package web

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newSetupApp(t *testing.T) *App {
	t.Helper()
	a, err := Open(Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}
func setupRequest(a *App, method, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Host = "127.0.0.1"
	r.Header.Set("Origin", "http://127.0.0.1")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: loginCSRFCookieName, Value: "test-csrf"})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func setupForm(t *testing.T, a *App) url.Values {
	t.Helper()
	token, err := os.ReadFile(filepath.Join(a.stateRoot, "secrets", setupTokenFilename))
	if err != nil {
		t.Fatal(err)
	}
	return url.Values{"token": {strings.TrimSpace(string(token))}, "username": {"owner"}, "password": {"A personal host passphrase 2026!"}, "confirm_password": {"A personal host passphrase 2026!"}, "csrf_token": {"test-csrf"}}
}
func TestAdministratorSetupLifecycle(t *testing.T) {
	a := newSetupApp(t)
	if _, err := os.Stat(filepath.Join(a.stateRoot, "secrets", initialPasswordFilename)); !os.IsNotExist(err) {
		t.Fatal("new installation exposed a login password", err)
	}
	form := setupForm(t, a)
	link, err := a.SetupURL("http://127.0.0.1:8787")
	if err != nil || !strings.HasSuffix(link, "/setup#token="+form.Get("token")) {
		t.Fatal(link, err)
	}
	for _, method := range []string{"GET", "POST"} {
		w := setupRequest(a, method, "/login", form)
		if w.Code != 303 || w.Header().Get("Location") != "/setup" {
			t.Fatal("login before setup", w.Code, w.Body.String())
		}
	}
	w := setupRequest(a, "GET", "/setup", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data-setup-form") || strings.Contains(w.Body.String(), form.Get("token")) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		field, value string
		status       int
	}{{"token", "wrong", 403}, {"csrf_token", "wrong", 403}, {"username", "", 400}, {"confirm_password", "different", 400}, {"password", "short", 400}} {
		original := form.Get(tc.field)
		form.Set(tc.field, tc.value)
		w = setupRequest(a, "POST", "/setup", form)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.field, w.Code, w.Body.String())
		}
		form.Set(tc.field, original)
	}
	w = setupRequest(a, "POST", "/setup", form)
	if w.Code != 303 || w.Header().Get("Location") != "/monitor" {
		t.Fatal(w.Code, w.Body.String())
	}
	var account, password string
	if err := a.db.QueryRow("SELECT username,password_hash FROM users WHERE role='administrator'").Scan(&account, &password); err != nil || account != "owner" || !verifyPassword(form.Get("password"), password) {
		t.Fatal("account not initialized", err)
	}
	if _, err := os.Stat(filepath.Join(a.stateRoot, "secrets", setupTokenFilename)); !os.IsNotExist(err) {
		t.Fatal("setup token not removed", err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/monitor", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	if _, _, ok := a.loadSession(r); !ok {
		t.Fatal("setup did not issue valid session")
	}
	form.Set("password", "A replacement must never apply")
	setupRequest(a, "POST", "/setup", form)
	var again string
	a.db.QueryRow("SELECT password_hash FROM users WHERE role='administrator'").Scan(&again)
	if again != password {
		t.Fatal("replayed setup changed password")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(Config{StateRoot: a.stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if pending, err := reopened.setupPending(); err != nil || pending {
		t.Fatal("restart reopened setup", err)
	}
}
func TestAdministratorSetupExpiryRotationAndConcurrentUse(t *testing.T) {
	a := newSetupApp(t)
	form := setupForm(t, a)
	a.db.Exec("UPDATE administrator_setup SET expires_at=?", time.Now().Add(-time.Second).Unix())
	if w := setupRequest(a, "POST", "/setup", form); w.Code != 403 {
		t.Fatal("expired token accepted", w.Code)
	}
	if err := a.refreshSetupToken(); err != nil {
		t.Fatal(err)
	}
	if w := setupRequest(a, "POST", "/setup", form); w.Code != 403 {
		t.Fatal("rotated token accepted", w.Code)
	}
	form = setupForm(t, a)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := setupRequest(a, "POST", "/setup", form)
			if w.Header().Get("Location") == "/monitor" {
				codes <- 1
			} else {
				codes <- 0
			}
		}()
	}
	wg.Wait()
	close(codes)
	success := 0
	for n := range codes {
		success += n
	}
	if success != 1 {
		t.Fatal("successful setup submissions", success)
	}
}
func TestAdministratorSetupProvisioningAndLegacyUpgrade(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "provisioned"}[override], func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{StateRoot: filepath.Join(root, "state")}
			if override {
				path := filepath.Join(root, "password")
				os.WriteFile(path, []byte("An unattended deployment password"), 0600)
				cfg.AdminPasswordFile = path
			}
			a, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !override {
				if _, err := a.ResetAdminCredentials("existing-owner"); err != nil {
					t.Fatal(err)
				}
				a.db.Exec("DROP TABLE administrator_setup")
				a.db.Exec("PRAGMA user_version=67")
			}
			a.Close()
			a, err = Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if pending, err := a.setupPending(); err != nil || pending {
				t.Fatal("configured or existing account requires setup", err)
			}
			if w := setupRequest(a, "GET", "/login", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "data-login-form") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
func TestAdministratorSetupFailsClosedAndLimitsAttempts(t *testing.T) {
	a := newSetupApp(t)
	form := setupForm(t, a)
	form.Set("token", "wrong")
	limited := false
	for i := 0; i < 50; i++ {
		if w := setupRequest(a, "POST", "/setup", form); w.Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("setup attempts not limited")
	}
	a.db.Exec("DROP TABLE administrator_setup")
	if w := setupRequest(a, "GET", "/login", nil); w.Code != 503 {
		t.Fatal("database failure reopened login", w.Code)
	}
}

func TestAdministratorSetupPasswordPolicyAndSecureCookie(t *testing.T) {
	a := newSetupApp(t)
	form := setupForm(t, a)
	for _, password := range []string{"short", "passwordpassword", strings.Repeat("界", 86)} {
		form.Set("password", password)
		form.Set("confirm_password", password)
		if w := setupRequest(a, "POST", "/setup", form); w.Code != 400 {
			t.Fatalf("password policy status=%d", w.Code)
		}
	}
	form = setupForm(t, a)
	r := httptest.NewRequest("POST", "/setup", strings.NewReader(form.Encode()))
	r.Host = "127.0.0.1"
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://127.0.0.1")
	r.AddCookie(&http.Cookie{Name: loginCSRFCookieName, Value: "test-csrf"})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	found := false
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			found = true
			if !cookie.Secure || !cookie.HttpOnly {
				t.Fatal("unsafe HTTPS session cookie")
			}
		}
	}
	if !found {
		t.Fatal("missing session cookie")
	}
}

func TestAdministratorSetupRollsBackOnAccountWriteFailure(t *testing.T) {
	a := newSetupApp(t)
	form := setupForm(t, a)
	if _, err := a.db.Exec("CREATE TRIGGER fail_setup_update BEFORE UPDATE ON users BEGIN SELECT RAISE(ABORT, 'write rejected'); END"); err != nil {
		t.Fatal(err)
	}
	if w := setupRequest(a, "POST", "/setup", form); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if pending, err := a.setupPending(); err != nil || !pending {
		t.Fatal("failed setup consumed token", err)
	}
	a.db.Exec("DROP TRIGGER fail_setup_update")
	if w := setupRequest(a, "POST", "/setup", form); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAdministratorSetupUsernameOverrideAndRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	cfg := Config{StateRoot: root, AdminUsername: "local-owner"}
	a, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := setupForm(t, a).Get("token")
	w := setupRequest(a, "GET", "/setup", nil)
	if !strings.Contains(w.Body.String(), `value="local-owner"`) {
		t.Fatal("configured username missing")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if pending, err := a.setupPending(); err != nil || !pending {
		t.Fatal("username-only configuration skipped setup", err)
	}
	if after := setupForm(t, a).Get("token"); before == after {
		t.Fatal("restart reused unfinished setup token")
	}
}
