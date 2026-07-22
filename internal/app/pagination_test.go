package app_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestVariableListUsesServerSidePagination(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/variables")
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
		response, err = client.PostForm(serverURL+"/variables", url.Values{
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

	response, err = client.Get(serverURL + "/variables")
	if err != nil {
		t.Fatalf("get first variable page: %v", err)
	}
	firstPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	firstBody := string(firstPage)
	if !strings.Contains(firstBody, "共 21 条 · 第 1 / 2 页") || !strings.Contains(firstBody, `value="VAR_00"`) {
		t.Fatalf("first page is missing pagination metadata or first row: %s", firstBody)
	}
	if strings.Contains(firstBody, `value="VAR_20"`) {
		t.Fatalf("first page contains a row from the second page: %s", firstBody)
	}

	response, err = client.Get(serverURL + "/variables?page=2")
	if err != nil {
		t.Fatalf("get second variable page: %v", err)
	}
	secondPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	secondBody := string(secondPage)
	if !strings.Contains(secondBody, "共 21 条 · 第 2 / 2 页") || !strings.Contains(secondBody, `value="VAR_20"`) {
		t.Fatalf("second page is missing pagination metadata or final row: %s", secondBody)
	}
	if strings.Contains(secondBody, `value="VAR_00"`) {
		t.Fatalf("second page contains a row from the first page: %s", secondBody)
	}
}
