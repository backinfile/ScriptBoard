package app_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

var externalKeyPattern = regexp.MustCompile(`sbk_[A-Za-z0-9_-]{16}\.[A-Za-z0-9_-]{43}`)

func TestAdministratorCreatesKeyAndExternalLogTrigger(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))

	response, err := client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(page), "External Interfaces") {
		t.Fatalf("external interfaces page status=%d body=%s", response.StatusCode, page)
	}

	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{
		"csrf_token": {formToken(t, page)},
		"label":      {"CI pipeline"},
		"duration":   {"1d"},
		"enabled":    {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	secret := externalKeyPattern.FindString(string(createdBody))
	if response.StatusCode != http.StatusCreated || secret == "" || !strings.Contains(string(createdBody), "only shown once") {
		t.Fatalf("create key status=%d secret=%q body=%s", response.StatusCode, secret, createdBody)
	}
	keyID := strings.Split(strings.TrimPrefix(secret, "sbk_"), ".")[0]

	response, err = client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/entries/new")
	if err != nil {
		t.Fatal(err)
	}
	entryPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys/"+keyID+"/entries", url.Values{
		"csrf_token":        {formToken(t, entryPage)},
		"name":              {"deployment-log"},
		"label":             {"Deployment callback"},
		"action_type":       {"log"},
		"log_category":      {"deploy"},
		"log_message_limit": {"1024"},
		"enabled":           {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create entry status=%d", response.StatusCode)
	}

	response, err = client.PostForm(serverURL+"/trigger?key="+url.QueryEscape(secret)+"&name=deployment-log", url.Values{"message": {"deployment finished"}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("trigger status=%d body=%s", response.StatusCode, body)
	}
	var payload struct {
		OK      bool   `json:"ok"`
		Action  string `json:"action"`
		Request string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Action != "log" || payload.Request == "" {
		t.Fatalf("trigger response = %#v", payload)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	activity, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(activity), "deployment finished") || !strings.Contains(string(activity), "deployment-log") {
		t.Fatalf("activity status=%d body=%s", response.StatusCode, activity)
	}
}

func TestExternalUploadAndConstrainedVariableActions(t *testing.T) {
	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	uploadRoot := filepath.Join(hostRoot, "incoming")
	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	secret, keyID := createExternalTestKey(t, client, serverURL, "Build agent")
	response, err := client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	keyEdit, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys/"+keyID, url.Values{"csrf_token": {formToken(t, keyEdit)}, "label": {"Release agent"}, "duration": {"7d"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update key status=%d", response.StatusCode)
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("INSERT INTO variables(name, value, is_password, created_at, updated_at) VALUES (?, ?, 0, 1, 1)", "environment", "staging"); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "external.sh", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "external.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO quick_runs(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at, updated_at)
		VALUES ('external-quick', 'External quick run', ?, ?, '', 30, 1, 1, 1)`, scriptPath, hostfiles.ComparisonKey(scriptPath)); err != nil {
		t.Fatal(err)
	}

	createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"artifact"}, "label": {"Artifact upload"}, "action_type": {"upload"}, "enabled": {"1"},
		"upload_directory": {uploadRoot}, "upload_max_bytes": {"32"}, "upload_extensions": {".txt"}, "upload_conflict": {"reject"},
	})
	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	interfacesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	editPattern := regexp.MustCompile(`/config/external-interfaces/entries/([A-Za-z0-9_-]+)/edit`)
	editMatch := editPattern.FindSubmatch(interfacesPage)
	if len(editMatch) != 2 {
		t.Fatalf("entry edit link missing: %s", interfacesPage)
	}
	response, err = client.Get(serverURL + string(editMatch[0]))
	if err != nil {
		t.Fatal(err)
	}
	entryEdit, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/external-interfaces/entries/"+string(editMatch[1]), url.Values{
		"csrf_token": {formToken(t, entryEdit)}, "name": {"artifact"}, "label": {"Artifact receiver"}, "action_type": {"upload"}, "enabled": {"1"},
		"upload_directory": {uploadRoot}, "upload_max_bytes": {"32"}, "upload_extensions": {".txt"}, "upload_conflict": {"reject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update entry status=%d", response.StatusCode)
	}
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "result.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("complete"))
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, serverURL+"/trigger?key="+url.QueryEscape(secret)+"&name=artifact", &upload)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload trigger status=%d body=%s", response.StatusCode, body)
	}
	if content, err := os.ReadFile(filepath.Join(uploadRoot, "result.txt")); err != nil || string(content) != "complete" {
		t.Fatalf("uploaded content=%q err=%v", content, err)
	}

	createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"environment"}, "label": {"Deployment environment"}, "action_type": {"variable"}, "enabled": {"1"},
		"variable_name": {"environment"}, "variable_type": {"enum"}, "variable_options": {"staging\nproduction"},
	})
	response, err = client.PostForm(serverURL+"/trigger?key="+url.QueryEscape(secret)+"&name=environment", url.Values{"value": {"production"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("variable trigger status=%d body=%s", response.StatusCode, body)
	}
	var value string
	if err := database.QueryRow("SELECT value FROM variables WHERE name = 'environment'").Scan(&value); err != nil || value != "production" {
		t.Fatalf("variable value=%q err=%v", value, err)
	}
	response, err = client.PostForm(serverURL+"/trigger?key="+url.QueryEscape(secret)+"&name=environment", url.Values{"value": {"root"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid enum status=%d", response.StatusCode)
	}

	createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"quick"}, "label": {"External quick run"}, "action_type": {"quick_run"}, "enabled": {"1"}, "quick_run_id": {"external-quick"},
	})
	request, _ = http.NewRequest(http.MethodPost, serverURL+"/trigger?key="+url.QueryEscape(secret)+"&name=quick", nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted || !bytes.Contains(body, []byte(`"run_id"`)) {
		t.Fatalf("quick run trigger status=%d body=%s", response.StatusCode, body)
	}
}

func createExternalTestKey(t *testing.T, client *http.Client, serverURL, label string) (string, string) {
	t.Helper()
	response, err := client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{"csrf_token": {formToken(t, page)}, "label": {label}, "duration": {"1d"}, "enabled": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	created, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	secret := externalKeyPattern.FindString(string(created))
	if response.StatusCode != http.StatusCreated || secret == "" {
		t.Fatalf("create key status=%d body=%s", response.StatusCode, created)
	}
	return secret, strings.Split(strings.TrimPrefix(secret, "sbk_"), ".")[0]
}

func createExternalTestEntry(t *testing.T, client *http.Client, serverURL, keyID string, values url.Values) {
	t.Helper()
	response, err := client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/entries/new")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	values.Set("csrf_token", formToken(t, page))
	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys/"+keyID+"/entries", values)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create entry status=%d body=%s", response.StatusCode, body)
	}
}
