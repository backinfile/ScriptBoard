package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func FuzzValidRequestTarget(f *testing.F) {
	for _, seed := range []string{
		"/login",
		"/login?return_to=%2Fmonitor",
		"/assets%2fapp.css",
		"/assets%5capp.css",
		"/assets/../login",
		"/assets//app.css",
		"http://panel.example/login",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, rawTarget string) {
		parsed, err := url.ParseRequestURI(rawTarget)
		if err != nil {
			return
		}
		request := &http.Request{Method: http.MethodGet, URL: parsed, RequestURI: rawTarget}
		if !validRequestTarget(request) {
			return
		}
		if parsed.IsAbs() || !strings.HasPrefix(rawTarget, "/") || strings.Contains(parsed.Path, "//") {
			t.Fatalf("accepted ambiguous request target %q", rawTarget)
		}
		escapedPath := strings.ToLower(parsed.EscapedPath())
		if strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") {
			t.Fatalf("accepted encoded separator in %q", rawTarget)
		}
		for _, segment := range strings.Split(parsed.Path, "/") {
			if segment == "." || segment == ".." {
				t.Fatalf("accepted dot segment in %q", rawTarget)
			}
		}
	})
}
