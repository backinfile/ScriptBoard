package app_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalGlobalControlFailsClosedAndCanResume(t *testing.T) {
	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	_, keyID := createExternalTestKey(t, client, serverURL, "Emergency control")
	logFile := filepath.Join(hostRoot, "controlled.log")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"controlled-log"}, "label": {"Controlled log"}, "action_type": {"log"}, "enabled": {"1"},
		"log_file": {logFile}, "log_message_limit": {"1024"},
	})

	pageResponse, err := client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	if !strings.Contains(string(page), `data-external-global-control="enabled"`) {
		t.Fatalf("enabled global control is not visible: %s", page)
	}
	withoutCSRF, err := client.PostForm(serverURL+"/config/external-interfaces/control", url.Values{"enabled": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = withoutCSRF.Body.Close()
	if withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("global control without CSRF status=%d", withoutCSRF.StatusCode)
	}
	invalid, err := client.PostForm(serverURL+"/config/external-interfaces/control", url.Values{
		"csrf_token": {formToken(t, page)}, "enabled": {"sometimes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid global control status=%d", invalid.StatusCode)
	}
	disabled, err := client.PostForm(serverURL+"/config/external-interfaces/control", url.Values{
		"csrf_token": {formToken(t, page)}, "enabled": {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = disabled.Body.Close()
	if disabled.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable global control status=%d", disabled.StatusCode)
	}

	response := invokeExternalForm(t, client, serverURL, secret, "controlled-log", url.Values{"message": {"must not be written"}})
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || response.StatusCode != http.StatusServiceUnavailable || payload.Error != "unavailable" {
		t.Fatalf("disabled trigger status=%d body=%s err=%v", response.StatusCode, body, err)
	}
	if content, err := os.ReadFile(logFile); err == nil && strings.Contains(string(content), "must not be written") {
		t.Fatalf("disabled trigger performed its action: %s", content)
	}

	pageResponse, err = client.Get(serverURL + "/config/external-interfaces")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	if !strings.Contains(string(page), `data-external-global-control="disabled"`) {
		t.Fatalf("disabled global control is not visible: %s", page)
	}
	enabled, err := client.PostForm(serverURL+"/config/external-interfaces/control", url.Values{
		"csrf_token": {formToken(t, page)}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = enabled.Body.Close()
	if enabled.StatusCode != http.StatusSeeOther {
		t.Fatalf("enable global control status=%d", enabled.StatusCode)
	}
	response = invokeExternalForm(t, client, serverURL, secret, "controlled-log", url.Values{"message": {"resumed"}})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resumed trigger status=%d", response.StatusCode)
	}
	if content, err := os.ReadFile(logFile); err != nil || !strings.Contains(string(content), "resumed") {
		t.Fatalf("resumed trigger content=%q err=%v", content, err)
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var controlEnabled, auditEvents, rejectedInvocations int
	if err := database.QueryRow("SELECT enabled FROM external_trigger_control WHERE id = 1").Scan(&controlEnabled); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'set_external_interface_global_control' AND result = 'succeeded'").Scan(&auditEvents); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM external_trigger_requests WHERE entry_name = 'controlled-log' AND result = 'rejected' AND http_status = 503").Scan(&rejectedInvocations); err != nil {
		t.Fatal(err)
	}
	if controlEnabled != 1 || auditEvents != 2 || rejectedInvocations != 1 {
		t.Fatalf("control=%d audit=%d rejected=%d", controlEnabled, auditEvents, rejectedInvocations)
	}

	if _, err := database.Exec("DROP TABLE external_trigger_control"); err != nil {
		t.Fatal(err)
	}
	response = invokeExternalForm(t, client, serverURL, secret, "controlled-log", url.Values{"message": {"must fail closed"}})
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err := json.Unmarshal(body, &payload); err != nil || response.StatusCode != http.StatusServiceUnavailable || payload.Error != "unavailable" {
		t.Fatalf("control read failure status=%d body=%s err=%v", response.StatusCode, body, err)
	}
	if content, err := os.ReadFile(logFile); err != nil || strings.Contains(string(content), "must fail closed") {
		t.Fatalf("control read failure did not fail closed: content=%q err=%v", content, err)
	}
}
