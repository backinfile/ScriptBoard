package web_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	app "scriptboard/internal/web"
	"testing"
)

func TestStartupCredentialsAreAppliedOnlyDuringStartup(t *testing.T) {
	root := t.TempDir()
	passwordPath := filepath.Join(root, "startup-password")
	const startup = "Startup-settings-test-2026!"
	const changed = "Changed-in-settings-2026!"
	const next = "Next-startup-password-2026!"
	os.WriteFile(passwordPath, []byte(startup), 0600)
	cfg := app.Config{StateRoot: filepath.Join(root, "state"), AdminUsername: "admin", AdminPasswordFile: passwordPath}
	application, err := app.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer func() { server.Close(); application.Close() }()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	login(t, client, server.URL, startup, 303)
	page := getBody(t, client, server.URL+"/settings/account", 200)
	response, err := client.PostForm(server.URL+"/settings/account/password", url.Values{"csrf_token": {formToken(t, page)}, "current_password": {startup}, "new_password": {changed}, "confirm_password": {changed}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("password update=%d", response.StatusCode)
	}
	os.WriteFile(passwordPath, []byte(next), 0600)
	login(t, client, server.URL, changed, 303)
	server.Close()
	application.Close()
	application, err = app.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(application.Handler())
	jar, _ = cookiejar.New(nil)
	client.Jar = jar
	login(t, client, server.URL, next, 303)
}
