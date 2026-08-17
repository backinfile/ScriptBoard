package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/config"
	"scriptboard/internal/privilegebroker"
)

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
