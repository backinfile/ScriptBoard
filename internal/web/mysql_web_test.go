package web_test

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	app "scriptboard/internal/web"

	_ "modernc.org/sqlite"
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
	for _, expected := range []string{`data-mysql-instances-region`} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("database workspace is missing partial-refresh region %q: %s", expected, body)
		}
	}
	if !strings.Contains(string(body), "TLS can be disabled, preferred, or required. Disabling TLS sends credentials and database traffic in plaintext.") {
		t.Fatalf("database form does not explain plaintext mode: %s", body)
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
	for _, expected := range []string{"Production", "db.internal", "preferred", "Database backups", `class="mysql-instance-tabs__state" data-state="untried"`, "Not connected yet"} {
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
	for _, expected := range []string{`class="mysql-instance-workspace"`, `class="mysql-instance-rail"`, `class="mysql-instance-tabs"`, `class="mysql-instance-tabs__state" data-state="failed"`, `Connection failed`, `class="mysql-tabs"`, `tab=overview`, `tab=backups`, `data-connection-test`, `data-preserve-scroll`, `aria-current="page"`, `data-mysql-drop-drawer`, `class="mysql-overview-facts"`, `TLS mode`, `Preferred`, `Refresh status`} {
		if !strings.Contains(string(selectedBody), expected) {
			t.Fatalf("selected database workspace missing %q: %s", expected, selectedBody)
		}
	}
	if strings.Contains(string(selectedBody), `mysql-instance-tabs__tls`) {
		t.Fatalf("database instance rail still exposes TLS mode: %s", selectedBody)
	}
	if tlsIndex, indexSizeIndex := strings.Index(string(selectedBody), `TLS mode`), strings.Index(string(selectedBody), `Index size`); indexSizeIndex >= 0 && tlsIndex < indexSizeIndex {
		t.Fatalf("TLS mode should be the final overview fact: %s", selectedBody)
	}
}

func TestBackupRecordsFilterAndOpenConfirmationDrawers(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot})
	response, err := client.Get(serverURL + "/resources/databases")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/databases/instances", url.Values{
		"csrf_token": {formToken(t, body)}, "name": {"Filter fixture"}, "host": {"127.0.0.1"}, "port": {"1"},
		"username": {"scriptboard"}, "password": {"database-password"}, "tls_mode": {"preferred"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/resources/databases")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	instanceMatch := regexp.MustCompile(`/resources/databases\?instance=([a-f0-9]+)`).FindSubmatch(body)
	if len(instanceMatch) != 2 {
		t.Fatalf("database instance link missing: %s", body)
	}
	instanceID := string(instanceMatch[1])
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for index := 0; index < 14; index++ {
		databaseName := "inventory"
		if index == 13 {
			databaseName = "reporting"
		}
		backupID := fmt.Sprintf("backup-%s-%02d", databaseName, index)
		_, err = database.Exec(`INSERT INTO mysql_backups
			(id, instance_id, database_name, plan_id, kind, path, size_bytes, sha256, warning, created_at, created_by_user_id, created_by_username)
			VALUES (?, ?, ?, '', 'manual', ?, 128, 'fixture-sha', '', ?, '', 'fixture')`,
			backupID, instanceID, databaseName, filepath.Join(stateRoot, backupID+".sql"), time.Now().Add(time.Duration(index)*time.Second).UnixNano())
		if err != nil {
			t.Fatal(err)
		}
	}

	response, err = client.Get(serverURL + "/resources/databases?instance=" + instanceID + "&tab=backups&database=inventory")
	if err != nil {
		t.Fatal(err)
	}
	filteredBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	page := string(filteredBody)
	for _, expected := range []string{
		`name="database"`, `<option value="inventory" selected>inventory</option>`, `backup-inventory-12`,
		`database=inventory`, `data-mysql-backup-restore-trigger`, `data-mysql-backup-delete-trigger`,
		`data-mysql-backup-restore-drawer`, `data-mysql-backup-delete-drawer`, `data-mysql-backup-restore-form`, `data-mysql-backup-delete-form`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("filtered backup page missing %q: %s", expected, page)
		}
	}
	if strings.Contains(page, "backup-reporting-13") {
		t.Fatalf("database filter returned another database: %s", page)
	}
	actionMenu := regexp.MustCompile(`(?s)<details class="action-menu">.*?</details>`).FindString(page)
	if strings.Contains(actionMenu, "<input") || strings.Contains(actionMenu, "<form") {
		t.Fatalf("backup action menu still contains inline form controls: %s", actionMenu)
	}

	response, err = client.Get(serverURL + "/resources/databases?instance=" + instanceID + "&tab=backups&database=missing")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid backup filter status=%d", response.StatusCode)
	}
}
