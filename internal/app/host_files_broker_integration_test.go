package app_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/app"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/privilegebroker"
)

func TestManagedFilesPageReadsHostThroughBroker(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "broker-host")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "broker-only.txt"), []byte("broker boundary"), 0o600); err != nil {
		t.Fatal(err)
	}
	brokerManager, err := hostfiles.Open(hostfiles.Options{Topology: testHostTopology{root: hostRoot}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := privilegebroker.NewBrokerHostFilesService(brokerManager)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil }, Authorizer: allowHostFilesAuthorizer{},
		Executor: rejectGenericPrivilegedExecutor{}, HostFiles: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() { _ = server.Close() })
	client := privilegebroker.NewClient(privilegebroker.ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})

	httpClient, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"), FileTopology: rejectingHostTopology{},
		HostFilesBackend: privilegebroker.NewHostFilesBackend(client),
	})
	response, err := httpClient.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "broker-only.txt") {
		t.Fatalf("managed Host Files page status=%d body=%s", response.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, "broker-only.txt")); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	response, err = httpClient.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Host Files without Broker status=%d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
}

type rejectingHostTopology struct{}

func (rejectingHostTopology) Roots() ([]hostfiles.Entry, error) {
	return nil, errors.New("Web topology must not be used")
}
func (rejectingHostTopology) FilesystemRoot(string) (string, error) {
	return "", errors.New("Web topology must not be used")
}
func (rejectingHostTopology) Restricted(string) bool { return true }

type allowHostFilesAuthorizer struct{}

func (allowHostFilesAuthorizer) Authorize(context.Context, privilegebroker.AuthorizationRequest) (privilegebroker.Actor, error) {
	return privilegebroker.Actor{UserID: "administrator", Username: "admin", Role: "administrator"}, nil
}
func (allowHostFilesAuthorizer) AuthorizeSession(context.Context, privilegebroker.AuthorizationRequest) (privilegebroker.Actor, error) {
	return privilegebroker.Actor{UserID: "administrator", Username: "admin", Role: "administrator"}, nil
}

type rejectGenericPrivilegedExecutor struct{}

func (rejectGenericPrivilegedExecutor) Execute(context.Context, privilegebroker.ExecutionRequest) error {
	return errors.New("generic privileged execution is not expected")
}
