package app_test

import (
	"bytes"
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
var externalCopyURLPattern = regexp.MustCompile(`/config/external-interfaces/keys/([A-Za-z0-9_-]{16})/copy`)

func createdExternalKeyID(t *testing.T, body []byte) string {
	t.Helper()
	match := externalCopyURLPattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("created key response is missing its copy action: %s", body)
	}
	return string(match[1])
}

func copyExternalTestKey(t *testing.T, client *http.Client, serverURL, keyID string) string {
	t.Helper()
	response, err := client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/copy")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]string
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !externalKeyPattern.MatchString(payload["key"]) {
		t.Fatalf("copy key status=%d key=%q", response.StatusCode, payload["key"])
	}
	return payload["key"]
}

func invokeExternalForm(t *testing.T, client *http.Client, serverURL, secret, name string, values url.Values) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, serverURL+"/trigger?name="+url.QueryEscape(name), strings.NewReader(values.Encode()))
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

func TestViewerCannotCopyExternalInterfaceKey(t *testing.T) {
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
	keyID := createdExternalKeyID(t, createdBody)
	viewer := createRoleUserClient(t, admin, serverURL, "external-key-viewer", "viewer")
	response, err := viewer.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/copy")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("viewer copy status=%d body=%s", response.StatusCode, body)
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
	for _, expected := range []string{`class="external-interface-tabs"`, `href="/config/external-interfaces" aria-current="page"`, `href="/config/external-interfaces?tab=activity"`, `>Interfaces<`} {
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
	keyID := createdExternalKeyID(t, createdBody)
	if response.StatusCode != http.StatusCreated || !strings.Contains(string(createdBody), `data-task-kind="external-key-created"`) ||
		!strings.Contains(string(createdBody), `data-task-refresh-on-close`) ||
		!strings.Contains(string(createdBody), `data-copy-key-url="/config/external-interfaces/keys/`+keyID+`/copy"`) ||
		externalKeyPattern.Match(createdBody) || strings.Contains(string(createdBody), `class="task-back"`) {
		t.Fatalf("create key should render a masked in-drawer result: status=%d body=%s", response.StatusCode, createdBody)
	}
	secret := copyExternalTestKey(t, client, serverURL, keyID)
	expectedCreatedHint := "sbk_" + keyID + ".••••" + secret[len(secret)-4:]
	if !strings.Contains(string(createdBody), expectedCreatedHint) {
		t.Fatalf("created key response missing masked key %q: %s", expectedCreatedHint, createdBody)
	}

	response, err = client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/copy")
	if err != nil {
		t.Fatal(err)
	}
	copyBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	var copiedKey map[string]string
	if err := json.Unmarshal(copyBody, &copiedKey); err != nil {
		t.Fatalf("decode copied key: %v body=%s", err, copyBody)
	}
	if response.StatusCode != http.StatusOK || copiedKey["key"] != secret || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("copy key status=%d body=%s", response.StatusCode, copyBody)
	}

	response, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	keyList, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	copyURL := "/config/external-interfaces/keys/" + keyID + "/copy"
	if !strings.Contains(string(keyList), `data-copy-key-url="`+copyURL+`"`) || strings.Contains(string(keyList), `href="`+copyURL+`"`) {
		t.Fatalf("copy key should be an inline copy button: %s", keyList)
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
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create entry status=%d", response.StatusCode)
	}

	queryRequest, _ := http.NewRequest(http.MethodPost, serverURL+"/trigger?key="+url.QueryEscape(secret)+"&name=deployment-log", strings.NewReader(url.Values{"message": {"must not be logged"}}.Encode()))
	queryRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	queryResponse, err := client.Do(queryRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = queryResponse.Body.Close()
	if queryResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query-string key status=%d, want 401", queryResponse.StatusCode)
	}

	response = invokeExternalForm(t, client, serverURL, secret, "deployment-log", url.Values{"message": {"deployment finished"}})
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
	for _, action := range []string{`href="` + previewURL + `"`, `/config/external-interfaces/entries/` + entryID + `/edit`, `/config/external-interfaces/entries/` + entryID + `/toggle`, `/config/external-interfaces/entries/` + entryID + `/delete`} {
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
		!strings.Contains(string(detail), "POST /trigger?name=deployment-log") ||
		!strings.Contains(string(detail), "Authorization: Bearer YOUR_KEY") || !strings.Contains(string(detail), "message") ||
		!strings.Contains(string(detail), `href="`+previewURL+`"`) || !strings.Contains(string(detail), `/entries/`+entryID+`/edit`) ||
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

func TestRotatedExternalKeyIsCopiedWithoutRenderingTheSecret(t *testing.T) {
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
	if response.StatusCode != http.StatusCreated || !strings.Contains(string(rotatedPage), `data-task-kind="external-key-rotated"`) ||
		!strings.Contains(string(rotatedPage), `data-task-refresh-on-close`) ||
		!strings.Contains(string(rotatedPage), `data-copy-key-url="/config/external-interfaces/keys/`+keyID+`/copy"`) || externalKeyPattern.Match(rotatedPage) {
		t.Fatalf("rotated key response exposes the secret or lacks the copy action: status=%d body=%s", response.StatusCode, rotatedPage)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces/keys/" + keyID + "/copy")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if payload["key"] == "" || payload["key"] == original {
		t.Fatalf("rotated key was not replaced: %q", payload["key"])
	}
	expectedRotatedHint := "sbk_" + keyID + ".••••" + payload["key"][len(payload["key"])-4:]
	if !strings.Contains(string(rotatedPage), expectedRotatedHint) {
		t.Fatalf("rotated key response missing masked key %q: %s", expectedRotatedHint, rotatedPage)
	}
}

func TestExternalInvocationHistorySupportsSearchDateFiltersAndPagination(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), stateRoot)
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now()
	for index := 0; index < 21; index++ {
		_, err = database.Exec(`INSERT INTO external_trigger_requests
			(id, occurred_at, key_id, key_label, entry_id, entry_name, action_type, result, http_status, message)
			VALUES (?, ?, 'key', 'Release service', 'entry', ?, 'log', 'succeeded', 200, ?)`,
			fmt.Sprintf("request-%02d", index), now.Unix(), fmt.Sprintf("call-%02d", index), fmt.Sprintf("message-%02d", index))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = database.Exec(`INSERT INTO external_trigger_requests
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
	detailPattern := regexp.MustCompile(`/config/external-interfaces/entries/([A-Za-z0-9_-]+)"`)
	detailMatch := detailPattern.FindSubmatch(interfacesPage)
	if len(detailMatch) != 2 {
		t.Fatalf("entry detail link missing: %s", interfacesPage)
	}
	if bytes.Contains(interfacesPage, []byte(`/config/external-interfaces/entries/`+string(detailMatch[1])+`/edit`)) {
		t.Fatalf("entry edit action should not be rendered in the list: %s", interfacesPage)
	}
	response, err = client.Get(serverURL + "/config/external-interfaces/entries/" + string(detailMatch[1]))
	if err != nil {
		t.Fatal(err)
	}
	entryDetail, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	editPattern := regexp.MustCompile(`/config/external-interfaces/entries/([A-Za-z0-9_-]+)/edit`)
	editMatch := editPattern.FindSubmatch(entryDetail)
	if len(editMatch) != 2 {
		t.Fatalf("entry edit action missing from detail drawer: %s", entryDetail)
	}
	response, err = client.Get(serverURL + string(editMatch[0]))
	if err != nil {
		t.Fatal(err)
	}
	entryEdit, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/external-interfaces/entries/"+string(detailMatch[1]), url.Values{
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
	request, _ := http.NewRequest(http.MethodPost, serverURL+"/trigger?name=artifact", &upload)
	request.Header.Set("Authorization", "Bearer "+secret)
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
	response = invokeExternalForm(t, client, serverURL, secret, "environment", url.Values{"value": {"production"}})
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("variable trigger status=%d body=%s", response.StatusCode, body)
	}
	var value string
	if err := database.QueryRow("SELECT value FROM variables WHERE name = 'environment'").Scan(&value); err != nil || value != "production" {
		t.Fatalf("variable value=%q err=%v", value, err)
	}
	response = invokeExternalForm(t, client, serverURL, secret, "environment", url.Values{"value": {"root"}})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid enum status=%d", response.StatusCode)
	}

	createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"quick"}, "label": {"External quick run"}, "action_type": {"quick_run"}, "enabled": {"1"}, "quick_run_id": {"external-quick"},
	})
	request, _ = http.NewRequest(http.MethodPost, serverURL+"/trigger?name=quick", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
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
	if response.StatusCode != http.StatusCreated || externalKeyPattern.Match(created) {
		t.Fatalf("create key status=%d body=%s", response.StatusCode, created)
	}
	keyID := createdExternalKeyID(t, created)
	return copyExternalTestKey(t, client, serverURL, keyID), keyID
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
