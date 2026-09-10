package web_test

import (
	"net/http"
	"path/filepath"
	"regexp"
	app "scriptboard/internal/web"
	"strings"
	"testing"
)

func TestMemoryEditorIsAvailableWithoutConfigurationFile(t *testing.T) {
	root := t.TempDir()
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(root, "state")})
	page := getBody(t, client, base+"/settings/memory", http.StatusOK)
	// Check control attributes so Linux swap guidance remains valid page content.
	if regexp.MustCompile(`<(?:input|button|fieldset)\b[^>]*\sdisabled(?:\s|=|>)`).Match(page) {
		t.Fatal("memory editor must use stored settings without a configuration file")
	}
	for _, control := range []string{`name="runner_memory_limit"`, `name="run_memory_limit"`, `type="submit"`} {
		if !strings.Contains(string(page), control) {
			t.Fatalf("memory editor missing control %s", control)
		}
	}
}
