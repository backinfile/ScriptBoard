package clusterstatus

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testHTTPKubeClient(t *testing.T, handler http.Handler) Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: test
clusters:
- name: test
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: test
  user:
    token: token
contexts:
- name: test
  context:
    cluster: test
    user: test
`, server.URL, base64.StdEncoding.EncodeToString(certificatePEM))
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := (HTTPFactory{}).Open(context.Background(), Connection{Name: "test", KubeconfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestHTTPSnapshotAggregatesPodsAndMetricsByStableWorkload(t *testing.T) {
	responses := map[string]string{
		"/version":                             `{"gitVersion":"v1.35.1"}`,
		"/apis/apps/v1/deployments":            `{"items":[{"metadata":{"name":"api","namespace":"production","uid":"deployment-api","creationTimestamp":"2026-08-01T00:00:00Z","annotations":{"deployment.kubernetes.io/revision":"42"}},"spec":{"replicas":2,"template":{"spec":{"containers":[{"name":"api","image":"ghcr.io/acme/api:v2"}]}}},"status":{"readyReplicas":2}}]}`,
		"/apis/apps/v1/statefulsets":           `{"items":[]}`,
		"/apis/apps/v1/daemonsets":             `{"items":[]}`,
		"/apis/apps/v1/replicasets":            `{"items":[{"metadata":{"name":"api-7dc9","namespace":"production","uid":"rs-api","ownerReferences":[{"uid":"deployment-api","kind":"Deployment","name":"api"}]}}]}`,
		"/apis/batch/v1/cronjobs":              `{"items":[]}`,
		"/apis/batch/v1/jobs":                  `{"items":[]}`,
		"/api/v1/pods":                         `{"items":[{"metadata":{"name":"api-7dc9-a","namespace":"production","ownerReferences":[{"uid":"rs-api","kind":"ReplicaSet","name":"api-7dc9"}]},"spec":{"nodeName":"worker-01","containers":[{"name":"api","image":"ghcr.io/acme/api:v2"}]},"status":{"phase":"Running","containerStatuses":[{"name":"api","ready":true,"restartCount":1}]}},{"metadata":{"name":"api-7dc9-b","namespace":"production","ownerReferences":[{"uid":"rs-api","kind":"ReplicaSet","name":"api-7dc9"}]},"spec":{"nodeName":"worker-02","containers":[{"name":"api","image":"ghcr.io/acme/api:v2"}]},"status":{"phase":"Running","containerStatuses":[{"name":"api","ready":true,"restartCount":0}]}}]}`,
		"/api/v1/nodes":                        `{"items":[{"metadata":{"name":"worker-01","labels":{"node-role.kubernetes.io/worker":""}},"status":{"conditions":[{"type":"Ready","status":"True"}],"capacity":{"cpu":"4","memory":"8Gi"},"nodeInfo":{"kubeletVersion":"v1.35.1"}}},{"metadata":{"name":"worker-02"},"status":{"conditions":[{"type":"Ready","status":"True"}],"capacity":{"cpu":"4","memory":"8Gi"},"nodeInfo":{"kubeletVersion":"v1.35.1"}}}]}`,
		"/api/v1/namespaces":                   `{"items":[{"metadata":{"name":"production"}},{"metadata":{"name":"kube-system"}}]}`,
		"/api/v1/services":                     `{"items":[{"metadata":{"name":"api-public","namespace":"production"},"spec":{"type":"NodePort","clusterIPs":["10.43.0.20"],"externalTrafficPolicy":"Local","ports":[{"name":"http","protocol":"TCP","port":80,"targetPort":8080,"nodePort":30080}]}},{"metadata":{"name":"database","namespace":"production"},"spec":{"type":"ClusterIP","clusterIP":"10.43.0.21","ports":[{"protocol":"TCP","port":5432,"targetPort":5432}]}}]}`,
		"/apis/networking.k8s.io/v1/ingresses": `{"items":[{"metadata":{"name":"api","namespace":"production"},"spec":{"ingressClassName":"traefik","tls":[{"hosts":["api.example.test"]}],"rules":[{"host":"api.example.test","http":{"paths":[{"path":"/v1","pathType":"Prefix","backend":{"service":{"name":"api-public","port":{"number":80}}}}]}}]},"status":{"loadBalancer":{"ingress":[{"ip":"192.0.2.10"}]}}}]}`,
		"/apis/metrics.k8s.io/v1beta1/pods":    `{"items":[{"metadata":{"name":"api-7dc9-a","namespace":"production"},"containers":[{"name":"api","usage":{"cpu":"125m","memory":"128Mi"}}]},{"metadata":{"name":"api-7dc9-b","namespace":"production"},"containers":[{"name":"api","usage":{"cpu":"75m","memory":"96Mi"}}]}]}`,
		"/apis/metrics.k8s.io/v1beta1/nodes":   `{"items":[{"metadata":{"name":"worker-01"},"usage":{"cpu":"400m","memory":"2Gi"}},{"metadata":{"name":"worker-02"},"usage":{"cpu":"800m","memory":"1Gi"}}]}`,
	}
	client := testHTTPKubeClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, ok := responses[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(body))
	}))

	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ServerVersion != "v1.35.1" || snapshot.Namespaces != 2 || snapshot.PodsReady != 2 || snapshot.PodsTotal != 2 || !snapshot.MetricsAvailable {
		t.Fatalf("snapshot facts: %#v", snapshot)
	}
	if len(snapshot.Workloads) != 1 {
		t.Fatalf("workloads: %#v", snapshot.Workloads)
	}
	workload := snapshot.Workloads[0]
	if workload.Key != "production/Deployment/api" || workload.Ready != 2 || workload.Desired != 2 || workload.Restarts != 1 || workload.CPUMillicores != 200 || workload.MemoryBytes != 224*1024*1024 || workload.Nodes != "worker-01, worker-02" {
		t.Fatalf("aggregated workload: %#v", workload)
	}
	if len(snapshot.Nodes) != 2 || snapshot.Nodes[0].CPUPercent != 10 || snapshot.Nodes[0].MemoryPercent != 25 {
		t.Fatalf("nodes: %#v", snapshot.Nodes)
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].Name != "api-public" || snapshot.Services[0].Ports[0].NodePort != 30080 || snapshot.Services[0].ExternalTrafficPolicy != "Local" {
		t.Fatalf("external services: %#v", snapshot.Services)
	}
	if len(snapshot.Ingresses) != 1 || snapshot.Ingresses[0].Class != "traefik" || snapshot.Ingresses[0].Addresses[0] != "192.0.2.10" || snapshot.Ingresses[0].Rules[0].Path != "/v1" || !snapshot.Ingresses[0].Rules[0].TLS {
		t.Fatalf("ingresses: %#v", snapshot.Ingresses)
	}
}

func TestHTTPSnapshotKeepsWorkloadsWhenPodAccessIsDenied(t *testing.T) {
	client := testHTTPKubeClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/version":
			_, _ = response.Write([]byte(`{"gitVersion":"v1.35.1"}`))
		case "/apis/apps/v1/deployments":
			_, _ = response.Write([]byte(`{"items":[{"metadata":{"name":"api","namespace":"production","uid":"api"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"api","image":"api:v1"}]}}},"status":{"readyReplicas":1}}]}`))
		case "/api/v1/pods":
			http.Error(response, `forbidden`, http.StatusForbidden)
		default:
			_, _ = response.Write([]byte(`{"items":[]}`))
		}
	}))
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workloads) != 1 || snapshot.Workloads[0].Name != "api" || snapshot.Errors["pods"] == "" {
		t.Fatalf("partial snapshot = %#v", snapshot)
	}
}

func TestHTTPSnapshotKeepsWorkloadsWhenExternalAccessIsDenied(t *testing.T) {
	client := testHTTPKubeClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/version":
			_, _ = response.Write([]byte(`{"gitVersion":"v1.35.1"}`))
		case "/apis/apps/v1/deployments":
			_, _ = response.Write([]byte(`{"items":[{"metadata":{"name":"api","namespace":"production","uid":"api"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"api","image":"api:v1"}]}}},"status":{"readyReplicas":1}}]}`))
		case "/api/v1/services", "/apis/networking.k8s.io/v1/ingresses":
			http.Error(response, `forbidden`, http.StatusForbidden)
		default:
			_, _ = response.Write([]byte(`{"items":[]}`))
		}
	}))
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workloads) != 1 || snapshot.Errors["services"] == "" || snapshot.Errors["ingresses"] == "" {
		t.Fatalf("partial snapshot = %#v", snapshot)
	}
}
