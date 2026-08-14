package web_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/kubeconfigmanager"
	app "scriptboard/internal/web"
)

const localKubeconfigFixture = `apiVersion: v1
kind: Config
clusters:
- name: production
  cluster:
    server: https://production.example.test
- name: staging
  cluster:
    server: https://staging.example.test
users:
- name: admin
  user: {}
- name: developer
  user: {}
contexts:
- name: production-admin
  context:
    cluster: production
    user: admin
    namespace: default
- name: staging-dev
  context:
    cluster: staging
    user: developer
    namespace: preview
current-context: production-admin
`

func TestKubernetesLocalManagementPageAndContextActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(localKubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})

	response, err := client.Get(serverURL + "/monitor/kubernetes?tab=local")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`data-kubernetes-tab="local"`, `data-kubernetes-local`, `data-kubernetes-import-drawer`, `production-admin`, `staging-dev`, `/monitor/kubernetes/local/download`} {
		if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte(expected)) {
			t.Fatalf("local page missing %q: status=%d body=%s", expected, response.StatusCode, page)
		}
	}

	response, err = client.Get(serverURL + "/monitor/kubernetes/local/contexts/staging-dev/download?path=" + url.QueryEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Disposition"), "attachment") || !bytes.Contains(exported, []byte("current-context: staging-dev")) || bytes.Contains(exported, []byte("production-admin")) {
		t.Fatalf("context download status=%d headers=%v body=%s", response.StatusCode, response.Header, exported)
	}

	response, err = client.PostForm(serverURL+"/monitor/kubernetes/local/contexts/staging-dev", url.Values{
		"csrf_token": {formToken(t, page)}, "path": {path}, "action": {"use"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("use context status=%d", response.StatusCode)
	}
	snapshot, err := kubeconfigmanager.Inspect(path)
	if err != nil || snapshot.Current != "staging-dev" {
		t.Fatalf("current context = %q, %v", snapshot.Current, err)
	}
}

func TestKubernetesLocalImportPreviewAndImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(localKubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	pageResponse, err := client.Get(serverURL + "/monitor/kubernetes?tab=local")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	incoming := []byte(`apiVersion: v1
kind: Config
clusters:
- name: lab
  cluster: {server: https://lab.example.test}
users:
- name: lab-user
  user: {}
contexts:
- name: lab
  context: {cluster: lab, user: lab-user, namespace: default}
current-context: lab
`)

	postUpload := func(endpoint string, includeCurrent bool) *http.Response {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("csrf_token", formToken(t, page))
		_ = writer.WriteField("path", path)
		if includeCurrent {
			_ = writer.WriteField("current_context", "import")
		}
		part, partErr := writer.CreateFormFile("kubeconfig", "lab.yaml")
		if partErr != nil {
			t.Fatal(partErr)
		}
		_, _ = part.Write(incoming)
		_ = writer.Close()
		request, requestErr := http.NewRequest(http.MethodPost, serverURL+endpoint, &body)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Content-Type", writer.FormDataContentType())
		result, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return result
	}

	preview := postUpload("/monitor/kubernetes/local/import/preview", false)
	previewBody, _ := io.ReadAll(preview.Body)
	_ = preview.Body.Close()
	if preview.StatusCode != http.StatusOK || !bytes.Contains(previewBody, []byte(`"contexts":1`)) {
		t.Fatalf("preview status=%d body=%s", preview.StatusCode, previewBody)
	}
	result := postUpload("/monitor/kubernetes/local/import", true)
	_ = result.Body.Close()
	if result.StatusCode != http.StatusSeeOther {
		t.Fatalf("import status=%d", result.StatusCode)
	}
	snapshot, err := kubeconfigmanager.Inspect(path)
	if err != nil || snapshot.Current != "lab" || len(snapshot.Contexts) != 3 {
		t.Fatalf("imported snapshot = %#v, %v", snapshot, err)
	}
}
