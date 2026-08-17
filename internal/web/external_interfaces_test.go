package web_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
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
	"time"

	"scriptboard/internal/hostfiles"
)

var externalKeyPattern = regexp.MustCompile(`sbk_[A-Za-z0-9_-]{16}\.[A-Za-z0-9_-]{43}`)

func TestExternalInterfaceGroupOwnsPathsAndMultipleKeys(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/external-interfaces/groups", url.Values{"csrf_token": {formToken(t, page)}, "label": {"Deployment automation"}, "call_name": {"deployment-automation"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	groupMatch := regexp.MustCompile(`/config/external-interfaces/groups/([A-Za-z0-9_-]+)/entries/new`).FindSubmatch(page)
	if len(groupMatch) != 2 || !bytes.Contains(page, []byte("Deployment automation")) {
		t.Fatalf("group page missing hierarchy: %s", page)
	}
	groupID := string(groupMatch[1])
	for _, suffix := range []string{"/entries/new", "/keys/new"} {
		response, err = client.Get(serverURL + "/config/external-interfaces/groups/" + groupID + suffix)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("Deployment automation")) {
			t.Fatalf("group task %s status=%d body=%s", suffix, response.StatusCode, body)
		}
	}
	entryTaskResponse, err := client.Get(serverURL + "/config/external-interfaces/groups/" + groupID + "/entries/new")
	if err != nil {
		t.Fatal(err)
	}
	entryTask, _ := io.ReadAll(entryTaskResponse.Body)
	_ = entryTaskResponse.Body.Close()
	for _, expected := range []string{`name="log_target_mode" value="managed"`, `data-external-log-rotate`, `name="log_max_file_mb"`, `name="log_max_backups"`} {
		if !bytes.Contains(entryTask, []byte(expected)) {
			t.Fatalf("managed log controls missing %q: %s", expected, entryTask)
		}
	}
	managedValues := url.Values{
		"csrf_token": {formToken(t, entryTask)}, "name": {"managed-log"}, "label": {"Managed log"}, "action_type": {"log"},
		"log_target_mode": {"managed"}, "log_rotate": {"1"}, "log_max_file_mb": {"1"}, "log_max_backups": {"2"},
		"log_message_limit": {"1024"}, "enabled": {"1"},
	}
	managedResponse, err := client.PostForm(serverURL+"/config/external-interfaces/groups/"+groupID+"/entries", managedValues)
	if err != nil {
		t.Fatal(err)
	}
	_ = managedResponse.Body.Close()
	database := openExternalTestDatabase(t, filepath.Join(root, "state", "app.db"))
	var managedTarget, managedConfigJSON string
	if err := database.QueryRow(`SELECT target, config_json FROM external_trigger_entries WHERE group_id = ? AND name = 'managed-log'`, groupID).Scan(&managedTarget, &managedConfigJSON); err != nil {
		t.Fatal(err)
	}
	wantManagedTarget := filepath.Join(root, "scriptboard-external-"+groupID+"-managed-log.log")
	if !hostfiles.Contains(managedTarget, wantManagedTarget) || !hostfiles.Contains(wantManagedTarget, managedTarget) || !strings.Contains(managedConfigJSON, `"managed":true`) || !strings.Contains(managedConfigJSON, `"max_file_bytes":1048576`) || !strings.Contains(managedConfigJSON, `"max_backups":2`) {
		t.Fatalf("managed log target=%q config=%s", managedTarget, managedConfigJSON)
	}
	keyTaskResponse, err := client.Get(serverURL + "/config/external-interfaces/groups/" + groupID + "/keys/new")
	if err != nil {
		t.Fatal(err)
	}
	keyTask, _ := io.ReadAll(keyTaskResponse.Body)
	_ = keyTaskResponse.Body.Close()
	keyValues := url.Values{"csrf_token": {formToken(t, keyTask)}, "label": {"Deploy key"}, "duration": {"1d"}, "enabled": {"1"}}
	created, err := client.PostForm(serverURL+"/config/external-interfaces/groups/"+groupID+"/keys", keyValues)
	if err != nil {
		t.Fatal(err)
	}
	_ = created.Body.Close()
	duplicate, err := client.PostForm(serverURL+"/config/external-interfaces/groups/"+groupID+"/keys", keyValues)
	if err != nil {
		t.Fatal(err)
	}
	duplicateBody, _ := io.ReadAll(duplicate.Body)
	_ = duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusUnprocessableEntity ||
		!bytes.Contains(duplicateBody, []byte("Deployment automation")) ||
		!bytes.Contains(duplicateBody, []byte(`action="/config/external-interfaces/groups/`+groupID+`/keys"`)) {
		t.Fatalf("group key validation must stay in the same drawer and group: status=%d body=%s", duplicate.StatusCode, duplicateBody)
	}
}

func createdExternalTestKey(t *testing.T, body []byte) (string, string) {
	t.Helper()
	secret := string(externalKeyPattern.Find(body))
	if secret == "" {
		t.Fatalf("created key response is missing the one-time secret: %s", body)
	}
	identifier, _, ok := strings.Cut(strings.TrimPrefix(secret, "sbk_"), ".")
	if !ok || len(identifier) != 16 {
		t.Fatalf("invalid external key secret: %q", secret)
	}
	return secret, identifier
}

func createdExternalKeyID(t *testing.T, body []byte) string {
	t.Helper()
	_, identifier := createdExternalTestKey(t, body)
	return identifier
}

func externalTriggerPath(group, name string) string {
	return "/trigger/" + url.PathEscape(group) + "/" + url.PathEscape(name)
}

func invokeExternalForm(t *testing.T, client *http.Client, serverURL, secret, group, name string, values url.Values) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, serverURL+externalTriggerPath(group, name), strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestExternalInterfaceKeyCannotBeRetrievedAfterCreation(t *testing.T) {
	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	pageResponse, err := admin.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	created, err := admin.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{
		"csrf_token": {formToken(t, page)}, "label": {"Viewer denied"}, "duration": {"1d"}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdBody, _ := io.ReadAll(created.Body)
	_ = created.Body.Close()
	secret, keyID := createdExternalTestKey(t, createdBody)
	viewer := createRoleUserClient(t, admin, serverURL, "external-key-viewer", "viewer")
	for name, client := range map[string]*http.Client{"administrator": admin, "viewer": viewer} {
		response, err := client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/copy")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK || bytes.Contains(body, []byte(secret)) {
			t.Fatalf("%s retrieved a one-time external key: status=%d body=%s", name, response.StatusCode, body)
		}
	}
}

func TestExternalWebsiteMonitorEntryReturnsReadOnlySnapshot(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))

	websiteTask, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	websitePage, _ := io.ReadAll(websiteTask.Body)
	_ = websiteTask.Body.Close()
	createdMonitor, err := client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token": {formToken(t, websitePage)}, "name": {"Remote-visible site"},
		"scope": {"external"}, "kind": {"http"}, "url": {"https://status.example/"},
		"frequency_seconds": {"60"}, "timeout_seconds": {"10"}, "http_method": {"GET"},
		"follow_redirects": {"1"}, "verify_tls": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = createdMonitor.Body.Close()

	interfaces, err := client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	interfacePage, _ := io.ReadAll(interfaces.Body)
	_ = interfaces.Body.Close()
	createdKey, err := client.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{
		"csrf_token": {formToken(t, interfacePage)}, "label": {"Website reader"}, "duration": {"1d"}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdKeyPage, _ := io.ReadAll(createdKey.Body)
	_ = createdKey.Body.Close()
	_, keyID := createdExternalTestKey(t, createdKeyPage)

	entryTask, err := client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/entries/new")
	if err != nil {
		t.Fatal(err)
	}
	entryPage, _ := io.ReadAll(entryTask.Body)
	_ = entryTask.Body.Close()
	createdEntry, err := client.PostForm(serverURL+"/config/external-interfaces/keys/"+keyID+"/entries", url.Values{
		"csrf_token": {formToken(t, entryPage)}, "label": {"Website status"}, "name": {"website-status"},
		"action_type": {"website_monitor"}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdEntryPage, _ := io.ReadAll(createdEntry.Body)
	_ = createdEntry.Body.Close()
	secret, _ := createdExternalTestKey(t, createdEntryPage)

	request, _ := http.NewRequest(http.MethodGet, serverURL+externalTriggerPath("legacy", "website-status"), nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload struct {
		Action string `json:"action"`
		Data   struct {
			Total    int                          `json:"total"`
			Monitors []struct{ Name, URL string } `json:"monitors"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || payload.Action != "website_monitor" || payload.Data.Total != 1 ||
		len(payload.Data.Monitors) != 1 || payload.Data.Monitors[0].Name != "Remote-visible site" {
		t.Fatalf("status=%d payload=%#v", response.StatusCode, payload)
	}
}

func TestDuplicateExternalKeyNamesStayInTheCurrentDrawer(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	newTaskResponse, err := client.Get(serverURL + "/config/external-interfaces/keys/new")
	if err != nil {
		t.Fatal(err)
	}
	newTask, _ := io.ReadAll(newTaskResponse.Body)
	_ = newTaskResponse.Body.Close()
	csrfToken := formToken(t, newTask)

	created, err := client.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{
		"csrf_token": {csrfToken}, "label": {"Webhook"}, "duration": {"1d"}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdBody, _ := io.ReadAll(created.Body)
	_ = created.Body.Close()
	firstID := createdExternalKeyID(t, createdBody)

	duplicate, err := client.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{
		"csrf_token": {csrfToken}, "label": {" webhook "}, "duration": {"30d"}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicateBody, _ := io.ReadAll(duplicate.Body)
	_ = duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(string(duplicateBody), `data-task-kind="external-key-new"`) ||
		!strings.Contains(string(duplicateBody), `role="alert"`) ||
		!strings.Contains(string(duplicateBody), `value=" webhook "`) ||
		!strings.Contains(string(duplicateBody), `<option value="30d" selected>`) ||
		strings.Contains(string(duplicateBody), `class="application-error`) {
		t.Fatalf("duplicate create should stay in the populated drawer: status=%d body=%s", duplicate.StatusCode, duplicateBody)
	}

	created, err = client.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{
		"csrf_token": {csrfToken}, "label": {"Deploy"}, "duration": {"never"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdBody, _ = io.ReadAll(created.Body)
	_ = created.Body.Close()
	secondID := createdExternalKeyID(t, createdBody)
	editResponse, err := client.Get(serverURL + "/config/external-interfaces/keys/" + secondID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	editTask, _ := io.ReadAll(editResponse.Body)
	_ = editResponse.Body.Close()
	if !strings.Contains(string(editTask), `data-async`) {
		t.Fatalf("key edit form should submit inside the drawer: %s", editTask)
	}
	duplicateEdit, err := client.PostForm(serverURL+"/config/external-interfaces/keys/"+secondID, url.Values{
		"csrf_token": {formToken(t, editTask)}, "label": {"WEBHOOK"}, "duration": {"7d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicateEditBody, _ := io.ReadAll(duplicateEdit.Body)
	_ = duplicateEdit.Body.Close()
	if duplicateEdit.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(string(duplicateEditBody), `data-task-kind="external-key-edit"`) ||
		!strings.Contains(string(duplicateEditBody), `role="alert"`) ||
		!strings.Contains(string(duplicateEditBody), `value="WEBHOOK"`) ||
		!strings.Contains(string(duplicateEditBody), `<option value="7d" selected>`) ||
		strings.Contains(string(duplicateEditBody), `class="application-error`) {
		t.Fatalf("duplicate edit should stay in the populated drawer: status=%d body=%s", duplicateEdit.StatusCode, duplicateEditBody)
	}
	if firstID == secondID {
		t.Fatal("test keys unexpectedly share an ID")
	}
}

func TestAdministratorCreatesKeyAndExternalLogTrigger(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(hostRoot, "deployment.log")
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

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
	for _, expected := range []string{`class="external-interface-tabs"`, `href="/config/external-interfaces" aria-current="page"`, `href="/config/external-interfaces?tab=activity"`, `href="/config/external-interfaces?tab=logs"`, `>Interfaces<`} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("external interfaces page is missing tab contract %q: %s", expected, page)
		}
	}
	if strings.Contains(string(page), `class="external-activity"`) {
		t.Fatalf("interface list tab still renders call history: %s", page)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces/keys/new")
	if err != nil {
		t.Fatal(err)
	}
	newKeyTask, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(newKeyTask), `data-task-kind="external-key-new"`) ||
		!strings.Contains(string(newKeyTask), `external-key-drawer-page`) ||
		!strings.Contains(string(newKeyTask), `<form method="post" action="/config/external-interfaces/keys" data-async>`) {
		t.Fatalf("create key task should submit inside the drawer: status=%d body=%s", response.StatusCode, newKeyTask)
	}

	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys", url.Values{
		"csrf_token": {formToken(t, newKeyTask)},
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
	secret, keyID := createdExternalTestKey(t, createdBody)
	if response.StatusCode != http.StatusCreated || !strings.Contains(string(createdBody), `data-task-kind="external-key-created"`) ||
		!strings.Contains(string(createdBody), `data-task-refresh-on-close`) ||
		!strings.Contains(string(createdBody), `data-copy-text data-copy-target="external-key-secret"`) ||
		!strings.Contains(string(createdBody), secret) || response.Header.Get("Cache-Control") != "no-store" ||
		strings.Contains(string(createdBody), `class="task-back"`) {
		t.Fatalf("create key should render a one-time in-drawer result: status=%d body=%s", response.StatusCode, createdBody)
	}

	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	keyList, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`data-external-key-manager`, `class="external-key-manager__sheet"`, `>Manage keys<`, `class="external-key-manager__intro"`} {
		if !strings.Contains(string(keyList), expected) {
			t.Fatalf("key management drawer missing %q: %s", expected, keyList)
		}
	}
	copyURL := "/config/external-interfaces/keys/" + keyID + "/copy"
	if strings.Contains(string(keyList), copyURL) || strings.Contains(string(keyList), secret) || strings.Contains(string(keyList), `data-copy-key`) {
		t.Fatalf("key list retained a complete-key retrieval surface: %s", keyList)
	}
	rotateTaskURL := "/config/external-interfaces/keys/" + keyID + "/rotate"
	if strings.Contains(string(keyList), `href="`+rotateTaskURL+`"`) {
		t.Fatalf("generate-new-key action should not appear in the key management row: %s", keyList)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	editTask, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(editTask), `href="`+rotateTaskURL+`" data-task-link`) ||
		!strings.Contains(string(editTask), `data-task-inner-close`) {
		t.Fatalf("generate-new-key action missing from key edit drawer: status=%d body=%s", response.StatusCode, editTask)
	}
	response, err = client.Get(serverURL + rotateTaskURL)
	if err != nil {
		t.Fatal(err)
	}
	rotateTask, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	expectedMaskedKey := "sbk_" + keyID + ".••••" + secret[len(secret)-4:]
	if response.StatusCode != http.StatusOK || !strings.Contains(string(rotateTask), `data-task-kind="external-key-rotate"`) ||
		!strings.Contains(string(rotateTask), `method="post"`) || !strings.Contains(string(rotateTask), `data-async`) ||
		!strings.Contains(string(rotateTask), expectedMaskedKey) || strings.Contains(string(rotateTask), secret) {
		t.Fatalf("generate-new-key task status=%d body=%s", response.StatusCode, rotateTask)
	}

	response, err = client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/entries/new")
	if err != nil {
		t.Fatal(err)
	}
	entryPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entryPage), `data-task-inner-close`) || !strings.Contains(string(entryPage), `data-external-entry-form data-async`) {
		t.Fatalf("new external function drawer should prefer its inner close action: %s", entryPage)
	}
	invalidEntry, err := client.PostForm(serverURL+"/config/external-interfaces/keys/"+keyID+"/entries", url.Values{
		"csrf_token": {formToken(t, entryPage)}, "name": {"invalid-entry"}, "label": {"Invalid entry"},
		"action_type": {"invalid"}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidEntryBody, _ := io.ReadAll(invalidEntry.Body)
	_ = invalidEntry.Body.Close()
	if invalidEntry.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(string(invalidEntryBody), `data-task-kind="external-entry-new"`) ||
		!strings.Contains(string(invalidEntryBody), `role="alert"`) ||
		!strings.Contains(string(invalidEntryBody), `value="Invalid entry"`) ||
		strings.Contains(string(invalidEntryBody), `class="application-error`) {
		t.Fatalf("invalid entry should stay in the populated drawer: status=%d body=%s", invalidEntry.StatusCode, invalidEntryBody)
	}
	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys/"+keyID+"/entries", url.Values{
		"csrf_token":        {formToken(t, entryPage)},
		"name":              {"deployment-log"},
		"label":             {"Deployment callback"},
		"action_type":       {"log"},
		"log_file":          {logFile},
		"log_category":      {"deploy"},
		"log_message_limit": {"1024"},
		"enabled":           {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	boundKeyPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create entry status=%d body=%s", response.StatusCode, boundKeyPage)
	}
	secret, _ = createdExternalTestKey(t, boundKeyPage)

	queryRequest, _ := http.NewRequest(http.MethodPost, serverURL+externalTriggerPath("legacy", "deployment-log")+"?key="+url.QueryEscape(secret), strings.NewReader(url.Values{"message": {"must not be logged"}}.Encode()))
	queryRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	queryResponse, err := client.Do(queryRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = queryResponse.Body.Close()
	if queryResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query-string key status=%d, want 401", queryResponse.StatusCode)
	}

	response = invokeExternalForm(t, client, serverURL, secret, "legacy", "deployment-log", url.Values{"message": {"deployment finished"}})
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
	logged, err := os.ReadFile(logFile)
	if err != nil || !strings.Contains(string(logged), "[deploy]\tdeployment finished") || strings.Contains(string(logged), "must not be logged") {
		t.Fatalf("log file content=%q err=%v", logged, err)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces?tab=activity")
	if err != nil {
		t.Fatal(err)
	}
	activity, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	previewURL := hostFileHref("/resources/files/view", logFile)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(activity), "deployment finished") || !strings.Contains(string(activity), "deployment-log") {
		t.Fatalf("activity status=%d body=%s", response.StatusCode, activity)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	interfaces, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	detailPattern := regexp.MustCompile(`/config/external-interfaces/entries/([A-Za-z0-9_-]+)" data-task-link`)
	detailMatch := detailPattern.FindSubmatch(interfaces)
	if len(detailMatch) != 2 {
		t.Fatalf("entry detail link missing: %s", interfaces)
	}
	if !strings.Contains(string(interfaces), `data-external-entry-row`) {
		t.Fatalf("clickable entry row missing: %s", interfaces)
	}
	entryID := string(detailMatch[1])
	if !strings.Contains(string(interfaces), `href="/config/external-interfaces/entries/`+entryID+`/edit" data-task-link aria-label="Edit callable function"`) {
		t.Fatalf("entry edit drawer action missing: %s", interfaces)
	}
	if !strings.Contains(string(interfaces), `data-external-group-id=`) ||
		!strings.Contains(string(interfaces), `<span class="external-key__facts"><span>Callable function <strong>1</strong></span><span aria-hidden="true">·</span><span>Keys <strong>1</strong></span></span>`) ||
		!strings.Contains(string(interfaces), `/toggle" data-async`) ||
		!strings.Contains(string(interfaces), `/delete" data-confirm=`) ||
		!strings.Contains(string(interfaces), `data-confirm="Delete this key and all of its callable functions?" data-async`) {
		t.Fatalf("external group fragment contract missing: %s", interfaces)
	}
	for _, action := range []string{`href="` + previewURL + `"`, `/config/external-interfaces/entries/` + entryID + `/toggle`, `/config/external-interfaces/entries/` + entryID + `/delete`} {
		if strings.Contains(string(interfaces), action) {
			t.Fatalf("entry action %q should only appear in the drawer: %s", action, interfaces)
		}
	}
	response, err = client.Get(serverURL + strings.TrimSuffix(string(detailMatch[0]), `" data-task-link`))
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(detail), `data-task-kind="external-entry-detail"`) ||
		!strings.Contains(string(detail), "POST /trigger/legacy/deployment-log") ||
		!strings.Contains(string(detail), "Authorization: Bearer YOUR_KEY") || !strings.Contains(string(detail), "message") ||
		!strings.Contains(string(detail), `href="`+previewURL+`"`) || strings.Contains(string(detail), `/entries/`+entryID+`/edit`) ||
		!strings.Contains(string(detail), `/entries/`+entryID+`/toggle`) || !strings.Contains(string(detail), `/entries/`+entryID+`/delete`) {
		t.Fatalf("entry detail status=%d body=%s", response.StatusCode, detail)
	}
	response, err = client.Get(serverURL + previewURL)
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(preview), "deployment finished") {
		t.Fatalf("log preview status=%d body=%s", response.StatusCode, preview)
	}
}

func TestExternalLogFilesTabListsSearchesAndDownloadsCurrentAndRotatedFiles(t *testing.T) {
	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/external-interfaces/groups", url.Values{
		"csrf_token": {formToken(t, page)}, "label": {"Archive receivers"}, "call_name": {"archive-receivers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	groupMatch := regexp.MustCompile(`/config/external-interfaces/groups/([A-Za-z0-9_-]+)/entries/new`).FindSubmatch(page)
	if len(groupMatch) != 2 {
		t.Fatalf("created group is missing: %s", page)
	}
	groupID := string(groupMatch[1])
	response, err = client.Get(serverURL + "/config/external-interfaces/groups/" + groupID + "/entries/new")
	if err != nil {
		t.Fatal(err)
	}
	entryTask, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	logFile := filepath.Join(hostRoot, "incoming.log")
	response, err = client.PostForm(serverURL+"/config/external-interfaces/groups/"+groupID+"/entries", url.Values{
		"csrf_token": {formToken(t, entryTask)}, "name": {"archive-log"}, "label": {"Archive receiver"}, "action_type": {"log"},
		"log_target_mode": {"custom"}, "log_file": {logFile}, "log_rotate": {"1"}, "log_max_file_mb": {"1"},
		"log_max_backups": {"2"}, "log_message_limit": {"1024"}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if err := os.WriteFile(logFile, []byte("current record\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile+".1", []byte("rotated record\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	readLogs := func(query string) string {
		t.Helper()
		response, requestErr := client.Get(serverURL + "/config/external-interfaces?tab=logs" + query)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("log files page status=%d body=%s", response.StatusCode, body)
		}
		return string(body)
	}
	logsPage := readLogs("")
	for _, expected := range []string{
		`href="/config/external-interfaces?tab=logs" aria-current="page"`, `data-external-log-files`, `Archive receiver`,
		`<details class="external-log-group" open>`, `class="external-log-group__chevron"`, `class="external-log-entry__identity"`, `<h3>Archive receivers</h3>`, `<code>archive-receivers</code>`,
		`data-preserve-scroll`,
		`incoming.log`, `incoming.log.1`, `name="tab" value="logs"`, `/resources/files/download?path=` + url.QueryEscape(logFile),
	} {
		if !strings.Contains(logsPage, expected) {
			t.Fatalf("log files page is missing %q: %s", expected, logsPage)
		}
	}
	searched := readLogs("&q=archive-log")
	if !strings.Contains(searched, "Archive receiver") || !strings.Contains(searched, "2 log files") {
		t.Fatalf("log file search did not retain both current and archive files: %s", searched)
	}
	missing := readLogs("&q=no-such-log")
	if !strings.Contains(missing, "No log files match this query.") || strings.Contains(missing, "incoming.log") {
		t.Fatalf("empty log search state is incorrect: %s", missing)
	}
}

func TestRotatedExternalKeyIsRenderedOnceAndNotRecoverable(t *testing.T) {
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	original, keyID := createExternalTestKey(t, client, serverURL, "Rotation display")
	response, err := client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/rotate")
	if err != nil {
		t.Fatal(err)
	}
	rotateTask, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/external-interfaces/keys/"+keyID+"/rotate", url.Values{
		"csrf_token": {formToken(t, rotateTask)}, "confirm": {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rotatedPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	rotated, rotatedID := createdExternalTestKey(t, rotatedPage)
	if response.StatusCode != http.StatusCreated || !strings.Contains(string(rotatedPage), `data-task-kind="external-key-rotated"`) ||
		!strings.Contains(string(rotatedPage), `data-task-refresh-on-close`) ||
		!strings.Contains(string(rotatedPage), `data-copy-text data-copy-target="external-key-secret"`) ||
		response.Header.Get("Cache-Control") != "no-store" || rotatedID != keyID || rotated == original {
		t.Fatalf("rotated key response is missing its one-time secret: status=%d body=%s", response.StatusCode, rotatedPage)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/copy")
	if err != nil {
		t.Fatal(err)
	}
	copyBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode == http.StatusOK || bytes.Contains(copyBody, []byte(rotated)) {
		t.Fatalf("rotated key remained recoverable: status=%d body=%s", response.StatusCode, copyBody)
	}
}

func TestExternalInvocationHistorySupportsSearchDateFiltersAndPagination(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), stateRoot)
	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	now := time.Now()
	for index := 0; index < 21; index++ {
		_, err := database.Exec(`INSERT INTO external_trigger_requests
			(id, occurred_at, key_id, key_label, entry_id, entry_name, action_type, result, http_status, message)
			VALUES (?, ?, 'key', 'Release service', 'entry', ?, 'log', 'succeeded', 200, ?)`,
			fmt.Sprintf("request-%02d", index), now.Unix(), fmt.Sprintf("call-%02d", index), fmt.Sprintf("message-%02d", index))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := database.Exec(`INSERT INTO external_trigger_requests
		(id, occurred_at, key_id, key_label, entry_id, entry_name, action_type, result, http_status, message)
		VALUES ('old-request', ?, 'key', 'Legacy service', 'entry', 'legacy-call', 'log', 'rejected', 400, 'old-message')`, now.AddDate(0, 0, -10).Unix())
	if err != nil {
		t.Fatal(err)
	}

	readPage := func(path string) string {
		t.Helper()
		response, requestErr := client.Get(serverURL + path)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, response.StatusCode, body)
		}
		return string(body)
	}
	first := readPage("/config/external-interfaces?tab=activity")
	if !strings.Contains(first, "22 records") || !strings.Contains(first, "1 / 2") || !strings.Contains(first, `name="q"`) ||
		!strings.Contains(first, `name="tab" value="activity"`) || !strings.Contains(first, `?page=2&amp;tab=activity`) {
		t.Fatalf("first page missing history controls or pagination: %s", first)
	}
	second := readPage("/config/external-interfaces?tab=activity&page=2")
	if !strings.Contains(second, "22 records") || !strings.Contains(second, "2 / 2") {
		t.Fatalf("second page missing pagination: %s", second)
	}
	searched := readPage("/config/external-interfaces?tab=activity&q=message-07")
	if !strings.Contains(searched, "message-07") || strings.Contains(searched, "message-08") || !strings.Contains(searched, "1 records") {
		t.Fatalf("search result is incorrect: %s", searched)
	}
	today := now.Format(time.DateOnly)
	filtered := readPage("/config/external-interfaces?tab=activity&from=" + today + "&to=" + today)
	if !strings.Contains(filtered, "21 records") || strings.Contains(filtered, "old-message") {
		t.Fatalf("date filter result is incorrect: %s", filtered)
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
	_, keyID := createExternalTestKey(t, client, serverURL, "Build agent")
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

	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
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
	scriptDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(scriptContent)))
	if _, err := database.Exec(`INSERT INTO quick_runs(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at, locked, script_sha256, revision, updated_at)
		VALUES ('external-quick', 'External quick run', ?, ?, '', 30, 1, 1, 1, ?, 1, 1)`, scriptPath, hostfiles.ComparisonKey(scriptPath), scriptDigest); err != nil {
		t.Fatal(err)
	}

	uploadSecret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"artifact"}, "label": {"Artifact upload"}, "action_type": {"upload"}, "enabled": {"1"},
		"upload_directory": {uploadRoot}, "upload_max_bytes": {"32"}, "upload_extensions": {".txt"}, "upload_conflict": {"reject"},
	})
	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	interfacesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	detailPattern := regexp.MustCompile(`/config/external-interfaces/entries/([A-Za-z0-9_-]+)"`)
	detailMatch := detailPattern.FindSubmatch(interfacesPage)
	if len(detailMatch) != 2 {
		t.Fatalf("entry detail link missing: %s", interfacesPage)
	}
	if !bytes.Contains(interfacesPage, []byte(`/config/external-interfaces/entries/`+string(detailMatch[1])+`/edit`)) {
		t.Fatalf("entry edit action should be rendered in the list: %s", interfacesPage)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces/entries/" + string(detailMatch[1]))
	if err != nil {
		t.Fatal(err)
	}
	entryDetail, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if bytes.Contains(entryDetail, []byte(`/config/external-interfaces/entries/`+string(detailMatch[1])+`/edit`)) {
		t.Fatalf("immutable capability exposes an edit action: %s", entryDetail)
	}
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "result.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("complete"))
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, serverURL+externalTriggerPath("legacy", "artifact"), &upload)
	request.Header.Set("Authorization", "Bearer "+uploadSecret)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("upload trigger status=%d body=%s", response.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(uploadRoot, "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("external upload bypassed private staging: %v", err)
	}
	var uploadResponse struct {
		Data struct {
			InboxID string `json:"inbox_id"`
			SHA256  string `json:"sha256"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &uploadResponse); err != nil || uploadResponse.Data.InboxID == "" || uploadResponse.Data.SHA256 == "" {
		t.Fatalf("invalid staged upload response: %s err=%v", body, err)
	}
	inboxResponse, err := client.Get(serverURL + "/resources/inbox")
	if err != nil {
		t.Fatal(err)
	}
	inboxPage, _ := io.ReadAll(inboxResponse.Body)
	_ = inboxResponse.Body.Close()
	rejectedPublish, err := client.PostForm(serverURL+"/resources/inbox/"+uploadResponse.Data.InboxID+"/publish", url.Values{
		"csrf_token": {formToken(t, inboxPage)}, "sha256": {strings.Repeat("0", 64)}, "confirm": {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = rejectedPublish.Body.Close()
	if rejectedPublish.StatusCode != http.StatusConflict {
		t.Fatalf("digest mismatch publish status=%d", rejectedPublish.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(uploadRoot, "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("digest mismatch still published content: %v", err)
	}
	published, err := client.PostForm(serverURL+"/resources/inbox/"+uploadResponse.Data.InboxID+"/publish", url.Values{
		"csrf_token": {formToken(t, inboxPage)}, "sha256": {uploadResponse.Data.SHA256}, "confirm": {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = published.Body.Close()
	if published.StatusCode != http.StatusSeeOther {
		t.Fatalf("publish staged upload status=%d", published.StatusCode)
	}
	if content, err := os.ReadFile(filepath.Join(uploadRoot, "result.txt")); err != nil || string(content) != "complete" {
		t.Fatalf("uploaded content=%q err=%v", content, err)
	}

	_, variableKeyID := createExternalTestKey(t, client, serverURL, "Variable agent")
	variableSecret := createExternalTestEntry(t, client, serverURL, variableKeyID, url.Values{
		"name": {"environment"}, "label": {"Deployment environment"}, "action_type": {"variable"}, "enabled": {"1"},
		"variable_name": {"environment"}, "variable_type": {"enum"}, "variable_options": {"staging\nproduction"},
	})
	response = invokeExternalForm(t, client, serverURL, variableSecret, "legacy", "environment", url.Values{"value": {"production"}})
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("variable trigger status=%d body=%s", response.StatusCode, body)
	}
	var value string
	if err := database.QueryRow("SELECT value FROM variables WHERE name = 'environment'").Scan(&value); err != nil || value != "production" {
		t.Fatalf("variable value=%q err=%v", value, err)
	}
	response = invokeExternalForm(t, client, serverURL, variableSecret, "legacy", "environment", url.Values{"value": {"root"}})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid enum status=%d", response.StatusCode)
	}

	_, quickKeyID := createExternalTestKey(t, client, serverURL, "Quick Run agent")
	quickSecret := createExternalTestEntry(t, client, serverURL, quickKeyID, url.Values{
		"name": {"quick"}, "label": {"External quick run"}, "action_type": {"quick_run"}, "enabled": {"1"}, "quick_run_id": {"external-quick"},
	})
	request, _ = http.NewRequest(http.MethodPost, serverURL+externalTriggerPath("legacy", "quick"), nil)
	request.Header.Set("Authorization", "Bearer "+quickSecret)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted || !bytes.Contains(body, []byte(`"run_id"`)) {
		t.Fatalf("quick run trigger status=%d body=%s", response.StatusCode, body)
	}
	var acceptedRuns int
	if err := database.QueryRow("SELECT count(*) FROM runs WHERE source_type = 'external/quick-run'").Scan(&acceptedRuns); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE quick_runs SET revision = revision + 1 WHERE id = 'external-quick'"); err != nil {
		t.Fatal(err)
	}
	response = invokeExternalForm(t, client, serverURL, quickSecret, "legacy", "quick", nil)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale Quick Run revision status=%d", response.StatusCode)
	}
	if _, err := database.Exec("UPDATE quick_runs SET revision = 1 WHERE id = 'external-quick'"); err != nil {
		t.Fatal(err)
	}
	changedScriptContent := scriptContent + "# changed\n"
	if runtime.GOOS == "windows" {
		changedScriptContent = scriptContent + "rem changed\r\n"
	}
	if err := os.WriteFile(scriptPath, []byte(changedScriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	response = invokeExternalForm(t, client, serverURL, quickSecret, "legacy", "quick", nil)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("changed Quick Run script status=%d", response.StatusCode)
	}
	var finalRuns int
	if err := database.QueryRow("SELECT count(*) FROM runs WHERE source_type = 'external/quick-run'").Scan(&finalRuns); err != nil {
		t.Fatal(err)
	}
	if finalRuns != acceptedRuns {
		t.Fatalf("rejected stale Quick Runs started work: before=%d after=%d", acceptedRuns, finalRuns)
	}
}

func openExternalTestDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	// Match the application's lock-wait policy when a test opens a second connection.
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	return database
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
	if response.StatusCode != http.StatusCreated || !externalKeyPattern.Match(created) || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create key status=%d body=%s", response.StatusCode, created)
	}
	return createdExternalTestKey(t, created)
}

func createExternalTestEntry(t *testing.T, client *http.Client, serverURL, keyID string, values url.Values) string {
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
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create entry status=%d body=%s", response.StatusCode, body)
	}
	secret, _ := createdExternalTestKey(t, body)
	return secret
}
