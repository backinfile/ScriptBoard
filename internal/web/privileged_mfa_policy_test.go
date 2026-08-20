package web_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "scriptboard/internal/web"
)

func TestPrivilegedAccountWithoutMFAAccessesOverview(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	application, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	passwordBytes, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "initial-admin-password"))
	if err != nil {
		t.Fatal(err)
	}
	login(t, client, server.URL, strings.TrimSpace(string(passwordBytes)), http.StatusSeeOther)

	overviewResponse, err := client.Get(server.URL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	overview, _ := io.ReadAll(overviewResponse.Body)
	_ = overviewResponse.Body.Close()
	if overviewResponse.StatusCode != http.StatusOK || !strings.Contains(string(overview), `data-host-overview`) {
		t.Fatalf("monitor status=%d body=%s", overviewResponse.StatusCode, overview)
	}
}
