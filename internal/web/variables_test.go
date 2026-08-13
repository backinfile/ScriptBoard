package web_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordVariableIsStoredNormallyAndMaskedByDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), stateRoot)
	response, err := client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatalf("get variables: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read variables page: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
		"name":        {"API_TOKEN"},
		"value":       {"plain-secret-value"},
		"is_password": {"1"},
		"csrf_token":  {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("create password variable: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create password variable status = %d", response.StatusCode)
	}

	db, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	var isPassword bool
	if err := db.QueryRow(`SELECT value, is_password FROM variables WHERE name = 'API_TOKEN'`).Scan(&value, &isPassword); err != nil {
		t.Fatal(err)
	}
	if value != "plain-secret-value" || !isPassword {
		t.Fatalf("value=%q is_password=%v", value, isPassword)
	}

	response, err = client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatalf("get variables after create: %v", err)
	}
	page, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read variables after create: %v", err)
	}
	html := string(page)
	for _, marker := range []string{
		`<small>Password type</small>`,
		`id="variable-name-0">API_TOKEN</code>`,
		`data-copy-text data-copy-name data-copy-target="variable-name-0"`,
		`aria-label="Copy variable name API_TOKEN"`,
		`class="secret-value" data-password-value data-copy-field hidden`,
		`data-password-mask aria-label="Variable value hidden">••••••••</span>`,
		`id="variable-secret-0" class="secret-content" data-password-content hidden>plain-secret-value</code>`,
		`data-toggle-password aria-controls="variable-secret-0" aria-expanded="false"`,
		`data-tooltip="Show variable value"`,
		`data-lucide="eye"`,
		`data-copy-text data-copy-password data-copy-target="variable-secret-0"`,
		`<noscript><details class="no-js-secret">`,
		`data-no-js-show>Show variable value</span>`,
		`data-no-js-hide>Hide variable value</span>`,
		`class="no-js-secret__mask" aria-label="Variable value hidden">••••••••</span>`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("password variable page is missing %q: %s", marker, html)
		}
	}
	maskPosition := strings.Index(html, `data-password-mask`)
	contentPosition := strings.Index(html, `data-password-content`)
	controlPosition := strings.Index(html, `class="secret-controls"`)
	if maskPosition < 0 || contentPosition < 0 || controlPosition < 0 ||
		maskPosition >= contentPosition || contentPosition >= controlPosition {
		t.Fatalf("password controls are not rendered after the value: %s", html)
	}

	response, err = client.PostForm(serverURL+"/resources/variables/API_TOKEN/update", url.Values{
		"name":       {"API_TOKEN"},
		"value":      {"updated-plain-value"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("change password variable to normal type: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update password variable status = %d", response.StatusCode)
	}
	if err := db.QueryRow(`SELECT value, is_password FROM variables WHERE name = 'API_TOKEN'`).Scan(&value, &isPassword); err != nil {
		t.Fatal(err)
	}
	if value != "updated-plain-value" || isPassword {
		t.Fatalf("updated value=%q is_password=%v", value, isPassword)
	}

	response, err = client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatalf("get normal variable: %v", err)
	}
	page, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read normal variable page: %v", err)
	}
	html = string(page)
	for _, marker := range []string{
		`id="variable-name-0">API_TOKEN</code>`,
		`data-copy-text data-copy-name data-copy-target="variable-name-0"`,
		`id="variable-value-0" class="value-preview">updated-plain-value</pre>`,
		`data-copy-text data-copy-value data-copy-target="variable-value-0"`,
		`aria-label="Copy variable value API_TOKEN"`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("normal variable page is missing %q: %s", marker, html)
		}
	}
}
