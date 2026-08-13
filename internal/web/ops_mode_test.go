package web_test

import (
	"io"
	"net/http"
	"path/filepath"
	"testing"
)

func TestOperationsModeIncludesAuthenticatedAIWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	for _, path := range []string{"/ai", "/settings/ai"} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, response.StatusCode, http.StatusOK)
		}
	}

	response, err := client.Get(serverURL + "/ai/conversations/example")
	if err != nil {
		t.Fatalf("GET missing conversation: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing conversation status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}
