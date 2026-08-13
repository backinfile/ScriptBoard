package app_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/clusterstatus"
)

type kubernetesFixtureFactory struct{ client *kubernetesFixtureClient }

func (factory kubernetesFixtureFactory) Open(context.Context, clusterstatus.Connection) (clusterstatus.Client, error) {
	return factory.client, nil
}

type kubernetesFixtureClient struct {
	snapshot clusterstatus.Snapshot
}

func (client *kubernetesFixtureClient) Close() error { return nil }
func (client *kubernetesFixtureClient) Capabilities(context.Context) (clusterstatus.Capabilities, error) {
	return clusterstatus.Capabilities{Workloads: true, Nodes: true, Metrics: true, Logs: true, Redeploy: true, Scale: true, RunCron: true}, nil
}
func (client *kubernetesFixtureClient) Fingerprint() string { return "sha256:fixture" }
func (client *kubernetesFixtureClient) Snapshot(context.Context) (clusterstatus.Snapshot, error) {
	return client.snapshot, nil
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

func TestKubernetesPageConfiguresTheOnlyClusterAndListsWorkloads(t *testing.T) {
	fixture := &kubernetesFixtureClient{snapshot: clusterstatus.Snapshot{
		CollectedAt: time.Now().UTC(), ServerVersion: "v1.35.1+k3s1", PodsReady: 2, PodsTotal: 2, Namespaces: 2, MetricsAvailable: true,
		Nodes:     []clusterstatus.Node{{Name: "edge-control-01", Role: "control-plane", Ready: true, CPUPercent: 12}},
		Workloads: []clusterstatus.Workload{{Key: "production/Deployment/api", Namespace: "production", Kind: "Deployment", Name: "api", Image: "ghcr.io/acme/api:v2", Status: "ready", StatusLabel: "正常", Ready: 2, Desired: 2, CPUMillicores: 200, MemoryBytes: 224 << 20, Revision: "rev 2"}},
	}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), KubernetesFactory: kubernetesFixtureFactory{client: fixture}})

	response, err := client.Get(serverURL + "/monitor/kubernetes")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Connect Kubernetes")) || !bytes.Contains(page, []byte(`/monitor/kubernetes/connection`)) {
		t.Fatalf("unconfigured page: status=%d body=%s", response.StatusCode, page)
	}
	response, err = client.Get(serverURL + "/monitor/kubernetes/connection")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(page, []byte("Kubeconfig servers may use HTTP or HTTPS. HTTP sends credentials and cluster data without transport encryption.")) {
		t.Fatalf("connection page does not explain HTTP support: %s", page)
	}

	response, err = client.PostForm(serverURL+"/monitor/kubernetes/connection", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"edge-home"}, "kubeconfig_path": {"/etc/scriptboard/kubeconfig"}, "context": {"default"}, "mode": {"limited"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor/kubernetes" {
		t.Fatalf("save connection: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	_ = response.Body.Close()

	response, err = client.Get(serverURL + "/monitor/kubernetes")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range [][]byte{[]byte("edge-home"), []byte("ghcr.io/acme/api:v2"), []byte("production"), []byte("Connection settings"), []byte("Monitor / Kubernetes"), []byte(`href="/monitor/kubernetes?direction=asc&amp;sort=name"`), []byte(`class="kubernetes-workload-controls"`), []byte(`class="section-index"`), []byte(`class="monitor-status-switch kubernetes-status-tabs"`), []byte(">Ready<")} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("configured page missing %q: %s", expected, page)
		}
	}
	for _, forbidden := range [][]byte{[]byte("Pinned workloads"), []byte("/monitor/kubernetes/workloads/production/Deployment/api/pin")} {
		if bytes.Contains(page, forbidden) {
			t.Fatalf("configured page still contains Kubernetes Pin UI %q: %s", forbidden, page)
		}
	}
}
