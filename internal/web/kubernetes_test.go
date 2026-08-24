package web_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/clusterstatus"
	app "scriptboard/internal/web"
)

type kubernetesFixtureFactory struct{ client *kubernetesFixtureClient }

func (factory kubernetesFixtureFactory) Open(context.Context, clusterstatus.Connection) (clusterstatus.Client, error) {
	return factory.client, nil
}

type kubernetesFixtureClient struct {
	snapshot    clusterstatus.Snapshot
	snapshotErr error
}

func (client *kubernetesFixtureClient) Close() error { return nil }
func (client *kubernetesFixtureClient) Capabilities(context.Context) (clusterstatus.Capabilities, error) {
	return clusterstatus.Capabilities{Workloads: true, Nodes: true, Metrics: true, Logs: true, Redeploy: true, Scale: true, RunCron: true}, nil
}
func (client *kubernetesFixtureClient) Fingerprint() string { return "sha256:fixture" }
func (client *kubernetesFixtureClient) Snapshot(context.Context) (clusterstatus.Snapshot, error) {
	return client.snapshot, client.snapshotErr
}
func (client *kubernetesFixtureClient) Detail(_ context.Context, key string) (clusterstatus.Detail, error) {
	for _, workload := range client.snapshot.Workloads {
		if workload.Key == key {
			return clusterstatus.Detail{Workload: workload, Pods: []clusterstatus.Pod{{Name: "api-abc", Namespace: workload.Namespace, Ready: "1/1", Phase: "Running"}}}, nil
		}
	}
	return clusterstatus.Detail{}, nil
}
func (client *kubernetesFixtureClient) Logs(context.Context, string, int) ([]clusterstatus.LogLine, error) {
	return []clusterstatus.LogLine{{At: time.Now(), Pod: "api-abc", Container: "api", Text: "ready"}}, nil
}
func (client *kubernetesFixtureClient) Operate(context.Context, clusterstatus.Operation) error {
	return nil
}

func TestKubernetesPageSeparatesConnectionsFromSelectedClusterMonitoring(t *testing.T) {
	fixture := &kubernetesFixtureClient{snapshot: clusterstatus.Snapshot{
		CollectedAt: time.Now().UTC(), ServerVersion: "v1.35.1+k3s1", PodsReady: 2, PodsTotal: 2, Namespaces: 2, MetricsAvailable: true,
		Nodes:     []clusterstatus.Node{{Name: "edge-control-01", Role: "control-plane", Ready: true, CPUPercent: 12}},
		Workloads: []clusterstatus.Workload{{Key: "production/Deployment/api", Namespace: "production", Kind: "Deployment", Name: "api", Image: "ghcr.io/acme/api:v2", Status: "ready", StatusLabel: "正常", Ready: 2, Desired: 2, CPUMillicores: 200, MemoryBytes: 224 << 20, Nodes: "edge-worker-01", Revision: "rev 2"}},
	}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), KubernetesFactory: kubernetesFixtureFactory{client: fixture}})

	response, err := client.Get(serverURL + "/monitor/kubernetes")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte(`data-kubernetes-tab="connections"`)) || !bytes.Contains(page, []byte(`/monitor/kubernetes/connections/new`)) || !bytes.Contains(page, []byte(`data-kubernetes-connection-drawer`)) || !bytes.Contains(page, []byte(`data-kubernetes-connection-open`)) {
		t.Fatalf("unconfigured page: status=%d body=%s", response.StatusCode, page)
	}
	if count := bytes.Count(page, []byte(`href="/monitor/kubernetes/connections/new"`)); count != 1 {
		t.Fatalf("unconfigured page should have one new connection action, got %d: %s", count, page)
	}
	response, err = client.Get(serverURL + "/monitor/kubernetes/connections/new")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(page, []byte("Kubeconfig servers may use HTTP or HTTPS. HTTP sends credentials and cluster data without transport encryption.")) {
		t.Fatalf("connection page does not explain HTTP support: %s", page)
	}
	if bytes.Contains(page, []byte(`data-kubernetes-common-paths`)) {
		t.Fatalf("common kubeconfig paths belong to local management, not the connection form: %s", page)
	}
	if bytes.Contains(page, []byte(`kubernetes-connection-danger`)) {
		t.Fatalf("new connection form must not offer deletion: %s", page)
	}

	response, err = client.PostForm(serverURL+"/monitor/kubernetes/connections", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"edge-home"}, "kubeconfig_path": {"/etc/scriptboard/kubeconfig"}, "context": {"default"}, "mode": {"limited"},
	})
	if err != nil {
		t.Fatal(err)
	}
	location := response.Header.Get("Location")
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(location, "/monitor/kubernetes?cluster=") {
		t.Fatalf("save connection: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	_ = response.Body.Close()
	firstID := strings.TrimPrefix(location, "/monitor/kubernetes?cluster=")

	response, err = client.Get(serverURL + "/monitor/kubernetes/connections/new")
	if err != nil {
		t.Fatal(err)
	}
	newConnectionPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/monitor/kubernetes/connections", url.Values{
		"csrf_token": {formToken(t, newConnectionPage)}, "name": {"staging"}, "kubeconfig_path": {"/etc/scriptboard/staging"}, "context": {"staging"}, "mode": {"observe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondLocation := response.Header.Get("Location")
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(secondLocation, "/monitor/kubernetes?cluster=") {
		t.Fatalf("save second connection: status=%d location=%q", response.StatusCode, secondLocation)
	}
	_ = response.Body.Close()
	secondID := strings.TrimPrefix(secondLocation, "/monitor/kubernetes?cluster=")
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("connection IDs first=%q second=%q", firstID, secondID)
	}

	response, err = client.Get(serverURL + secondLocation)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range [][]byte{[]byte(`data-kubernetes-tab="monitor"`), []byte(`data-monitor-refresh`), []byte("Showing the latest snapshot"), []byte(`name="cluster"`), []byte(`value="` + firstID + `"`), []byte(`value="` + secondID + `" selected`), []byte("edge-home"), []byte("staging"), []byte("ghcr.io/acme/api:v2"), []byte("production"), []byte(`>Node<`), []byte(`class="kubernetes-workload-node"><code title="edge-worker-01">edge-worker-01</code>`), []byte(`href="/monitor/kubernetes?tab=connections"`), []byte(`/monitor/kubernetes/clusters/` + secondID + `/workloads/production/Deployment/api/details`), []byte(`data-kubernetes-can-manage="true"`), []byte(`class="kubernetes-drawer"`), []byte(">Ready<")} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("configured page missing %q: %s", expected, page)
		}
	}
	response, err = client.Get(serverURL + "/monitor/kubernetes?tab=connections")
	if err != nil {
		t.Fatal(err)
	}
	connectionsPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range [][]byte{[]byte(`data-kubernetes-tab="connections"`), []byte("edge-home"), []byte("staging"), []byte(`/monitor/kubernetes/connections/` + firstID), []byte(`/monitor/kubernetes/connections/` + secondID)} {
		if !bytes.Contains(connectionsPage, expected) {
			t.Fatalf("connections page missing %q: %s", expected, connectionsPage)
		}
	}
	response, err = client.Get(serverURL + "/monitor/kubernetes/connections/" + firstID)
	if err != nil {
		t.Fatal(err)
	}
	connectionPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	deleteAction := `/monitor/kubernetes/connections/` + firstID + `/delete`
	for _, expected := range [][]byte{[]byte(`class="kubernetes-connection-danger"`), []byte(`action="` + deleteAction + `"`), []byte(`data-confirm=`), []byte("Delete connection")} {
		if !bytes.Contains(connectionPage, expected) {
			t.Fatalf("connection settings missing delete control %q: %s", expected, connectionPage)
		}
	}
	response, err = client.PostForm(serverURL+deleteAction, url.Values{"csrf_token": {"invalid"}, "confirm": {"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("delete connection without CSRF status=%d", response.StatusCode)
	}
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+deleteAction, url.Values{"csrf_token": {formToken(t, connectionPage)}, "confirm": {"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor/kubernetes?tab=connections" {
		t.Fatalf("delete connection status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/monitor/kubernetes?tab=connections")
	if err != nil {
		t.Fatal(err)
	}
	connectionsPage, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if bytes.Contains(connectionsPage, []byte("edge-home")) || !bytes.Contains(connectionsPage, []byte("staging")) {
		t.Fatalf("connection list after delete: %s", connectionsPage)
	}
	for _, forbidden := range [][]byte{[]byte("Pinned workloads"), []byte("/monitor/kubernetes/workloads/production/Deployment/api/pin")} {
		if bytes.Contains(page, forbidden) {
			t.Fatalf("configured page still contains Kubernetes Pin UI %q: %s", forbidden, page)
		}
	}
	logRequest, err := http.NewRequest(http.MethodGet, serverURL+"/monitor/kubernetes/clusters/"+secondID+"/workloads/production/Deployment/api/logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	logRequest.Header.Set("Accept", "text/html")
	logResponse, err := client.Do(logRequest)
	if err != nil {
		t.Fatal(err)
	}
	logPage, _ := io.ReadAll(logResponse.Body)
	_ = logResponse.Body.Close()
	if logResponse.StatusCode != http.StatusOK || !bytes.Contains(logPage, []byte(`data-kubernetes-logs-page`)) ||
		!bytes.Contains(logPage, []byte("ready")) || !bytes.Contains(logPage, []byte("api-abc/api")) ||
		!bytes.Contains(logPage, []byte(`class="run-log-section kubernetes-log-stage"`)) ||
		!bytes.Contains(logPage, []byte(`/logs/download`)) {
		t.Fatalf("Kubernetes log page status=%d body=%s", logResponse.StatusCode, logPage)
	}
	logDownload, err := client.Get(serverURL + "/monitor/kubernetes/clusters/" + secondID + "/workloads/production/Deployment/api/logs/download")
	if err != nil {
		t.Fatal(err)
	}
	logText, _ := io.ReadAll(logDownload.Body)
	_ = logDownload.Body.Close()
	if logDownload.StatusCode != http.StatusOK || !strings.Contains(logDownload.Header.Get("Content-Disposition"), ".txt") || !bytes.Contains(logText, []byte("api-abc/api")) || !bytes.Contains(logText, []byte("ready")) {
		t.Fatalf("Kubernetes log TXT download status=%d disposition=%q body=%s", logDownload.StatusCode, logDownload.Header.Get("Content-Disposition"), logText)
	}
}
