package web_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/privilegebroker"
	app "scriptboard/internal/web"
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
	response, err = httpClient.Get(hostFileRequestURL(serverURL, "/resources/files/download", filepath.Join(hostRoot, "broker-only.txt")))
	if err != nil {
		t.Fatal(err)
	}
	download, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(download) != "broker boundary" {
		t.Fatalf("managed Host Files download=%q error=%v", download, err)
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

func TestQuickCreateTreatsBrokerNotFoundAsAvailableDestination(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "broker-host")
	stagingRoot := filepath.Join(root, "exchange")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	brokerManager, err := hostfiles.Open(hostfiles.Options{Topology: testHostTopology{root: hostRoot}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := privilegebroker.NewBrokerHostFilesService(brokerManager, stagingRoot)
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
		HostFilesBackend: privilegebroker.NewHostFilesBackend(client, stagingRoot),
	})
	page := getBody(t, httpClient, serverURL+"/config/quick-runs/from-source/new", http.StatusOK)
	language, extension, source := "shell", ".sh", "#!/bin/sh\nprintf broker-quick-create\\n"
	if runtime.GOOS == "windows" {
		language, extension, source = "batch", ".cmd", "@echo off\r\necho broker-quick-create\r\n"
	}
	response, err := httpClient.PostForm(serverURL+"/config/quick-runs/from-source", url.Values{
		"csrf_token": {formToken(t, page)}, "language": {language}, "source": {source},
		"working_directory": {hostRoot}, "file_name": {"broker-created"}, "name": {"Broker created"},
		"timeout_seconds": {"30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("Broker-backed Quick Create status=%d body=%s", response.StatusCode, body)
	}
	created, err := os.ReadFile(filepath.Join(hostRoot, "broker-created"+extension))
	if err != nil || string(created) != source {
		t.Fatalf("Broker-backed Quick Create source=%q error=%v", created, err)
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
