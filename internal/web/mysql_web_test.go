package web_test

import (
	"database/sql"
	"encoding/json"
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

func TestMySQLWriteModeChallengeRequiresCSRFAndReturnsLocalizedStepUpDialog(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot})
	page := getBody(t, client, serverURL+"/resources/databases", http.StatusOK)

	response, err := client.PostForm(serverURL+"/resources/databases/sql/write-access/challenge", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("write-mode challenge without CSRF status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}

	request, err := http.NewRequest(http.MethodPost, serverURL+"/resources/databases/sql/write-access/challenge", strings.NewReader(url.Values{
		"csrf_token": {formToken(t, page)},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept-Language", "zh-CN")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("write-mode challenge status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	var challenge struct {
		Method      string `json:"method"`
		CSRFToken   string `json:"csrf_token"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&challenge); err != nil {
		t.Fatal(err)
	}
	if challenge.Method != "password" || challenge.CSRFToken == "" || challenge.Title != "确认启用可写模式" || !strings.Contains(challenge.Description, "再次验证") {
		t.Fatalf("unexpected write-mode challenge: %+v", challenge)
	}
}

func TestDatabasesPageCombinesMySQLAndRedisConnectionsAndOffersOneAddFlow(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot})
	page := string(getBody(t, client, serverURL+"/resources/databases", http.StatusOK))
	for _, expected := range []string{
		`data-database-workspace`,
		`href="/resources/databases?add=mysql"`,
		`href="/resources/databases?add=redis"`,
		`data-database-engine-choice`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("combined database workspace is missing %q: %s", expected, page)
		}
	}
	if strings.Contains(page, `class="database-engine-switch"`) {
		t.Fatalf("combined database workspace still renders the old page-level engine switch: %s", page)
	}
	csrfToken := formToken(t, []byte(page))
	response, err := client.PostForm(serverURL+"/resources/databases/instances", url.Values{
		"csrf_token": {csrfToken}, "name": {"Zulu SQL"}, "host": {"mysql.internal"}, "port": {"3306"},
		"username": {"scriptboard"}, "password": {"database-password"}, "tls_mode": {"preferred"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/databases/redis/instances", url.Values{
		"csrf_token": {csrfToken}, "name": {"Alpha Cache"}, "environment": {"production"},
		"host": {"redis.internal"}, "port": {"6379"}, "database": {"0"}, "tls_mode": {"disabled"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	mixedPage := string(getBody(t, client, serverURL+"/resources/databases", http.StatusOK))
	for _, expected := range []string{`class="mysql-instance-tabs database-connection-tabs"`, `data-engine="mysql"`, `data-engine="redis"`, `data-lucide="database"`, `data-lucide="memory-stick"`, `>MySQL</small>`, `>Redis</small>`} {
		if !strings.Contains(mixedPage, expected) {
			t.Fatalf("unified connection inventory is missing %q: %s", expected, mixedPage)
		}
	}
	if strings.Contains(mixedPage, `database-connection-group`) {
		t.Fatalf("connection inventory is still split into engine groups: %s", mixedPage)
	}
	if redisIndex, mysqlIndex := strings.Index(mixedPage, "Alpha Cache"), strings.Index(mixedPage, "Zulu SQL"); redisIndex < 0 || mysqlIndex < 0 || redisIndex >= mysqlIndex {
		t.Fatalf("mixed connection inventory is not ordered by name: %s", mixedPage)
	}

	redisAddPage := string(getBody(t, client, serverURL+"/resources/databases?add=redis", http.StatusOK))
	for _, expected := range []string{`data-database-add-drawer open`, `data-database-engine-choice="redis"`, `action="/resources/databases/redis/instances"`} {
		if !strings.Contains(redisAddPage, expected) {
			t.Fatalf("Redis choice in the shared add flow is missing %q: %s", expected, redisAddPage)
		}
	}
}

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
	for _, expected := range []string{"Production", "db.internal", "preferred", "Databases", `class="mysql-instance-tabs__state" data-state="untried"`, "Not connected yet"} {
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
	for _, expected := range []string{`class="mysql-instance-workspace"`, `class="mysql-instance-rail database-connection-rail"`, `class="mysql-instance-tabs database-connection-tabs"`, `class="mysql-instance-tabs__state" data-state="failed"`, `Connection failed`, `class="mysql-tabs"`, `tab=overview`, `tab=backups`, `data-connection-test`, `connection-test-result sr-only`, `data-preserve-scroll`, `aria-current="page"`, `data-mysql-drop-drawer`, `data-mysql-clear-drawer`, `action="/resources/databases/instances/` + string(instanceMatch[1]) + `/clear"`, `class="mysql-overview-facts"`, `TLS mode`, `Preferred`, `Refresh status`, `mysql-edit-instance-title`, `Edit instance`, `Leave blank to keep the current password.`, `name="id" value="` + string(instanceMatch[1]) + `"`, `name="name" value="Production"`, `class="mysql-danger-zone mysql-instance-delete"`, `action="/resources/databases/instances/` + string(instanceMatch[1]) + `/delete"`, `name="confirm" value="yes"`, `data-confirm="Remove this instance connection?`} {
		if !strings.Contains(string(selectedBody), expected) {
			t.Fatalf("selected database workspace missing %q: %s", expected, selectedBody)
		}
	}
	for _, expected := range []string{`class="mysql-detail database-detail"`, `data-database-engine="mysql"`, `data-database-detail-tabs`, `data-database-tabs`, `data-database-tab-panel="overview"`} {
		if !strings.Contains(string(selectedBody), expected) {
			t.Fatalf("MySQL detail does not follow the shared database tab framework; missing %q: %s", expected, selectedBody)
		}
	}
	if strings.Contains(string(selectedBody), `mysql-instance-tabs__tls`) {
		t.Fatalf("database instance rail still exposes TLS mode: %s", selectedBody)
	}
	if tlsIndex, indexSizeIndex := strings.Index(string(selectedBody), `TLS mode`), strings.Index(string(selectedBody), `Index size`); indexSizeIndex >= 0 && tlsIndex < indexSizeIndex {
		t.Fatalf("TLS mode should be the final overview fact: %s", selectedBody)
	}
	instanceID := string(instanceMatch[1])
	response, err = client.PostForm(serverURL+"/resources/databases/instances/"+instanceID+"/clear", url.Values{
		"csrf_token": {formToken(t, selectedBody)}, "database": {"inventory"}, "confirmation": {"wrong"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("clear database without matching confirmation status=%d", response.StatusCode)
	}
	sqlPage := string(getBody(t, client, serverURL+"/resources/databases?instance="+instanceID+"&tab=sql", http.StatusOK))
	for _, expected := range []string{
		`data-mysql-query-settings-drawer`, `data-lucide="sliders-horizontal"`, `data-mysql-timeout-summary`,
		`data-mysql-max-rows-summary`, `data-mysql-query-timeout`, `data-mysql-query-max-rows`,
		`data-mysql-query-settings-apply`, `name="timeout_seconds"`, `name="max_rows"`,
	} {
		if !strings.Contains(sqlPage, expected) {
			t.Fatalf("SQL query settings drawer missing %q: %s", expected, sqlPage)
		}
	}
	if strings.Contains(sqlPage, `class="mysql-sql-limits"`) {
		t.Fatalf("SQL execution limits still occupy the persistent footer: %s", sqlPage)
	}
	for _, endpoint := range []string{"sql", "sql/write"} {
		response, err = client.PostForm(serverURL+"/resources/databases/instances/"+instanceID+"/"+endpoint, url.Values{
			"database": {"scriptboard_qa"}, "statement": {"SELECT 1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("MySQL %s without CSRF status=%d, want %d", endpoint, response.StatusCode, http.StatusForbidden)
		}
	}
	response, err = client.PostForm(serverURL+"/resources/databases/instances", url.Values{
		"csrf_token": {formToken(t, selectedBody)}, "id": {instanceID}, "name": {"Production renamed"},
		"host": {"db2.internal"}, "port": {"3307"}, "username": {"scriptboard2"}, "password": {""}, "tls_mode": {"disabled"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/databases?instance="+instanceID {
		t.Fatalf("edit instance status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + "/resources/databases?instance=" + instanceID)
	if err != nil {
		t.Fatal(err)
	}
	editedBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{"Production renamed", "db2.internal", `value="3307"`, `value="scriptboard2"`, `<option value="disabled" selected>`} {
		if !strings.Contains(string(editedBody), expected) {
			t.Fatalf("edited database workspace missing %q: %s", expected, editedBody)
		}
	}
	response, err = client.PostForm(serverURL+"/resources/databases/instances/"+instanceID+"/delete", url.Values{
		"csrf_token": {formToken(t, editedBody)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete instance without explicit confirmation status=%d", response.StatusCode)
	}
	response, err = client.PostForm(serverURL+"/resources/databases/instances/"+instanceID+"/delete", url.Values{
		"csrf_token": {formToken(t, editedBody)}, "confirm": {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/databases" {
		t.Fatalf("delete instance status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
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
	if !strings.Contains(page, `action="/resources/databases/instances/`+instanceID+`/delete"`) {
		t.Fatalf("backup tab edit drawer is missing the remove-instance action: %s", page)
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

func TestBackupPlanCanBeEditedInDrawerAndRequiresTypedDeleteConfirmation(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot})
	page := getBody(t, client, serverURL+"/resources/databases", http.StatusOK)
	response, err := client.PostForm(serverURL+"/resources/databases/instances", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"Plan fixture"}, "host": {"127.0.0.1"}, "port": {"1"},
		"username": {"scriptboard"}, "password": {"database-password"}, "tls_mode": {"preferred"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	page = getBody(t, client, serverURL+"/resources/databases", http.StatusOK)
	instanceMatch := regexp.MustCompile(`/resources/databases\?instance=([a-f0-9]+)`).FindStringSubmatch(string(page))
	if len(instanceMatch) != 2 {
		t.Fatalf("database instance link missing: %s", page)
	}
	instanceID := instanceMatch[1]
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().UnixNano()
	if _, err := database.Exec(`INSERT INTO mysql_backup_plans
		(id,name,instance_id,databases_json,expression,retention_count,enabled,next_fire_at,created_at,updated_at)
		VALUES ('plan-edit-fixture','Nightly',?, '["inventory"]','0 2 * * *',7,1,?,?,?)`, instanceID, now, now, now); err != nil {
		t.Fatal(err)
	}

	page = getBody(t, client, serverURL+"/resources/databases?instance="+instanceID+"&tab=plans", http.StatusOK)
	for _, expected := range []string{
		`action="/resources/databases/plans/plan-edit-fixture"`, `value="Nightly"`, `value="0 2 * * *"`,
		`value="inventory" checked`, `data-mysql-plan-delete-trigger`, `data-mysql-plan-delete-drawer`, `data-mysql-plan-delete-form`,
	} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("plan drawer missing %q: %s", expected, page)
		}
	}
	response, err = client.PostForm(serverURL+"/resources/databases/plans/plan-edit-fixture", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"Weeknight"}, "expression": {"30 1 * * 1-5"},
		"retention_count": {"14"}, "databases": {"inventory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "tab=plans") {
		t.Fatalf("update plan status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	var name, expression string
	var retention int
	if err := database.QueryRow("SELECT name,expression,retention_count FROM mysql_backup_plans WHERE id='plan-edit-fixture'").Scan(&name, &expression, &retention); err != nil || name != "Weeknight" || expression != "30 1 * * 1-5" || retention != 14 {
		t.Fatalf("updated plan = %q %q %d, err=%v", name, expression, retention, err)
	}
	response, err = client.PostForm(serverURL+"/resources/databases/plans/plan-edit-fixture/delete", url.Values{
		"csrf_token": {formToken(t, page)}, "confirmation": {"wrong"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed delete status=%d", response.StatusCode)
	}
}
