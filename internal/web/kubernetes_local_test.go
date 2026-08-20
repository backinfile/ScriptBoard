package web_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/clusterstatus"
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

type fixtureKubeconfigManager struct {
	kubeconfigmanager.DirectManager
	snapshot kubeconfigmanager.Snapshot
}

func (manager fixtureKubeconfigManager) Inspect(_ context.Context, path string) (kubeconfigmanager.Snapshot, error) {
	snapshot := manager.snapshot
	snapshot.Path = path
	return snapshot, nil
}

func TestKubernetesLocalManagementUsesConfiguredKubeconfigManager(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root-only-k3s.yaml")
	if err := os.WriteFile(path, []byte("not readable by the Web control plane"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	manager := fixtureKubeconfigManager{snapshot: kubeconfigmanager.Snapshot{
		Exists: true, Current: "k3s-admin", Contexts: []kubeconfigmanager.Context{{Name: "k3s-admin", Current: true}},
	}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), KubeconfigManager: manager,
	})

	response, err := client.Get(serverURL + "/monitor/kubernetes?tab=local")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("k3s-admin")) {
		t.Fatalf("local management bypassed configured kubeconfig manager: status=%d body=%s", response.StatusCode, page)
	}
}

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
	for _, expected := range []string{`data-kubernetes-tab="local"`, `data-kubernetes-local`, `data-kubernetes-import-drawer`, `data-kubernetes-common-paths`, `production-admin`, `staging-dev`, `/monitor/kubernetes/local/download`, `/etc/rancher/k3s/k3s.yaml`, `Default path`} {
		if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte(expected)) {
			t.Fatalf("local page missing %q: status=%d body=%s", expected, response.StatusCode, page)
		}
	}
	if bytes.Contains(page, []byte("The selected kubeconfig path is not managed by ScriptBoard")) {
		t.Fatalf("default local kubeconfig path was rejected: %s", page)
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

func TestKubernetesLocalManagementDisablesMissingPathCandidate(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing-kubeconfig")
	t.Setenv("KUBECONFIG", missingPath)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})

	response, err := client.Get(serverURL + "/monitor/kubernetes?tab=local")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte(`value="`+missingPath+`" selected disabled`)) {
		t.Fatalf("missing local kubeconfig candidate should be disabled: status=%d body=%s", response.StatusCode, page)
	}
}

func TestKubernetesLocalContextWithSlashCanBeRenamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	fixture := strings.Replace(localKubeconfigFixture, "- name: staging-dev\n", "- name: team/staging-dev\n", 1)
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
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
	response, err = client.PostForm(serverURL+"/monitor/kubernetes/local/contexts", url.Values{
		"csrf_token": {formToken(t, page)}, "path": {path}, "context": {"team/staging-dev"}, "action": {"rename"}, "name": {"team/preview"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename slash context status=%d body=%s", response.StatusCode, body)
	}
	snapshot, err := kubeconfigmanager.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Contexts[1].Name != "team/preview" {
		t.Fatalf("renamed context=%q", snapshot.Contexts[1].Name)
	}
}

func TestKubernetesLocalContextCanBeAddedAsConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(localKubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	fixture := &kubernetesFixtureClient{snapshot: clusterstatus.Snapshot{CollectedAt: time.Now().UTC()}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), KubernetesFactory: kubernetesFixtureFactory{client: fixture},
	})

	response, err := client.Get(serverURL + "/monitor/kubernetes?tab=local")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	addAction := `/monitor/kubernetes/local/contexts/production-admin/connection`
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte(addAction)) || !bytes.Contains(page, []byte("Add cluster connection")) {
		t.Fatalf("local connection action missing: status=%d body=%s", response.StatusCode, page)
	}

	response, err = client.PostForm(serverURL+addAction, url.Values{"csrf_token": {formToken(t, page)}, "path": {path}})
	if err != nil {
		t.Fatal(err)
	}
	location := response.Header.Get("Location")
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(location, "notice=connection_added") {
		t.Fatalf("add local connection: status=%d location=%q", response.StatusCode, location)
	}

	response, err = client.Get(serverURL + location)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(page, []byte("Open monitor")) || bytes.Contains(page, []byte(`action="`+addAction+`"`)) {
		t.Fatalf("added context did not switch to monitor action: %s", page)
	}

	response, err = client.PostForm(serverURL+addAction, url.Values{"csrf_token": {formToken(t, page)}, "path": {path}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "notice=connection_exists") {
		t.Fatalf("duplicate local connection: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	_ = response.Body.Close()

	response, err = client.Get(serverURL + "/monitor/kubernetes?tab=connections")
	if err != nil {
		t.Fatal(err)
	}
	connectionsPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if count := bytes.Count(connectionsPage, []byte(`class="kubernetes-connection-row"`)); count != 1 {
		t.Fatalf("connection row count=%d body=%s", count, connectionsPage)
	}
}

func TestKubernetesLocalCurrentContextConnectionIsNotDuplicated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(localKubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	fixture := &kubernetesFixtureClient{snapshot: clusterstatus.Snapshot{CollectedAt: time.Now().UTC()}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), KubernetesFactory: kubernetesFixtureFactory{client: fixture},
	})

	response, err := client.Get(serverURL + "/monitor/kubernetes/connections/new")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/monitor/kubernetes/connections", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"current"}, "kubeconfig_path": {path}, "context": {""}, "mode": {"observe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("save current-context connection status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/monitor/kubernetes?tab=local")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	addAction := `/monitor/kubernetes/local/contexts/production-admin/connection`
	if !bytes.Contains(page, []byte("Open monitor")) || bytes.Contains(page, []byte(`action="`+addAction+`"`)) {
		t.Fatalf("current-context connection was not recognized: %s", page)
	}

	response, err = client.PostForm(serverURL+addAction, url.Values{"csrf_token": {formToken(t, page)}, "path": {path}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "notice=connection_exists") {
		t.Fatalf("current-context duplicate status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	_ = response.Body.Close()
}

func TestKubernetesLocalConnectionSaveIsAuditedWhenInitialRefreshFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(localKubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	fixture := &kubernetesFixtureClient{snapshotErr: errors.New("snapshot unavailable")}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), KubernetesFactory: kubernetesFixtureFactory{client: fixture},
	})

	response, err := client.Get(serverURL + "/monitor/kubernetes?tab=local")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/monitor/kubernetes/local/contexts/production-admin/connection", url.Values{
		"csrf_token": {formToken(t, page)}, "path": {path},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("refresh failure status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/history/audit.csv?q=add_local_kubeconfig_connection")
	if err != nil {
		t.Fatal(err)
	}
	auditPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(auditPage, []byte("add_local_kubeconfig_connection")) {
		t.Fatalf("saved connection audit missing: status=%d body=%s", response.StatusCode, auditPage)
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
