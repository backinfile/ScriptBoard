package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"scriptboard/internal/app"
)

func TestFirstStartCreatesCredentialAndProtectsFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")

	application, err := app.Open(app.Config{
		ManagedRoot: managedRoot,
		StateRoot:   stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	passwordPath := filepath.Join(stateRoot, "secrets", "initial-admin-password")
	password, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}
	if len(strings.TrimSpace(string(password))) < 20 {
		t.Fatalf("initial password is unexpectedly short: %d bytes", len(password))
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	loginBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(loginBody), "登录") {
		t.Fatalf("unexpected login response: status=%d body=%q", response.StatusCode, loginBody)
	}
	if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("login cache control = %q, want no-store", cacheControl)
	}
	if !strings.Contains(string(loginBody), `name="username" autofocus`) {
		t.Fatalf("login username is not initially focused: %s", loginBody)
	}
	if !strings.Contains(string(loginBody), `<body class="login-page">`) {
		t.Fatalf("login page styling depends on JavaScript: %s", loginBody)
	}

	response, err = client.Get(server.URL + "/files/")
	if err != nil {
		t.Fatalf("get protected files page: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("protected page status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if location := response.Header.Get("Location"); location != "/login" {
		t.Fatalf("protected page redirect = %q, want /login", location)
	}
}

func TestRootRedirectsToLoginWhenUnauthenticated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	response, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("root status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if location := response.Header.Get("Location"); location != "/login" {
		t.Fatalf("root redirect = %q, want /login", location)
	}
}

func TestLoginPageExposesAJAXEnhancementHooks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	for _, expected := range []string{
		`data-login-form`,
		`data-login-error`,
		`data-login-error-message`,
		`aria-live="polite"`,
	} {
		if !bytes.Contains(page, []byte(expected)) {
			t.Fatalf("login page does not contain %q: %s", expected, page)
		}
	}
}

func TestInvalidLoginRendersInlineErrorPage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}

	response, err = client.PostForm(server.URL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {"wrong-password"},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	defer response.Body.Close()
	body, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read invalid login response: %v", err)
	}

	page := string(body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("invalid login content type = %q, want HTML", contentType)
	}
	for _, expected := range []string{"<!doctype html>", "用户名或密码错误", `role="alert"`, `value="admin"`, `action="/login"`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("invalid login page does not contain %q: %s", expected, page)
		}
	}
}

func TestInvalidAJAXLoginReturnsStructuredErrorAndFreshCSRFToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}

	form := url.Values{
		"username":   {"admin"},
		"password":   {"wrong-password"},
		"csrf_token": {formToken(t, page)},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create AJAX login request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post AJAX login: %v", err)
	}
	defer response.Body.Close()

	var payload struct {
		Error     string `json:"error"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode AJAX login response: %v", err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("AJAX login status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("AJAX login content type = %q, want JSON", contentType)
	}
	if payload.Error != "用户名或密码错误" {
		t.Fatalf("AJAX login error = %q", payload.Error)
	}
	if payload.CSRFToken == "" || payload.CSRFToken == form.Get("csrf_token") {
		t.Fatalf("AJAX login did not return a fresh CSRF token")
	}
}

func TestAJAXLoginReturnsServerSelectedRedirect(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	password, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}

	form := url.Values{
		"username":   {"admin"},
		"password":   {strings.TrimSpace(string(password))},
		"csrf_token": {formToken(t, page)},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create AJAX login request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post AJAX login: %v", err)
	}
	defer response.Body.Close()

	var payload struct {
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode AJAX login response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("AJAX login status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if payload.Redirect != "/settings/account" {
		t.Fatalf("AJAX login redirect = %q, want /settings/account", payload.Redirect)
	}

	response, err = client.Get(server.URL + payload.Redirect)
	if err != nil {
		t.Fatalf("follow AJAX login redirect: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated redirect status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestLoginRateLimitCannotBeBypassedByChangingUsername(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	for attempt := 1; attempt <= 6; attempt++ {
		response := invalidLoginAttempt(t, client, server.URL, "unknown-"+strconv.Itoa(attempt), "")
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, want)
		}
		if attempt == 6 && response.Header.Get("Retry-After") != "2" {
			t.Fatalf("first retry delay = %q, want 2 seconds", response.Header.Get("Retry-After"))
		}
		_ = response.Body.Close()
	}
}

func TestLoginRateLimitCannotBeBypassedByChangingSourceAddress(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot:    filepath.Join(root, "managed"),
		StateRoot:      filepath.Join(root, "state"),
		TrustedProxies: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Jar: jar}

	for attempt := 1; attempt <= 6; attempt++ {
		response := invalidLoginAttempt(t, client, server.URL, "admin", "203.0.113."+strconv.Itoa(attempt))
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, want)
		}
		_ = response.Body.Close()
	}
}

func TestRateLimitedLoginIsRecordedInAudit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	authenticated, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	attacker := &http.Client{Jar: jar}
	for attempt := 1; attempt <= 6; attempt++ {
		response := invalidLoginAttempt(t, attacker, serverURL, "unknown-"+strconv.Itoa(attempt), "")
		_ = response.Body.Close()
	}

	response, err := authenticated.Get(serverURL + "/audit")
	if err != nil {
		t.Fatalf("get audit page: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read audit page: %v", err)
	}
	if !strings.Contains(string(body), "rate_limited") {
		t.Fatalf("audit page does not contain rate-limited login: %s", body)
	}
}

func TestProtectedErrorsRenderInsideTheApplicationShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/account")
	if err != nil {
		t.Fatalf("get account settings: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account settings: %v", err)
	}

	response, err = client.PostForm(serverURL+"/settings/account", url.Values{
		"username":         {"admin"},
		"current_password": {"wrong-password"},
		"new_password":     {"这是另一个足够长的安全密码"},
		"confirm_password": {"这是另一个足够长的安全密码"},
		"csrf_token":       {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post invalid account change: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account error: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("account error status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("account error content type = %q, want HTML", contentType)
	}
	for _, expected := range []string{
		`class="app-header"`, `aria-label="主导航"`, `action="/logout"`,
		`role="alert"`, "当前密码错误", "返回账户设置", "本机 · 0 个运行",
		`<a href="/settings/account">admin</a>`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("account error page does not contain %q: %s", expected, page)
		}
	}
	if strings.Contains(page, "LOCAL / READY") {
		t.Fatalf("account error page contains the former placeholder status: %s", page)
	}
}

func TestApplicationShellMarksTrustedProxyRequestsAsRemote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot:    filepath.Join(root, "managed"),
		StateRoot:      filepath.Join(root, "state"),
		TrustedProxies: []string{"127.0.0.1"},
	})
	request, err := http.NewRequest(http.MethodGet, serverURL+"/files/", nil)
	if err != nil {
		t.Fatalf("create files request: %v", err)
	}
	request.Header.Set("X-Forwarded-For", "203.0.113.42")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("get files through trusted proxy: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files page: %v", err)
	}
	if !strings.Contains(string(body), "远程 · 0 个运行") {
		t.Fatalf("files page does not identify remote management: %s", body)
	}
}

func TestSecondInstanceUsingSameStateRootIsRejected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	config := app.Config{ManagedRoot: filepath.Join(root, "managed"), StateRoot: filepath.Join(root, "state")}
	first, err := app.Open(config)
	if err != nil {
		t.Fatalf("open first instance: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := app.Open(config)
	if err == nil {
		_ = second.Close()
		t.Fatal("second instance unexpectedly opened the same State Root")
	}
	if !strings.Contains(err.Error(), "另一个 ScriptBoard 实例") {
		t.Fatalf("second instance error = %q", err)
	}
}

func TestInitialPasswordLoginRequiresPasswordChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	passwordBytes, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	csrf := formToken(t, body)

	response, err = client.PostForm(server.URL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {strings.TrimSpace(string(passwordBytes))},
		"csrf_token": {csrf},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if location := response.Header.Get("Location"); location != "/settings/account" {
		t.Fatalf("login redirect = %q, want /settings/account", location)
	}

	response, err = client.Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("get login while authenticated: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/settings/account" {
		t.Fatalf("authenticated login page response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(server.URL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/settings/account" {
		t.Fatalf("files while password change required: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestFirstPasswordChangeRevokesSessionAndRemovesCredentialFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   stateRoot,
	})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	passwordPath := filepath.Join(stateRoot, "secrets", "initial-admin-password")
	passwordBytes, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}
	initialPassword := strings.TrimSpace(string(passwordBytes))
	const newPassword = "这是一个新的安全密码短语"

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	login(t, client, server.URL, initialPassword, http.StatusSeeOther)

	response, err := client.Get(server.URL + "/settings/account")
	if err != nil {
		t.Fatalf("get account settings: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account settings: %v", err)
	}
	csrf := formToken(t, body)

	response, err = client.PostForm(server.URL+"/settings/account", url.Values{
		"current_password": {initialPassword},
		"new_password":     {newPassword},
		"confirm_password": {newPassword},
		"csrf_token":       {csrf},
	})
	if err != nil {
		t.Fatalf("post password change: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("password change response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, err := os.Stat(passwordPath); !os.IsNotExist(err) {
		t.Fatalf("initial password file still exists: %v", err)
	}

	response, err = client.Get(server.URL + "/files/")
	if err != nil {
		t.Fatalf("get files with revoked session: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("revoked session response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	login(t, client, server.URL, initialPassword, http.StatusUnauthorized)
	response = login(t, client, server.URL, newPassword, http.StatusSeeOther)
	if response.Header.Get("Location") != "/files/" {
		t.Fatalf("new password login redirect = %q, want /files/", response.Header.Get("Location"))
	}
}

func login(t *testing.T, client *http.Client, serverURL, password string, wantStatus int) *http.Response {
	t.Helper()
	response, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	response, err = client.PostForm(serverURL+"/login", url.Values{
		"username":   {"admin"},
		"password":   {password},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("login status = %d, want %d", response.StatusCode, wantStatus)
	}
	return response
}

func authenticatedClient(t *testing.T, managedRoot, stateRoot string) (*http.Client, string) {
	t.Helper()
	return authenticatedClientWithConfig(t, app.Config{ManagedRoot: managedRoot, StateRoot: stateRoot})
}

func authenticatedClientWithConfig(t *testing.T, config app.Config) (*http.Client, string) {
	t.Helper()
	application, err := app.Open(config)
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	passwordBytes, err := os.ReadFile(filepath.Join(config.StateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatalf("read initial password: %v", err)
	}
	initialPassword := strings.TrimSpace(string(passwordBytes))
	login(t, client, server.URL, initialPassword, http.StatusSeeOther)

	response, err := client.Get(server.URL + "/settings/account")
	if err != nil {
		t.Fatalf("get account settings: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read account settings: %v", err)
	}
	const password = "用于自动测试的专用安全密码"
	response, err = client.PostForm(server.URL+"/settings/account", url.Values{
		"current_password": {initialPassword},
		"new_password":     {password},
		"confirm_password": {password},
		"csrf_token":       {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("change password status = %d", response.StatusCode)
	}
	login(t, client, server.URL, password, http.StatusSeeOther)
	return client, server.URL
}

func formToken(t *testing.T, body []byte) string {
	t.Helper()
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("csrf token not found in response: %q", body)
	}
	return string(match[1])
}

func invalidLoginAttempt(t *testing.T, client *http.Client, serverURL, username, forwardedFor string) *http.Response {
	t.Helper()
	response, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	form := url.Values{
		"username":   {username},
		"password":   {"wrong-password"},
		"csrf_token": {formToken(t, body)},
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create login request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	return response
}

func hiddenValue(t *testing.T, body []byte, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]+)"`)
	match := pattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("hidden %s not found in response: %q", name, body)
	}
	return string(match[1])
}
