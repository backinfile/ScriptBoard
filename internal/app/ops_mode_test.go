package app_test

import (
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"testing"
)

func TestOperationsModeHasNoAIWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	for _, path := range []string{"/ai", "/ai/conversations/example", "/settings/ai"} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want %d", path, response.StatusCode, http.StatusNotFound)
		}
	}

	for _, path := range []string{"/overview", "/assets/app.css", "/assets/app-v2.js"} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		for _, forbidden := range [][]byte{
			[]byte(`href="/ai"`),
			[]byte(`data-ai-`),
			[]byte(`AI 工作台`),
			[]byte(`settings/ai`),
		} {
			if bytes.Contains(body, forbidden) {
				t.Fatalf("%s still exposes removed AI surface %q", path, forbidden)
			}
		}
	}
}
