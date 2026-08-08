package app_test

import (
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"scriptboard/internal/app"
)

func TestAdministratorCanRegisterMySQLInstanceFromDatabaseWorkspace(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot})
	response, err := client.Get(serverURL + "/resources/databases")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `data-mysql-workspace`) ||
		!strings.Contains(string(body), `/resources/databases/instances`) ||
		!strings.Contains(string(body), `class="mysql-drawer"`) ||
		!strings.Contains(string(body), `class="mysql-drawer-sheet"`) ||
		!strings.Contains(string(body), `action="/resources/databases/settings/backup-root"`) {
		t.Fatalf("database workspace status=%d body=%s", response.StatusCode, body)
	}
	for _, expected := range []string{`data-mysql-overview`, `data-mysql-instances-region`} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("database workspace is missing partial-refresh region %q: %s", expected, body)
		}
	}
	response, err = client.PostForm(serverURL+"/resources/databases/instances", url.Values{
		"csrf_token": {formToken(t, body)}, "name": {"Production"}, "host": {"db.internal"}, "port": {"3306"},
		"username": {"scriptboard"}, "password": {"database-password"}, "tls_mode": {"preferred"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/databases" {
		t.Fatalf("create instance status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + "/resources/databases")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{"Production", "db.internal", "preferred", "Database backups"} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("database workspace missing %q: %s", expected, body)
		}
	}
	instanceMatch := regexp.MustCompile(`/resources/databases\?instance=([a-f0-9]+)`).FindSubmatch(body)
	if len(instanceMatch) != 2 {
		t.Fatalf("database instance link missing: %s", body)
	}
	response, err = client.Get(serverURL + "/resources/databases?instance=" + string(instanceMatch[1]))
	if err != nil {
		t.Fatal(err)
	}
	selectedBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`class="mysql-tabs"`, `tab=backups`, `data-connection-test`} {
		if !strings.Contains(string(selectedBody), expected) {
			t.Fatalf("selected database workspace missing %q: %s", expected, selectedBody)
		}
	}
}
