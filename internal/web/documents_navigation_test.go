package web_test

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDocumentEditorRetainsOrigin(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	if err := os.MkdirAll(hostRoot, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hostRoot, "note.md")
	if err := os.WriteFile(path, []byte("# Before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	client, base := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	token := documentsCSRFToken(t, client, base)
	response := addDocument(t, client, base, token, path)
	response.Body.Close()
	origin := "/resources/documents?q=note"
	response, err := client.Get(base + origin)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "class=\"file-created\"") {
		t.Fatal("document creation column missing")
	}
	match := regexp.MustCompile(`href="(/resources/files/edit\?[^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("missing document editor link")
	}
	editURL := html.UnescapeString(string(match[1]))
	parsed, _ := url.Parse(editURL)
	if parsed.Query().Get("return_to") != origin {
		t.Fatalf("editor origin = %q, want %q", parsed.Query().Get("return_to"), origin)
	}
	response, err = client.Get(base + editURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), `href="`+html.EscapeString(origin)+`"`) {
		t.Fatal("editor lacks document return link")
	}
	digest := regexp.MustCompile(`name="digest" value="([^"]+)"`).FindSubmatch(body)
	if len(digest) != 2 {
		t.Fatal("missing digest")
	}
	response, err = client.PostForm(base+editURL, url.Values{"csrf_token": {formToken(t, body)}, "digest": {string(digest[1])}, "content": {"# After\n"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != origin {
		t.Fatalf("save status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	for _, bad := range []string{"https://example.org", "//example.org", "/resources/documents/other", "/settings/users"} {
		response, err = client.Get(base + "/resources/files/edit?" + url.Values{"path": {path}, "return_to": {bad}}.Encode())
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		if strings.Contains(string(body), `class="task-back" href="`+html.EscapeString(bad)+`"`) {
			t.Fatalf("accepted unsupported origin %q", bad)
		}
	}
}
