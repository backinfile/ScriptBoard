package web_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestVariableListLoadsAllRecordsForGrouping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatalf("get variables: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read variables page: %v", err)
	}
	csrfToken := formToken(t, page)

	for index := 0; index < 21; index++ {
		name := fmt.Sprintf("VAR_%02d", index)
		response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
			"name":       {name},
			"value":      {fmt.Sprintf("value-%02d", index)},
			"csrf_token": {csrfToken},
		})
		if err != nil {
			t.Fatalf("create variable %s: %v", name, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("create variable %s status = %d", name, response.StatusCode)
		}
	}

	response, err = client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatalf("get first variable page: %v", err)
	}
	firstPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	firstBody := string(firstPage)
	if !strings.Contains(firstBody, `data-grouped-records="variable-groups"`) || !strings.Contains(firstBody, `>VAR_00</code>`) || !strings.Contains(firstBody, `>VAR_20</code>`) {
		t.Fatalf("grouped variable list is missing its grouping shell or records: %s", firstBody)
	}
	if strings.Contains(firstBody, "records · 1 / 2") {
		t.Fatalf("grouped variable list unexpectedly renders pagination: %s", firstBody)
	}
}
