package app_test

import (
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

func formToken(t *testing.T, body []byte) string {
	t.Helper()
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("csrf token not found in response: %q", body)
	}
	return string(match[1])
}
