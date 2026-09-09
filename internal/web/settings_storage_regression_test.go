package web_test

import (
	"net/http"
	"path/filepath"
	app "scriptboard/internal/web"
	"strings"
	"testing"
)

func TestMemoryEditorIsAvailableWithoutConfigurationFile(t *testing.T) {
	root := t.TempDir()
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(root, "state")})
	page := getBody(t, client, base+"/settings/memory", http.StatusOK)
	if strings.Contains(string(page), "disabled") {
		t.Fatal("memory editor must use stored settings without a configuration file")
	}
}
