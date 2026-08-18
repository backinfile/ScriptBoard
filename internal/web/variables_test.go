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

func TestCreateTypedVariablePersistsItsValidatedType(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), stateRoot)
	response, err := client.Get(serverURL + "/resources/variables/new")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`data-variable-form`, `name="value_type" data-variable-value-type`, `value="text"`, `value="bool"`, `value="integer"`, `value="float"`, `value="version"`, `data-variable-value-field="text"`, `data-variable-value-field="bool"`, `data-variable-value-field="scalar"`} {
		if !strings.Contains(string(page), marker) {
			t.Fatalf("new Variable form is missing %q", marker)
		}
	}

	response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
		"name":       {"RETRIES"},
		"value":      {"12"},
		"value_type": {"integer"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status=%d", response.StatusCode)
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var value, valueType string
	if err := database.QueryRow(`SELECT value, value_type FROM variables WHERE name = 'RETRIES'`).Scan(&value, &valueType); err != nil {
		t.Fatal(err)
	}
	if value != "12" || valueType != "integer" {
		t.Fatalf("value=%q type=%q", value, valueType)
	}
	if _, err := database.Exec(`UPDATE variables SET updated_at = 1735689600 WHERE name = 'RETRIES'`); err != nil {
		t.Fatal(err)
	}

	response, err = client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, marker := range []string{
		`<th>Value type</th>`,
		`<td data-label="Value type" data-variable-type="integer">Integer</td>`,
		`<th>Revision</th>`,
		`<td data-label="Revision"><span class="revision-badge">v1</span></td>`,
		`<th>Last modified</th>`,
		`<time datetime="2025-01-01T00:00:00Z">`,
	} {
		if !strings.Contains(string(page), marker) {
			t.Fatalf("Variable list does not show metadata marker %q: %s", marker, page)
		}
	}
}

func TestCreateTypedVariableRejectsAValueWithTheWrongFormat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), stateRoot)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(serverURL + "/resources/variables/new")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
		"name":       {"RETRIES"},
		"value":      {"1.5"},
		"value_type": {"integer"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", response.StatusCode)
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM variables WHERE name = 'RETRIES'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid typed variable was persisted")
	}
}

func TestEditVariableChangesTypeOnlyWhenTheSubmittedValueMatches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), stateRoot)
	response, err := client.Get(serverURL + "/resources/variables/new")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
		"name": {"RELEASE_VERSION"}, "value": {"draft"}, "value_type": {"text"}, "csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	_, keyID := createExternalTestKey(t, client, serverURL, "Release Variable agent")
	_ = createExternalTestEntry(t, client, serverURL, keyID, url.Values{
		"name": {"release-version"}, "label": {"Release version"}, "action_type": {"variable"}, "enabled": {"1"},
		"variable_name": {"RELEASE_VERSION"}, "variable_type": {"text"}, "variable_max_length": {"128"}, "variable_allow_empty": {"1"},
	})

	response, err = client.Get(serverURL + "/resources/variables/RELEASE_VERSION/edit")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/variables/RELEASE_VERSION/update", url.Values{
		"name": {"RELEASE_VERSION"}, "value": {"2.4.1"}, "value_type": {"version"}, "csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status=%d", response.StatusCode)
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var value, valueType string
	var revision int64
	if err := database.QueryRow(`SELECT value, value_type, revision FROM variables WHERE name = 'RELEASE_VERSION'`).Scan(&value, &valueType, &revision); err != nil {
		t.Fatal(err)
	}
	if value != "2.4.1" || valueType != "version" || revision != 2 {
		t.Fatalf("value=%q type=%q revision=%d", value, valueType, revision)
	}

	response, err = client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(page), `<td data-label="Revision"><span class="revision-badge">v2</span></td>`) {
		t.Fatalf("Variable list does not show the updated revision: %s", page)
	}
}
