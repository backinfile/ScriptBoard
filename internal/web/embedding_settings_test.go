package web_test

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddingSettingsApplyImmediatelyAndValidate(t *testing.T) {
	root := t.TempDir()
	client, base := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	page := getBody(t, client, base+"/settings/embedding", http.StatusOK)
	for _, text := range []string{`action="/settings/embedding"`, `value="all"`, `href="/settings/embedding" aria-current="page"`} {
		if !strings.Contains(string(page), text) {
			t.Fatalf("missing %s", text)
		}
	}
	token := formToken(t, page)
	for _, tc := range []struct {
		mode, origins, csrf, policy string
		status                      int
	}{
		{"all", "", "", "'none'", 403},
		{"all", "", token, "*", 303},
		{"specific", "https://parent.example\nhttp://localhost:9000", token, "https://parent.example http://localhost:9000", 303},
		{"specific", "*", token, "https://parent.example http://localhost:9000", 422},
		{"specific", "https://parent.example; script-src *", token, "https://parent.example http://localhost:9000", 422},
		{"specific", "", token, "https://parent.example http://localhost:9000", 422},
		{"unknown", "", token, "https://parent.example http://localhost:9000", 422},
		{"deny", "", token, "'none'", 303},
	} {
		response, err := client.PostForm(base+"/settings/embedding", url.Values{"mode": {tc.mode}, "origins": {tc.origins}, "csrf_token": {tc.csrf}})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s: got %d want %d", tc.mode, response.StatusCode, tc.status)
		}
		response, err = client.Get(base + "/login")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors "+tc.policy+";") {
			t.Fatalf("policy: %v", response.Header)
		}
		if (response.Header.Get("X-Frame-Options") == "DENY") != (tc.policy == "'none'") {
			t.Fatalf("XFO: %v", response.Header)
		}
	}
	request, _ := http.NewRequest("POST", base+"/settings/embedding", strings.NewReader(url.Values{"mode": {"all"}, "csrf_token": {token}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://foreign.example")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("cross-origin: %d", response.StatusCode)
	}
}
