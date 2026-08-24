package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/config"
	"scriptboard/internal/installation"
	"scriptboard/internal/kubeconfigmanager"
	"scriptboard/internal/privilegebroker"
)

func TestApplicationInstallRootRejectsExecutableOutsideManagedRelease(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	installRoot := filepath.Join(root, "install")
	if err := os.MkdirAll(filepath.Join(stateRoot, "updates"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installRoot, "versions", "1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := installation.Metadata{
		Schema: installation.MetadataSchema, InstallID: "test-install", InstallRoot: installRoot,
		StateRoot: stateRoot, ServiceName: "ScriptBoard", ConfigPath: filepath.Join(root, "config.yaml"),
		OS: runtime.GOOS, Arch: runtime.GOARCH, ManagedLayout: true, Current: "1.0.0",
	}
	write := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(installRoot, "install.json"), metadata)
	write(filepath.Join(stateRoot, "updates", "install-ref.json"), map[string]any{
		"schema": installation.MetadataSchema, "install_id": metadata.InstallID, "install_root": installRoot,
	})

	if _, err := applicationInstallRoot(stateRoot); err == nil || !strings.Contains(err.Error(), "outside the active Installed Release") {
		t.Fatalf("managed executable mismatch did not fail closed: %v", err)
	}
}

func TestRuntimeDependenciesFollowDeploymentMode(t *testing.T) {
	portable, err := webDependencies(config.Config{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if portable.applicationProbe != nil || portable.kubernetesFactory != nil || portable.brokerEndpoint != "" {
		t.Fatalf("portable dependencies unexpectedly use Broker runtime: %#v", portable)
	}
	managed, err := webDependenciesWithIdentity(config.Config{StateRoot: t.TempDir()}, t.TempDir(), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if managed.brokerEndpoint == "" {
		t.Fatal("managed dependencies have no Broker endpoint")
	}
	if _, ok := managed.applicationProbe.(*privilegebroker.ApplicationProbe); !ok {
		t.Fatalf("managed application probe type = %T", managed.applicationProbe)
	}
	if _, ok := managed.kubernetesFactory.(privilegebroker.KubernetesFactory); !ok {
		t.Fatalf("managed Kubernetes factory type = %T", managed.kubernetesFactory)
	}
	if _, ok := managed.kubeconfigManager.(*privilegebroker.RemoteKubeconfigManager); !ok {
		t.Fatalf("managed kubeconfig manager type = %T", managed.kubeconfigManager)
	}
}

func TestBrokerKubernetesServiceResolvesSavedConnection(t *testing.T) {
	db, err := sql.Open("sqlite", "file:broker-kubernetes-resolver?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE kubernetes_connection (id TEXT PRIMARY KEY, name TEXT, kubeconfig_path TEXT, context_name TEXT, operation_mode TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO kubernetes_connection VALUES ('k8s-local','Local','C:\\secure\\kubeconfig','production','limited')`); err != nil {
		t.Fatal(err)
	}
	connection, found, err := (brokerKubernetesService{db: db, factory: clusterstatus.HTTPFactory{}}).ResolveConnection(context.Background(), "k8s-local")
	if err != nil || !found || connection.Name != "Local" || connection.Context != "production" || connection.Mode != clusterstatus.ModeLimited {
		t.Fatalf("connection=%#v found=%v err=%v", connection, found, err)
	}
}

func TestBrokerKubeconfigManagerReadsRegisteredExternalPathWithoutExportingCredentials(t *testing.T) {
	db, err := sql.Open("sqlite", "file:broker-kubeconfig-manager?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE kubernetes_connection (id TEXT PRIMARY KEY, kubeconfig_path TEXT)`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "root-only-k3s.yaml")
	fixture := `apiVersion: v1
kind: Config
clusters: []
users: []
contexts: []
current-context: ""
`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO kubernetes_connection VALUES ('k3s', ?)`, path); err != nil {
		t.Fatal(err)
	}
	manager := newBrokerKubeconfigManager(db, filepath.Join(t.TempDir(), "state"), "")
	snapshot, err := manager.Inspect(context.Background(), path)
	if err != nil || !snapshot.Exists {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if exportable, err := manager.Exportable(context.Background(), path); err != nil || exportable {
		t.Fatalf("external kubeconfig exportable=%v err=%v", exportable, err)
	}
	if _, err := manager.Download(context.Background(), path); err == nil {
		t.Fatal("Broker exported credentials from an external privileged kubeconfig")
	}
	var _ kubeconfigmanager.Manager = manager
}

func TestConnectOnDemandHostRetriesUntilEndpointIsReady(t *testing.T) {
	starts, attempts := 0, 0
	peerClosed := make(chan struct{})
	connection, err := connectOnDemandHost(context.Background(), func(context.Context) error {
		starts++
		return nil
	}, func(context.Context) (net.Conn, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("endpoint is not ready")
		}
		client, peer := net.Pipe()
		go func() {
			_ = peer.Close()
			close(peerClosed)
		}()
		return client, nil
	}, "test host")
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	<-peerClosed
	if starts != 1 || attempts != 3 {
		t.Fatalf("starts=%d attempts=%d", starts, attempts)
	}
}

func TestReadSecurityEventTokenIsBoundedAndTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  fixture-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readSecurityEventToken(path)
	if err != nil || value != "fixture-token" {
		t.Fatalf("token=%q err=%v", value, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4097)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecurityEventToken(path); err == nil {
		t.Fatal("oversized security event token accepted")
	}
}

func TestValidateNetworkConfigurationRequiresCompleteTLSAndHostPort(t *testing.T) {
	for _, input := range [][3]string{{"127.0.0.1", "", ""}, {"127.0.0.1:8787", "cert.pem", ""}} {
		if err := validateNetworkConfiguration(input[0], input[1], input[2]); err == nil {
			t.Fatalf("invalid network configuration accepted: %v", input)
		}
	}
	if err := validateNetworkConfiguration("127.0.0.1:8787", "", ""); err != nil {
		t.Fatal(err)
	}
}
