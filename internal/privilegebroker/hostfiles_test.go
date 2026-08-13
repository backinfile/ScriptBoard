package privilegebroker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

func TestHostFilesUsesTypedAuthorizedBrokerOperations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("broker host files"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{Topology: fixtureHostFilesTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewBrokerHostFilesService(manager)
	if err != nil {
		t.Fatal(err)
	}
	backend, closeServer := hostFilesTestBackend(t, service)
	defer closeServer()
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "host-files-domain-test"})

	entries, err := backend.List(ctx, root)
	if err != nil || len(entries) != 1 || entries[0].Name != "note.txt" {
		t.Fatalf("list = %+v, %v", entries, err)
	}
	document, err := backend.ReadText(ctx, filepath.Join(root, "note.txt"), 1024)
	if err != nil || document.Content != "broker host files" {
		t.Fatalf("document = %+v, %v", document, err)
	}
	if err := backend.CreateDirectory(ctx, root, "created"); err != nil {
		t.Fatal(err)
	}
	trash, err := backend.MoveToTrash(ctx, filepath.Join(root, "note.txt"), "trash-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.RestoreFromTrash(ctx, trash.StoredPath, trash.OriginalPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestHostFilesFailsClosedWithoutAuthorizationAndForProtectedPaths(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "private")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: []string{protected}, Topology: fixtureHostFilesTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewBrokerHostFilesService(manager)
	backend, closeServer := hostFilesTestBackend(t, service)
	defer closeServer()
	if _, err := backend.List(context.Background(), root); err == nil {
		t.Fatal("Host Files read succeeded without authorization")
	}
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "host-files-protected-test"})
	if _, err := backend.List(ctx, protected); err == nil {
		t.Fatal("Broker exposed a protected path")
	}
}

func TestHostFilesProtocolRejectsGenericAndUnrelatedFields(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationHostFilesList, RequestID: "host-files-protocol-test",
		SessionToken: strings.Repeat("s", 32), HostFiles: &hostFilesWireRequest{Path: filepath.Clean(t.TempDir()), Limit: hostFilesPageSize}}
	requests := []wireRequest{
		func() wireRequest { value := valid; value.Parameters = json.RawMessage(`{"path":"no"}`); return value }(),
		func() wireRequest { value := valid; value.ProviderID = "credential-domain"; return value }(),
		func() wireRequest { value := valid; value.MySQL = &mysqlWireRequest{}; return value }(),
		func() wireRequest { value := valid; value.SessionToken = ""; return value }(),
		func() wireRequest { value := valid; value.HostFiles.Limit = hostFilesPageSize + 1; return value }(),
		func() wireRequest {
			value := valid
			value.HostFiles.StagingPath = filepath.Join(t.TempDir(), "smuggled")
			return value
		}(),
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("accepted invalid Host Files request: %+v", request)
		}
	}
}

func TestExternalHostFilesLogProtocolRejectsGenericAndSessionFields(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationHostFilesExternalLog, RequestID: "external-host-log-test",
		HostFiles: &hostFilesWireRequest{ExternalToken: "sbk_synthetic_external_token_value", ExternalEntryID: "entry-1", ExternalEntryName: "deployment-log", ExternalMessage: "deployment complete"}}
	if err := validateWireRequest(valid); err != nil {
		t.Fatalf("valid external Host Files log request rejected: %v", err)
	}
	requests := []wireRequest{
		func() wireRequest { value := valid; value.SessionToken = strings.Repeat("s", 32); return value }(),
		func() wireRequest { value := valid; value.Parameters = json.RawMessage(`{}`); return value }(),
		func() wireRequest { value := valid; value.HostFiles.Path = filepath.Clean(t.TempDir()); return value }(),
		func() wireRequest {
			value := valid
			value.HostFiles.ExternalMessage = strings.Repeat("x", (8<<10)+1)
			return value
		}(),
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("accepted invalid external Host Files log request: %+v", request)
		}
	}
}

func TestScheduledHostFilesProtocolIsSessionlessAndFixed(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationHostFilesPrepareSchedule, RequestID: "scheduled-host-files-test",
		HostFiles: &hostFilesWireRequest{ScheduleID: "schedule-1"}}
	if err := validateWireRequest(valid); err != nil {
		t.Fatalf("valid scheduled Host Files request rejected: %v", err)
	}
	valid.SessionToken = strings.Repeat("s", 32)
	if err := validateWireRequest(valid); err == nil {
		t.Fatal("scheduled Host Files request accepted a reusable user session")
	}
}

func TestHostFilesStagesUploadsAndStreamsLargeDownloadsThroughBroker(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	stagingRoot := filepath.Join(root, "exchange")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{Topology: fixtureHostFilesTopology{root: hostRoot}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewBrokerHostFilesService(manager, stagingRoot)
	if err != nil {
		t.Fatal(err)
	}
	backend, closeServer := hostFilesTestBackend(t, service)
	defer closeServer()
	backend.stagingRoot = stagingRoot
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "host-files-content-test"})
	payload := bytes.Repeat([]byte("broker-chunk-proof\n"), 400_000)
	if _, err := backend.Upload(ctx, hostRoot, "large.txt", bytes.NewReader(payload), int64(len(payload)), false, ""); err != nil {
		t.Fatal(err)
	}
	file, info, err := backend.OpenRegular(ctx, filepath.Join(hostRoot, "large.txt"))
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(payload)) || !bytes.Equal(read, payload) {
		t.Fatalf("download size=%d/%d content_equal=%v", info.Size, len(payload), bytes.Equal(read, payload))
	}
	entries, err := os.ReadDir(stagingRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging entries=%d error=%v", len(entries), err)
	}
}

func TestHostFilesReadHandlesAreBoundToTheAuthorizedUser(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bound.txt")
	if err := os.WriteFile(path, []byte("bound content"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{Topology: fixtureHostFilesTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	serviceValue, err := NewBrokerHostFilesService(manager)
	if err != nil {
		t.Fatal(err)
	}
	service := serviceValue.(*brokerHostFilesService)
	handle, _, err := service.OpenRead(context.Background(), "user-a", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadChunk(context.Background(), "user-b", handle, 0, 5); err == nil {
		t.Fatal("a different user consumed a Host Files read handle")
	}
	content, err := service.ReadChunk(context.Background(), "user-a", handle, 0, 32)
	if err != nil || string(content) != "bound content" {
		t.Fatalf("owner read = %q, %v", content, err)
	}
	if err := service.CloseRead(context.Background(), "user-a", handle); err != nil {
		t.Fatal(err)
	}
}

func TestHostFilesLogHistoryRunsInsideBroker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "service.log")
	if err := os.WriteFile(path, []byte("first line\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{Topology: fixtureHostFilesTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewBrokerHostFilesService(manager)
	if err != nil {
		t.Fatal(err)
	}
	backend, closeServer := hostFilesTestBackend(t, service)
	defer closeServer()
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "host-files-log-test"})
	source, err := backend.OpenLogSource(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.History(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Text != "first line" || page.Entries[1].Text != "second line" {
		t.Fatalf("unexpected Broker log history: %+v", page.Entries)
	}
}

func hostFilesTestBackend(t *testing.T, service HostFilesService) (*HostFilesBackend, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureMySQLAuthorizer{actor: Actor{UserID: "administrator", Role: "administrator"}}, Executor: &fixtureExecutor{}, HostFiles: service})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	return NewHostFilesBackend(client), func() { _ = server.Close() }
}

type fixtureHostFilesTopology struct{ root string }

func (topology fixtureHostFilesTopology) Roots() ([]hostfiles.Entry, error) {
	return []hostfiles.Entry{{Name: "fixture", Path: topology.root, Kind: hostfiles.Directory}}, nil
}
func (topology fixtureHostFilesTopology) FilesystemRoot(string) (string, error) {
	return topology.root, nil
}
func (fixtureHostFilesTopology) Restricted(string) bool { return false }
