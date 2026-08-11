package app_test

import (
	"database/sql"
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

	unsigned := invokeExternalForm(t, client, serverURL, secret, "signed-log", url.Values{"message": {"unsigned"}})
	unsignedBody, _ := io.ReadAll(unsigned.Body)
	_ = unsigned.Body.Close()
	if unsigned.StatusCode != http.StatusUnauthorized || !strings.Contains(string(unsignedBody), `"error":"invalid_key"`) {
		t.Fatalf("unsigned status=%d body=%s", unsigned.StatusCode, unsignedBody)
	}

	timestamp := time.Now().UTC().Unix()
	nonce := "nonce_app_1234567890"
	requestURI := "/trigger?name=signed-log"
	values := url.Values{"message": {"signed once"}}
	request, err := http.NewRequest(http.MethodPost, serverURL+requestURI, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-ScriptBoard-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-ScriptBoard-Nonce", nonce)
	request.Header.Set("X-ScriptBoard-Signature", externaltrigger.RequestSignature(secret, timestamp, nonce, http.MethodPost, requestURI))
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
	var rejectedRequests, succeededRequests, rejectedAudits int
	if err := database.QueryRow(`SELECT COUNT(*) FROM external_trigger_requests WHERE entry_name='signed-log' AND result='rejected' AND http_status=401`).Scan(&rejectedRequests); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM external_trigger_requests WHERE entry_name='signed-log' AND result='succeeded' AND http_status=200`).Scan(&succeededRequests); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='external_trigger_log' AND result='rejected'`).Scan(&rejectedAudits); err != nil {
		t.Fatal(err)
	}
	if rejectedRequests != 2 || succeededRequests != 1 || rejectedAudits != 2 {
		t.Fatalf("rejected requests=%d succeeded requests=%d rejected audits=%d", rejectedRequests, succeededRequests, rejectedAudits)
	}
}
