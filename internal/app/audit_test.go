package app_test

import (
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditRecordsActionWithoutVariableValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/variables")
	if err != nil {
		t.Fatalf("get variables: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	const sensitiveValue = "must-not-appear-in-audit"
	response, err = client.PostForm(serverURL+"/variables", url.Values{
		"name":       {"AUDITED"},
		"value":      {sensitiveValue},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("create variable: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/audit")
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "create_variable") || !strings.Contains(string(body), "AUDITED") {
		t.Fatalf("audit event missing: status=%d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), sensitiveValue) {
		t.Fatalf("variable value leaked into audit: %s", body)
	}
}
