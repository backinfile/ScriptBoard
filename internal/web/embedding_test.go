package web_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	app "scriptboard/internal/web"
	"strings"
	"testing"
)

func TestEmbeddingLoginAndNestedCustomTab(t *testing.T) {
	for _, tc := range []struct {
		name            string
		enabled, secure bool
	}{
		{"default-http", false, false}, {"default-https", false, true}, {"embedded-http", true, false}, {"embedded-https", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			passwordFile := filepath.Join(root, "password")
			const password = "Embedding integration password 2026!"
			if err := os.WriteFile(passwordFile, []byte(password), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := app.Config{StateRoot: filepath.Join(root, "state"), AdminPasswordFile: passwordFile}
			if tc.enabled {
				cfg.FrameAncestors = []string{"https://parent.example", "http://localhost:9000"}
			}
			application, err := app.Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.Close() })
			var server *httptest.Server
			if tc.secure {
				server = httptest.NewTLSServer(application.Handler())
			} else {
				server = httptest.NewServer(application.Handler())
			}
			t.Cleanup(server.Close)
			client := server.Client()
			client.Jar, _ = cookiejar.New(nil)
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			checkPolicy := func(response *http.Response) {
				t.Helper()
				want := "frame-ancestors 'none'"
				xframe := "DENY"
				if tc.enabled {
					want = "frame-ancestors https://parent.example http://localhost:9000"
					xframe = ""
				}
				if !strings.Contains(response.Header.Get("Content-Security-Policy"), want) || response.Header.Get("X-Frame-Options") != xframe {
					t.Fatalf("invalid frame policy: %v", response.Header)
				}
			}
			response, err := client.Get(server.URL + "/login")
			if err != nil {
				t.Fatal(err)
			}
			page, _ := io.ReadAll(response.Body)
			response.Body.Close()
			checkPolicy(response)
			if response.StatusCode != 200 {
				t.Fatalf("login GET: %d", response.StatusCode)
			}
			cookies := response.Cookies()
			if len(cookies) == 0 {
				t.Fatal("missing login cookie")
			}
			wantSameSite := http.SameSiteStrictMode
			if tc.enabled && tc.secure {
				wantSameSite = http.SameSiteNoneMode
			}
			for _, cookie := range cookies {
				if cookie.SameSite != wantSameSite || cookie.Secure != tc.secure || !cookie.HttpOnly {
					t.Fatalf("invalid login cookie: %v", cookie)
				}
			}
			response, err = client.PostForm(server.URL+"/login", url.Values{"username": {"admin"}, "password": {password}, "csrf_token": {formToken(t, page)}})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("login POST: %d", response.StatusCode)
			}
			found := false
			for _, cookie := range response.Cookies() {
				if cookie.Name == "scriptboard_session" {
					found = true
					want := http.SameSiteLaxMode
					if tc.enabled && tc.secure {
						want = http.SameSiteNoneMode
					}
					if cookie.SameSite != want || cookie.Secure != tc.secure {
						t.Fatalf("invalid session cookie: %v", cookie)
					}
				}
			}
			if !found {
				t.Fatal("missing session")
			}
			response, err = client.Get(server.URL + "/config/custom-tabs")
			if err != nil {
				t.Fatal(err)
			}
			page, _ = io.ReadAll(response.Body)
			response.Body.Close()
			checkPolicy(response)
			if response.StatusCode != 200 {
				t.Fatalf("authenticated GET: %d", response.StatusCode)
			}
			values := url.Values{"csrf_token": {formToken(t, page)}, "name": {"Nested ScriptBoard"}, "target_url": {"https://child.example"}, "credential_mode": {"target_state"}, "visible_role": {"administrator"}, "enabled": {"true"}}
			// Trusted embedding parents still cannot submit cross-origin administrative changes.
			request, _ := http.NewRequest("POST", server.URL+"/config/custom-tabs", strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "https://parent.example")
			response, err = client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("cross-origin POST: %d", response.StatusCode)
			}
			response, err = client.PostForm(server.URL+"/config/custom-tabs", values)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("create tab: %d", response.StatusCode)
			}
			response, _ = client.Get(server.URL + "/config/custom-tabs")
			page, _ = io.ReadAll(response.Body)
			response.Body.Close()
			match := regexp.MustCompile("/config/custom-tabs/([^/\"]+)/toggle").FindSubmatch(page)
			if len(match) != 2 {
				t.Fatal("missing tab")
			}
			response, err = client.PostForm(server.URL+"/config/custom-tabs/"+string(match[1])+"/toggle", url.Values{"csrf_token": {formToken(t, page)}, "enabled": {"true"}})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("enable tab: %d", response.StatusCode)
			}
			response, err = client.Get(server.URL + "/defined/tabs/" + string(match[1]))
			if err != nil {
				t.Fatal(err)
			}
			page, _ = io.ReadAll(response.Body)
			response.Body.Close()
			checkPolicy(response)
			if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-src https://child.example") || !strings.Contains(string(page), "allow-same-origin") {
				t.Fatalf("nested tab failed: %d %s", response.StatusCode, page)
			}
		})
	}
}
