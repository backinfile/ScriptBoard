package web_test

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/externaltrigger"
)

func TestExternalSignedRequestRejectsUnsignedAndReplay(t *testing.T) {
	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	_, keyID := createExternalTestKey(t, client, serverURL, "Signed calls")
	logFile := filepath.Join(hostRoot, "signed.log")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"signed-log"}, "label": {"Signed log"}, "action_type": {"log"}, "enabled": {"1"},
		"require_signature": {"1"}, "log_file": {logFile}, "log_message_limit": {"1024"},
	})

	unsigned := invokeExternalForm(t, client, serverURL, secret, "legacy", "signed-log", url.Values{"message": {"unsigned"}})
	unsignedBody, _ := io.ReadAll(unsigned.Body)
	_ = unsigned.Body.Close()
	if unsigned.StatusCode != http.StatusUnauthorized || !strings.Contains(string(unsignedBody), `"error":"invalid_key"`) {
		t.Fatalf("unsigned status=%d body=%s", unsigned.StatusCode, unsignedBody)
	}

	timestamp := time.Now().UTC().Unix()
	requestURI := externalTriggerPath("legacy", "signed-log")
	values := url.Values{"message": {"signed once"}}
	contentType := "application/x-www-form-urlencoded"

	tamperedNonce := "nonce_body_tamper_12345"
	tampered, err := http.NewRequest(http.MethodPost, serverURL+requestURI, strings.NewReader(url.Values{"message": {"modified in transit"}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	tampered.Header.Set("Content-Type", contentType)
	tampered.Header.Set("Authorization", "Bearer "+secret)
	tampered.Header.Set("X-ScriptBoard-Timestamp", strconv.FormatInt(timestamp, 10))
	tampered.Header.Set("X-ScriptBoard-Nonce", tamperedNonce)
	tampered.Header.Set("X-ScriptBoard-Signature", externaltrigger.RequestSignature(secret, timestamp, tamperedNonce, http.MethodPost, requestURI, contentType, []byte(values.Encode())))
	tamperedResponse, err := client.Do(tampered)
	if err != nil {
		t.Fatal(err)
	}
	_ = tamperedResponse.Body.Close()
	if tamperedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered signed body status=%d", tamperedResponse.StatusCode)
	}

	nonce := "nonce_app_1234567890"
	request, err := http.NewRequest(http.MethodPost, serverURL+requestURI, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-ScriptBoard-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-ScriptBoard-Nonce", nonce)
	request.Header.Set("X-ScriptBoard-Signature", externaltrigger.RequestSignature(secret, timestamp, nonce, http.MethodPost, requestURI, contentType, []byte(values.Encode())))
	accepted, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = accepted.Body.Close()
	if accepted.StatusCode != http.StatusOK {
		t.Fatalf("signed status=%d", accepted.StatusCode)
	}

	replay, err := http.NewRequest(http.MethodPost, serverURL+requestURI, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	replay.Header = request.Header.Clone()
	replayed, err := client.Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	replayBody, _ := io.ReadAll(replayed.Body)
	_ = replayed.Body.Close()
	if replayed.StatusCode != http.StatusUnauthorized || !strings.Contains(string(replayBody), `"error":"invalid_key"`) {
		t.Fatalf("replay status=%d body=%s", replayed.StatusCode, replayBody)
	}
	content, err := os.ReadFile(logFile)
	if err != nil || strings.Count(string(content), "signed once") != 1 || strings.Contains(string(content), "unsigned") {
		t.Fatalf("signed action content=%q err=%v", content, err)
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var rejectedRequests, succeededRequests, rejectedAudits, correlatedAudits int
	if err := database.QueryRow(`SELECT COUNT(*) FROM external_trigger_requests WHERE entry_name='signed-log' AND result='rejected' AND http_status=401`).Scan(&rejectedRequests); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM external_trigger_requests WHERE entry_name='signed-log' AND result='succeeded' AND http_status=200`).Scan(&succeededRequests); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='external_trigger_log' AND result='rejected'`).Scan(&rejectedAudits); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events AS audit
		JOIN external_trigger_requests AS request ON request.id = audit.request_id
		WHERE audit.action='external_trigger_log' AND audit.authentication_assurance='external-capability'
		AND audit.target NOT LIKE '%request=%'`).Scan(&correlatedAudits); err != nil {
		t.Fatal(err)
	}
	if rejectedRequests != 3 || succeededRequests != 1 || rejectedAudits != 3 || correlatedAudits != 4 {
		t.Fatalf("rejected requests=%d succeeded requests=%d rejected audits=%d correlated audits=%d", rejectedRequests, succeededRequests, rejectedAudits, correlatedAudits)
	}
}

func TestExternalSignedRequestRejectsDuplicateProofHeadersBeforeAction(t *testing.T) {
	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	_, keyID := createExternalTestKey(t, client, serverURL, "Duplicate proof")
	logFile := filepath.Join(hostRoot, "duplicate-proof.log")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"duplicate-proof"}, "label": {"Duplicate proof"}, "action_type": {"log"}, "enabled": {"1"},
		"require_signature": {"1"}, "log_file": {logFile}, "log_message_limit": {"1024"},
	})

	timestamp := time.Now().UTC().Unix()
	nonce := "nonce_duplicate_proof_123"
	requestURI := externalTriggerPath("legacy", "duplicate-proof")
	contentType := "application/x-www-form-urlencoded"
	body := []byte("message=must-not-run")
	signature := externaltrigger.RequestSignature(secret, timestamp, nonce, http.MethodPost, requestURI, contentType, body)
	request, err := http.NewRequest(http.MethodPost, serverURL+requestURI, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-ScriptBoard-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-ScriptBoard-Nonce", nonce)
	request.Header.Add("X-ScriptBoard-Signature", signature)
	request.Header.Add("X-ScriptBoard-Signature", signature)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("duplicate signature status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if _, err := os.Stat(logFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("duplicate signature reached action: %v", err)
	}
}

func TestExternalKeyRevocationDuringSignedBodyStagingPreventsAction(t *testing.T) {
	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "host"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	_, keyID := createExternalTestKey(t, client, serverURL, "Revoke in flight")
	logFile := filepath.Join(hostRoot, "revoked-in-flight.log")
	secret := createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"revoke-in-flight"}, "label": {"Revoke in flight"}, "action_type": {"log"}, "enabled": {"1"},
		"require_signature": {"1"}, "log_file": {logFile}, "log_message_limit": {"1024"},
	})

	body := []byte("message=must-not-run-after-revocation")
	timestamp := time.Now().UTC().Unix()
	nonce := "nonce_revoke_in_flight_123"
	requestURI := externalTriggerPath("legacy", "revoke-in-flight")
	contentType := "application/x-www-form-urlencoded"
	reader, writer := io.Pipe()
	request, err := http.NewRequest(http.MethodPost, serverURL+requestURI, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = int64(len(body))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-ScriptBoard-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-ScriptBoard-Nonce", nonce)
	request.Header.Set("X-ScriptBoard-Signature", externaltrigger.RequestSignature(secret, timestamp, nonce, http.MethodPost, requestURI, contentType, body))
	type requestResult struct {
		response *http.Response
		err      error
	}
	result := make(chan requestResult, 1)
	go func() {
		response, requestErr := client.Do(request)
		result <- requestResult{response: response, err: requestErr}
	}()
	if _, err := writer.Write(body[:8]); err != nil {
		t.Fatal(err)
	}
	stagingDirectory := filepath.Join(stateRoot, "external-requests", "signed-bodies")
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, readErr := os.ReadDir(stagingDirectory)
		if readErr == nil && len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("signed request did not enter staging: %v", readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE external_trigger_keys SET enabled = 0 WHERE id = ?`, keyID); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	_ = database.Close()
	if _, err := writer.Write(body[8:]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	completed := <-result
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	defer completed.response.Body.Close()
	if completed.response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked in-flight key status = %d, want %d", completed.response.StatusCode, http.StatusUnauthorized)
	}
	if _, err := os.Stat(logFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked in-flight key reached action: %v", err)
	}
}
