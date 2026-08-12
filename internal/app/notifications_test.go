package app_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/app"
)

func TestNotificationsPageIsReadOnlyAndStatesImplementedCoverage(t *testing.T) {
	t.Parallel()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	page := getSecurityPage(t, client, serverURL+"/settings/notifications")
	for _, expected := range [][]byte{
		[]byte("Notifications and alerts"),
		[]byte("Every committed audit event"),
		[]byte("No alert records yet"),
		[]byte("Email delivery"),
		[]byte("tokens are never shown"),
	} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("notifications page missing %q: %s", expected, page)
		}
	}
	pageBody := string(page)
	pageStart := strings.Index(pageBody, `data-notifications-page`)
	if pageStart < 0 || strings.Contains(pageBody[pageStart:], "<form") {
		t.Fatalf("read-only notifications page unexpectedly contains a form: %s", page)
	}
}
