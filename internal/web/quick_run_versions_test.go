package web_test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

func TestQuickRunEditCanRebindExternalInterfacesToTheNewVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "versioned.sh", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "versioned.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	createQuickRunFromFile(t, client, serverURL, scriptPath, "Versioned deploy", "")
	quickPage := getQuickRunsPage(t, client, serverURL)
	quickRunID := quickRunIDForName(t, quickPage, "Versioned deploy")

	setQuickRunLockForVersionTest(t, client, serverURL, quickRunID, true)
	_, keyID := createExternalTestKey(t, client, serverURL, "Versioned agent")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"deploy"}, "label": {"Deploy"}, "action_type": {"quick_run"}, "enabled": {"1"}, "quick_run_id": {quickRunID},
	})

	setQuickRunLockForVersionTest(t, client, serverURL, quickRunID, false)
	editQuickRunForVersionTest(t, client, serverURL, quickRunID, "Version 2", false)
	setQuickRunLockForVersionTest(t, client, serverURL, quickRunID, true)
	response := invokeExternalForm(t, client, serverURL, secret, "legacy", "deploy", nil)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("external interface bound to the old version status=%d, want conflict", response.StatusCode)
	}

	setQuickRunLockForVersionTest(t, client, serverURL, quickRunID, false)
	editQuickRunForVersionTest(t, client, serverURL, quickRunID, "Version 3", true)
	setQuickRunLockForVersionTest(t, client, serverURL, quickRunID, true)
	response = invokeExternalForm(t, client, serverURL, secret, "legacy", "deploy", nil)
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("synchronized external interface status=%d body=%s", response.StatusCode, body)
	}

	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	var revision int64
	var configJSON string
	if err := database.QueryRow("SELECT revision FROM quick_runs WHERE id = ?", quickRunID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT config_json FROM external_trigger_entries WHERE name = 'deploy'").Scan(&configJSON); err != nil {
		t.Fatal(err)
	}
	if revision != 3 || !strings.Contains(configJSON, fmt.Sprintf(`"revision":%d`, revision)) {
		t.Fatalf("Quick Run revision=%d external config=%s", revision, configJSON)
	}
	response, err := client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	interfacesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(interfacesPage), quickRunID+" · v3") {
		t.Fatalf("bound Quick Run version is not visible on External Interfaces: %s", interfacesPage)
	}
}

func TestExternalQuickRunChoiceShowsGroupAndVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "selector.sh", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "selector.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(scriptContent)))
	if _, err := database.Exec("INSERT INTO quick_run_groups(id, name, sort_order, created_at, updated_at) VALUES ('release', 'Release tools', 1, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO quick_runs(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at, group_id, locked, script_sha256, revision, updated_at)
		VALUES ('selector-quick', 'Deploy API', ?, ?, '', 30, 1, 1, 'release', 1, ?, 7, 1)`, scriptPath, hostfiles.ComparisonKey(scriptPath), digest); err != nil {
		t.Fatal(err)
	}
	_, keyID := createExternalTestKey(t, client, serverURL, "Selector agent")
	response, err := client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/entries/new")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), "Release tools / Deploy API · v7") {
		t.Fatalf("Quick Run choice does not show group and version: %s", body)
	}
}

func setQuickRunLockForVersionTest(t *testing.T, client *http.Client, serverURL, id string, locked bool) {
	t.Helper()
	page := getQuickRunsPage(t, client, serverURL)
	value := "0"
	if locked {
		value = "1"
	}
	response, err := client.PostForm(serverURL+"/config/quick-runs/"+id+"/lock", url.Values{"csrf_token": {formToken(t, page)}, "locked": {value}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("set Quick Run lock status=%d", response.StatusCode)
	}
}

func editQuickRunForVersionTest(t *testing.T, client *http.Client, serverURL, id, name string, sync bool) {
	t.Helper()
	response, err := client.Get(serverURL + "/config/quick-runs/" + id + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	values := url.Values{"csrf_token": {formToken(t, page)}, "name": {name}, "timeout_seconds": {"30"}}
	if sync {
		values.Set("sync_external_interfaces", "1")
	}
	response, err = client.PostForm(serverURL+"/config/quick-runs/"+id+"/update", values)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit Quick Run status=%d", response.StatusCode)
	}
}
