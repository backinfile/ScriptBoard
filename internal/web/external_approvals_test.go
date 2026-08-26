package web_test

import (
	"bytes"
	"crypto/sha256"
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

	"scriptboard/internal/hostfiles"
)

func TestExternalVariableWaitsForApprovalAndExecutesOnlyOnce(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), stateRoot)
	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	if _, err := database.Exec(`INSERT INTO variables(name, value, is_password, created_at, updated_at) VALUES ('environment', 'staging', 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	_, keyID := createExternalTestKey(t, client, serverURL, "Approval agent")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"environment"}, "label": {"Deployment environment"}, "action_type": {"variable"}, "enabled": {"1"}, "require_approval": {"1"},
		"variable_name": {"environment"}, "variable_type": {"enum"}, "variable_options": {"staging\nproduction"},
	})
	response := invokeExternalForm(t, client, serverURL, secret, "legacy", "environment", url.Values{"value": {"production"}})
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted || !strings.Contains(string(body), `"state":"pending_approval"`) {
		t.Fatalf("approval trigger status=%d body=%s", response.StatusCode, body)
	}
	var value string
	if err := database.QueryRow(`SELECT value FROM variables WHERE name = 'environment'`).Scan(&value); err != nil || value != "staging" {
		t.Fatalf("variable changed before approval: value=%q error=%v", value, err)
	}
	pageResponse, err := client.Get(serverURL + "/config/external-interfaces?tab=approvals")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	match := regexp.MustCompile(`data-external-approval-id="([A-Za-z0-9_-]+)"`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("approval row missing: %s", page)
	}
	approvalID := string(match[1])
	approved, err := client.PostForm(serverURL+"/config/external-interfaces/approvals/"+approvalID+"/approve", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = approved.Body.Close()
	if approved.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve status=%d", approved.StatusCode)
	}
	if err := database.QueryRow(`SELECT value FROM variables WHERE name = 'environment'`).Scan(&value); err != nil || value != "production" {
		t.Fatalf("approved variable value=%q error=%v", value, err)
	}
	again, err := client.PostForm(serverURL+"/config/external-interfaces/approvals/"+approvalID+"/approve", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Fatalf("second approval status=%d, want conflict", again.StatusCode)
	}
}

func TestExternalUploadIsCachedUntilApprovalThenWrittenToTarget(t *testing.T) {
	root := t.TempDir()
	stateRoot, uploadRoot := filepath.Join(root, "state"), filepath.Join(root, "uploads")
	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), stateRoot)
	_, keyID := createExternalTestKey(t, client, serverURL, "Upload approval agent")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"artifact"}, "label": {"Artifact"}, "action_type": {"upload"}, "enabled": {"1"}, "require_approval": {"1"},
		"upload_directory": {uploadRoot}, "upload_max_bytes": {"64"}, "upload_extensions": {".txt"}, "upload_conflict": {"reject"},
	})
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, _ := writer.CreateFormFile("file", "result.txt")
	_, _ = part.Write([]byte("approved upload"))
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, serverURL+externalTriggerPath("legacy", "artifact"), &upload)
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted || !strings.Contains(string(body), `"state":"pending_approval"`) {
		t.Fatalf("upload approval status=%d body=%s", response.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(uploadRoot, "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("upload reached target before approval: %v", err)
	}
	pageResponse, _ := client.Get(serverURL + "/config/external-interfaces?tab=approvals")
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	match := regexp.MustCompile(`data-external-approval-id="([A-Za-z0-9_-]+)"`).FindSubmatch(page)
	if len(match) != 2 || !bytes.Contains(page, []byte("result.txt")) {
		t.Fatalf("cached upload approval missing: %s", page)
	}
	approved, err := client.PostForm(serverURL+"/config/external-interfaces/approvals/"+string(match[1])+"/approve", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = approved.Body.Close()
	content, readErr := os.ReadFile(filepath.Join(uploadRoot, "result.txt"))
	if approved.StatusCode != http.StatusSeeOther || readErr != nil || string(content) != "approved upload" {
		t.Fatalf("approved upload status=%d content=%q error=%v", approved.StatusCode, content, readErr)
	}
}

func TestExternalUploadRejectionKeepsTargetUntouchedAndDeletesCache(t *testing.T) {
	root := t.TempDir()
	stateRoot, uploadRoot := filepath.Join(root, "state"), filepath.Join(root, "uploads")
	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), stateRoot)
	_, keyID := createExternalTestKey(t, client, serverURL, "Rejected upload agent")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"artifact-reject"}, "label": {"Rejected artifact"}, "action_type": {"upload"}, "enabled": {"1"}, "require_approval": {"1"},
		"upload_directory": {uploadRoot}, "upload_max_bytes": {"64"}, "upload_extensions": {".txt"}, "upload_conflict": {"reject"},
	})
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, _ := writer.CreateFormFile("file", "rejected.txt")
	_, _ = part.Write([]byte("do not publish"))
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, serverURL+externalTriggerPath("legacy", "artifact-reject"), &upload)
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	pageResponse, _ := client.Get(serverURL + "/config/external-interfaces?tab=approvals")
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	match := regexp.MustCompile(`data-external-approval-id="([A-Za-z0-9_-]+)"`).FindSubmatch(page)
	if response.StatusCode != http.StatusAccepted || len(match) != 2 || !bytes.Contains(page, []byte("SHA-256")) {
		t.Fatalf("rejection approval was not staged: status=%d page=%s", response.StatusCode, page)
	}
	approvalID := string(match[1])
	withoutCSRF, err := client.PostForm(serverURL+"/config/external-interfaces/approvals/"+approvalID+"/reject", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	_ = withoutCSRF.Body.Close()
	if withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("reject without CSRF status=%d", withoutCSRF.StatusCode)
	}
	rejected, err := client.PostForm(serverURL+"/config/external-interfaces/approvals/"+approvalID+"/reject", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = rejected.Body.Close()
	if rejected.StatusCode != http.StatusSeeOther {
		t.Fatalf("reject status=%d", rejected.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(uploadRoot, "rejected.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejected upload reached target: %v", err)
	}
	staged, err := os.ReadDir(filepath.Join(stateRoot, "approvals", "uploads"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("rejected upload cache entries=%d error=%v", len(staged), err)
	}
	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	var approvalStatus, invocationResult string
	if err := database.QueryRow(`SELECT status FROM external_trigger_approvals WHERE id = ?`, approvalID).Scan(&approvalStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT result FROM external_trigger_requests WHERE id = ?`, approvalID).Scan(&invocationResult); err != nil {
		t.Fatal(err)
	}
	if approvalStatus != "rejected" || invocationResult != "rejected" {
		t.Fatalf("rejected states approval=%q invocation=%q", approvalStatus, invocationResult)
	}
}

func TestRetiredUploadInboxRoutesAreUnavailable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	for _, path := range []string{"/resources/inbox", "/resources/inbox/file/retired"} {
		response, err := client.Get(serverURL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("retired route %s status=%d, want 404", path, response.StatusCode)
		}
	}
}

func TestExternalLogWaitsForApproval(t *testing.T) {
	root := t.TempDir()
	stateRoot, logPath := filepath.Join(root, "state"), filepath.Join(root, "approved.log")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), stateRoot)
	_, keyID := createExternalTestKey(t, client, serverURL, "Log approval agent")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"notice"}, "label": {"Notice"}, "action_type": {"log"}, "enabled": {"1"}, "require_approval": {"1"},
		"log_target_mode": {"custom"}, "log_file": {logPath}, "log_message_limit": {"128"},
	})
	response := invokeExternalForm(t, client, serverURL, secret, "legacy", "notice", url.Values{"message": {"deploy complete"}})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("log approval status=%d", response.StatusCode)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("log was written before approval: %v", err)
	}
	pageResponse, _ := client.Get(serverURL + "/config/external-interfaces?tab=approvals")
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	match := regexp.MustCompile(`data-external-approval-id="([A-Za-z0-9_-]+)"`).FindSubmatch(page)
	if len(match) != 2 || !bytes.Contains(page, []byte("deploy complete")) {
		t.Fatalf("log approval missing: %s", page)
	}
	approved, err := client.PostForm(serverURL+"/config/external-interfaces/approvals/"+string(match[1])+"/approve", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = approved.Body.Close()
	content, readErr := os.ReadFile(logPath)
	if approved.StatusCode != http.StatusSeeOther || readErr != nil || !strings.Contains(string(content), "deploy complete") {
		t.Fatalf("approved log status=%d content=%q error=%v", approved.StatusCode, content, readErr)
	}
}

func TestExternalQuickRunWaitsForApproval(t *testing.T) {
	root := t.TempDir()
	stateRoot, hostRoot := filepath.Join(root, "state"), filepath.Join(root, "host")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	scriptName, scriptContent := "approved.sh", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "approved.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(scriptContent)))
	if _, err := database.Exec(`INSERT INTO quick_runs(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at, locked, script_sha256, revision, updated_at)
		VALUES ('approved-quick', 'Approved quick', ?, ?, '', 30, 1, 1, 0, ?, 1, 1)`, scriptPath, hostfiles.ComparisonKey(scriptPath), digest); err != nil {
		t.Fatal(err)
	}
	_, keyID := createExternalTestKey(t, client, serverURL, "Quick approval agent")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"deploy"}, "label": {"Deploy"}, "action_type": {"quick_run"}, "enabled": {"1"}, "require_approval": {"1"}, "quick_run_id": {"approved-quick"},
	})
	request, _ := http.NewRequest(http.MethodPost, serverURL+externalTriggerPath("legacy", "deploy"), http.NoBody)
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	var runCount int
	if response.StatusCode != http.StatusAccepted || database.QueryRow(`SELECT COUNT(*) FROM runs WHERE source_type = 'external/quick-run'`).Scan(&runCount) != nil || runCount != 0 {
		t.Fatalf("quick run started before approval: status=%d runs=%d", response.StatusCode, runCount)
	}
	pageResponse, _ := client.Get(serverURL + "/config/external-interfaces?tab=approvals")
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	match := regexp.MustCompile(`data-external-approval-id="([A-Za-z0-9_-]+)"`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("quick run approval missing: %s", page)
	}
	approved, err := client.PostForm(serverURL+"/config/external-interfaces/approvals/"+string(match[1])+"/approve", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = approved.Body.Close()
	if err := database.QueryRow(`SELECT COUNT(*) FROM runs WHERE source_type = 'external/quick-run'`).Scan(&runCount); approved.StatusCode != http.StatusSeeOther || err != nil || runCount != 1 {
		t.Fatalf("approved quick run status=%d runs=%d error=%v", approved.StatusCode, runCount, err)
	}
}
