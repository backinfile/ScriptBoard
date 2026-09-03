package web_test

import (
	"context"
	"database/sql"
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
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/privilegebroker"
	app "scriptboard/internal/web"
)

func TestManagedBatchTrashDoesNotLockBrokerResultAudit(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "broker-host")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(hostRoot, "first.txt"), filepath.Join(hostRoot, "second.txt")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	initializer, err := app.Open(app.Config{StateRoot: stateRoot, FileTopology: testHostTopology{root: hostRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if err := initializer.Close(); err != nil {
		t.Fatal(err)
	}
	brokerDatabase, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(stateRoot, "app.db"))+"?mode=rw&_pragma=busy_timeout(50)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brokerDatabase.Close() })
	security, err := privilegebroker.NewDatabaseSecurity(brokerDatabase, auditlog.New(brokerDatabase), time.Now)
	if err != nil {
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
		Auditor: security, Executor: rejectGenericPrivilegedExecutor{}, HostFiles: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() { _ = server.Close() })
	brokerClient := privilegebroker.NewClient(privilegebroker.ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	httpClient, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: stateRoot, FileTopology: rejectingHostTopology{},
		HostFilesBackend: privilegebroker.NewHostFilesBackend(brokerClient),
	})
	page := getBody(t, httpClient, hostFilesRequestURL(serverURL, hostRoot), http.StatusOK)
	response, err := httpClient.PostForm(serverURL+"/resources/files/batch-delete", url.Values{
		"csrf_token": {formToken(t, page)}, "confirm_references": {"yes"}, "path": paths,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("Broker-backed batch trash status=%d body=%s", response.StatusCode, body)
	}
	trashPage := getBody(t, httpClient, serverURL+"/resources/trash", http.StatusOK)
	for _, path := range paths {
		if !strings.Contains(string(trashPage), path) {
			t.Fatalf("Broker-backed batch trash did not record %q", path)
		}
	}
}

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

func TestManagedBatchUploadTreatsBrokerNotFoundAsAvailableDestination(t *testing.T) {
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
	page := getBody(t, httpClient, hostFilesRequestURL(serverURL, hostRoot), http.StatusOK)
	status, body := postHostUploadBatch(t, httpClient, serverURL, formToken(t, []byte(page)), hostRoot, "skip", map[string]string{"new.txt": "new"})
	if status != http.StatusOK {
		t.Fatalf("Broker-backed batch upload status=%d body=%s", status, body)
	}
}

func TestManagedMoveTreatsBrokerNotFoundAsAvailableDestination(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "broker-host")
	stagingRoot := filepath.Join(root, "exchange")
	source := filepath.Join(hostRoot, "source.txt")
	destination := filepath.Join(hostRoot, "renamed.txt")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("broker rename"), 0o600); err != nil {
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
	page := getBody(t, httpClient, hostFilesRequestURL(serverURL, hostRoot), http.StatusOK)
	response, err := httpClient.PostForm(serverURL+"/resources/files/move", url.Values{
		"source": {source}, "destination": {destination}, "csrf_token": {formToken(t, []byte(page))},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("Broker-backed move status=%d body=%s", response.StatusCode, body)
	}
	if content, err := os.ReadFile(destination); err != nil || string(content) != "broker rename" {
		t.Fatalf("Broker-backed move destination=%q error=%v", content, err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Broker-backed move source still exists: %v", err)
	}
}

func TestManagedWebPreservesBrokerHostFilesExchangeRoot(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "broker-host")
	stateRoot := filepath.Join(root, "state")
	stagingRoot := filepath.Join(stateRoot, "inbox", "host-files-broker")
	sentinel := filepath.Join(stagingRoot, "broker-owned")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stagingRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("preserve broker exchange"), 0o600); err != nil {
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
	_, _ = authenticatedClientWithConfig(t, app.Config{
		StateRoot: stateRoot, FileTopology: rejectingHostTopology{},
		HostFilesBackend: privilegebroker.NewHostFilesBackend(client, stagingRoot),
	})
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "preserve broker exchange" {
		t.Fatalf("managed Web changed Broker exchange content=%q error=%v", content, err)
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
