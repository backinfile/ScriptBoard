package web_test

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type jumpResponse struct {
	URL     string `json:"url"`
	Error   string
	Partial bool
	Entries []struct{ Name, Path, Kind, URL string }
}

func TestFileJumpResolvesPathsAndLocatesFilesAcrossPages(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	host := filepath.Join(root, "host")
	state := filepath.Join(root, "state")
	client, base := authenticatedClient(t, host, state)
	for i := 0; i < 45; i++ {
		if err := os.WriteFile(filepath.Join(host, fmt.Sprintf("entry-%02d.txt", i)), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(host, "中文 & folder")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(host, ".hidden.txt")
	if err := os.WriteFile(hidden, []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	get := func(values url.Values) (jumpResponse, int) {
		t.Helper()
		values.Set("path", host)
		values.Set("format", "json")
		r, e := client.Get(base + "/resources/files/jump?" + values.Encode())
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		var body jumpResponse
		if e = json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		return body, r.StatusCode
	}
	for _, target := range []string{sub, "./中文 & folder", ".", "../host"} {
		result, status := get(url.Values{"target": {target}})
		if status != 200 || result.URL == "" {
			t.Fatalf("target %q: %d %+v", target, status, result)
		}
	}
	for _, target := range []string{"", filepath.Join(host, "missing"), state, "https://example.com"} {
		result, status := get(url.Values{"target": {target}})
		if status == 200 || result.Error == "" || result.URL != "" {
			t.Fatalf("invalid %q: %d %+v", target, status, result)
		}
	}
	follow := *client
	follow.CheckRedirect = nil
	for _, target := range []string{filepath.Join(host, "entry-44.txt"), hidden} {
		result, status := get(url.Values{"target": {target}, "sort": {"name"}, "direction": {"desc"}})
		if status != 200 {
			t.Fatal(result)
		}
		r, e := follow.Get(base + result.URL)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		needle := `data-file-path="` + html.EscapeString(target) + `"`
		if !strings.Contains(string(body), needle) || !strings.Contains(string(body), `data-file-focus`) {
			t.Fatalf("target not visible: %s", body)
		}
		if strings.Contains(result.URL, "q=") {
			t.Fatal("jump retained a search")
		}
	}
	// With ascending order the final file must be on page three, even without a page parameter.
	result, _ := get(url.Values{"target": {filepath.Join(host, "entry-44.txt")}, "sort": {"name"}})
	r, _ := follow.Get(base + result.URL)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(body), "3 / 3") {
		t.Fatalf("wrong focused page: %s", body)
	}
	// GET form fallback redirects to the same destination without requiring JavaScript.
	r, e := client.Get(base + "/resources/files/jump?" + url.Values{"path": {host}, "target": {sub}}.Encode())
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("fallback status %d", r.StatusCode)
	}
}

func TestFileJumpSearchScopeHiddenLimitAndFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	host := filepath.Join(root, "host")
	client, base := authenticatedClient(t, host, filepath.Join(root, "state"))
	for _, dir := range []string{"child", ".private"} {
		if err := os.Mkdir(filepath.Join(host, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"match.txt", "child/match-child.txt", ".private/match-secret.txt", ".match-hidden.txt"} {
		if err := os.WriteFile(filepath.Join(host, filepath.FromSlash(name)), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	search := func(scope, hidden, q string) jumpResponse {
		t.Helper()
		r, e := client.Get(base + "/resources/files/jump?" + url.Values{"path": {host}, "mode": {"search"}, "format": {"json"}, "q": {q}, "scope": {scope}, "show_hidden": {hidden}}.Encode())
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		var v jumpResponse
		if e = json.NewDecoder(r.Body).Decode(&v); e != nil {
			t.Fatal(e)
		}
		if r.StatusCode != 200 {
			t.Fatal(v)
		}
		return v
	}
	for _, tc := range []struct {
		scope, hidden, q string
		want             int
	}{{"current", "", "match", 1}, {"recursive", "", "match", 2}, {"recursive", "1", "match", 4}, {"recursive", "", "", 0}, {"recursive", "", "absent", 0}} {
		v := search(tc.scope, tc.hidden, tc.q)
		if len(v.Entries) != tc.want || v.Partial {
			t.Fatalf("%+v => %+v", tc, v)
		}
	}
	for i := 0; i < 105; i++ {
		if err := os.WriteFile(filepath.Join(host, fmt.Sprintf("limit-%03d.txt", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	all := search("all", "", "limit")
	if len(all.Entries) != 100 || !all.Partial {
		t.Fatalf("unbounded all search: %+v", all)
	}
	v := search("recursive", "", "limit")
	if len(v.Entries) != 100 || !v.Partial {
		t.Fatalf("unbounded result: %+v", v)
	}
	r, e := client.Get(base + "/resources/files/jump?" + url.Values{"path": {host}, "mode": {"search"}, "scope": {"all"}, "q": {"match"}}.Encode())
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(body), "match-child") || !strings.Contains(string(body), "data-jump-result") || !strings.Contains(string(body), `<option value="all" selected>`) {
		t.Fatalf("missing server results: %s", body)
	}
}

func TestFileFocusCanonicalPageAcrossNavigationEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	host := filepath.Join(root, "host")
	client, base := authenticatedClient(t, host, filepath.Join(root, "state"))
	for i := 0; i < 45; i++ {
		if err := os.WriteFile(filepath.Join(host, fmt.Sprintf("entry-%02d.txt", i)), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	hidden := filepath.Join(host, ".hidden.txt")
	if err := os.WriteFile(hidden, []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	follow := *client
	follow.CheckRedirect = nil
	for _, tc := range []struct{ name, target, query, direction, page, wantPage string }{
		{"quick access or document location", filepath.Join(host, "entry-44.txt"), "", "asc", "", "3"},
		{"deferred navigation", filepath.Join(host, "entry-44.txt"), "entry-00", "asc", "1", "3"},
		{"stale page", filepath.Join(host, "entry-44.txt"), "", "asc", "1", "3"},
		{"filtered directory", filepath.Join(host, "entry-44.txt"), "entry-00", "asc", "1", "3"},
		{"descending directory", filepath.Join(host, "entry-00.txt"), "", "desc", "1", "3"},
		{"hidden file location", hidden, "", "asc", "2", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := url.Values{"path": {host}, "focus_path": {tc.target}, "q": {tc.query}, "sort": {"name"}, "direction": {tc.direction}, "page": {tc.page}}
			request, err := http.NewRequest(http.MethodGet, base+"/resources/files?"+values.Encode(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "deferred navigation" {
				request.Header.Set("X-ScriptBoard-Navigation", "pjax")
				request.Header.Set("X-ScriptBoard-Data", "shell")
			}
			response, err := follow.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if got := response.Request.URL.Query().Get("page"); got != tc.wantPage {
				t.Fatalf("page=%q, want %s", got, tc.wantPage)
			}
			if response.Request.URL.Query().Get("q") != "" {
				t.Fatal("focus retained a filter")
			}
			if !strings.Contains(string(body), `data-file-path="`+html.EscapeString(tc.target)+`"`) || !strings.Contains(string(body), "data-file-focus") {
				t.Fatal("target row not visible")
			}
		})
	}
}
