package privilegebroker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/kubeconfigmanager"
)

const brokerKubeconfigFixture = `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: https://127.0.0.1:6443
users:
- name: admin
  user: {}
contexts:
- name: k3s-admin
  context:
    cluster: local
    user: admin
current-context: k3s-admin
`

type exportableKubeconfigService struct {
	kubeconfigmanager.DirectManager
}

func TestValidateKubeconfigRequestRejectsUnscopedOrOversizedAccess(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationKubeconfigInspect, RequestID: "kubeconfig-validation",
		SessionToken: strings.Repeat("s", 32), Kubeconfig: &kubeconfigWireRequest{Path: filepath.Join(t.TempDir(), "config")}}
	cases := []wireRequest{
		func() wireRequest { value := valid; value.SessionToken = ""; return value }(),
		func() wireRequest {
			value := valid
			value.Kubeconfig = &kubeconfigWireRequest{Path: "relative"}
			return value
		}(),
		func() wireRequest {
			value := valid
			value.Kubeconfig = &kubeconfigWireRequest{Path: valid.Kubeconfig.Path, Raw: []byte("unexpected")}
			return value
		}(),
		func() wireRequest { value := valid; value.Runtime = &runtimeWireRequest{}; return value }(),
		{Version: ProtocolVersion, Operation: operationKubeconfigImport, RequestID: "kubeconfig-oversized", SessionToken: strings.Repeat("s", 32),
			Kubeconfig: &kubeconfigWireRequest{Path: valid.Kubeconfig.Path, Raw: make([]byte, kubeconfigmanager.MaxFileSize+1)}},
	}
	for index, request := range cases {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("invalid kubeconfig request %d was accepted", index)
		}
	}
}

func (exportableKubeconfigService) Exportable(context.Context, string) (bool, error) {
	return true, nil
}

func TestRemoteKubeconfigManagerInspectsAndMutatesThroughBroker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k3s.yaml")
	if err := os.WriteFile(path, []byte(brokerKubeconfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator", Username: "admin", Role: "administrator"}},
		Executor:   &fixtureExecutor{}, Kubeconfigs: exportableKubeconfigService{}, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	manager := NewRemoteKubeconfigManager(client)
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "kubeconfig-session-token-0123456789", RequestID: "kubeconfig-broker-test"})

	snapshot, err := manager.Inspect(ctx, path)
	if err != nil || snapshot.Current != "k3s-admin" || len(snapshot.Contexts) != 1 || !snapshot.Exportable {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if err := manager.RenameContext(ctx, path, "k3s-admin", "k3s-local"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = manager.Inspect(ctx, path)
	if err != nil || snapshot.Current != "k3s-local" || len(snapshot.Contexts) != 1 || snapshot.Contexts[0].Name != "k3s-local" {
		t.Fatalf("mutated snapshot=%#v err=%v", snapshot, err)
	}
}
