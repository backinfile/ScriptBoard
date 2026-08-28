package web_test

import (
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedGroupCatalogAppearsAcrossGroupedPages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/groups/new?return_to=%2Fconfig%2Fquick-runs")
	if err != nil {
		t.Fatal(err)
	}
	task, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/groups?return_to=%2Fconfig%2Fquick-runs", url.Values{
		"csrf_token": {formToken(t, task)},
		"name":       {"Shared operations"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create shared group status=%d", response.StatusCode)
	}

	for _, path := range []string{"/config/quick-runs", "/config/schedules", "/resources/variables", "/monitor/websites"} {
		response, err = client.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if !strings.Contains(string(body), `data-group-name="Shared operations"`) {
			t.Fatalf("%s does not render the shared empty group: %s", path, body)
		}
	}

	response, err = client.Get(serverURL + "/resources/files/quick-access")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), `"Name":"Shared operations"`) {
		t.Fatalf("file Quick access API does not expose the shared group catalog: %s", body)
	}
}
