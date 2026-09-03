package privilegebroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/hostfiles"
)

func TestHostFilesTrashLifecycleAcceptsValidSessionWithoutRecentStepUp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("trash lifecycle"), 0o600); err != nil {
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
	now := time.Unix(1786957701, 0).UTC()
	database := openBrokerDatabase(t)
	security, err := NewDatabaseSecurity(database, auditlog.New(database), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := "host-files-session-token-0123456789"
	insertBrokerSession(t, database, token, "maintainer", now.Add(-4*time.Hour).Unix(), now.Add(time.Hour).Unix())
	backend, closeServer := hostFilesTestBackendWithAuthorizer(t, service, security)
	defer closeServer()
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: token, RequestID: "host-files-trash-session-test"})

	trashed, err := backend.MoveToTrash(ctx, path, "trash-session")
	if err != nil {
		t.Fatalf("valid session could not move Host Entry to trash: %v", err)
	}
	if err := backend.RestoreFromTrash(ctx, trashed.StoredPath, trashed.OriginalPath); err != nil {
		t.Fatalf("valid session could not restore Host Entry: %v", err)
	}
	trashed, err = backend.MoveToTrash(ctx, path, "trash-session-purge")
	if err != nil {
		t.Fatalf("valid session could not move restored Host Entry to trash: %v", err)
	}
	if err := backend.PurgeTrash(ctx, trashed.StoredPath); err != nil {
		t.Fatalf("valid session could not purge trash: %v", err)
	}
	missingPath := filepath.Join(root, "missing-before-purge.txt")
	if err := os.WriteFile(missingPath, []byte("missing before purge"), 0o600); err != nil {
		t.Fatal(err)
	}
	trashed, err = backend.MoveToTrash(ctx, missingPath, "trash-session-missing-purge")
	if err != nil {
		t.Fatalf("move missing-purge fixture to trash: %v", err)
	}
	if err := os.Remove(trashed.StoredPath); err != nil {
		t.Fatalf("remove stored trash fixture: %v", err)
	}
	if err := backend.PurgeTrash(ctx, trashed.StoredPath); err != nil {
		t.Fatalf("Broker purge should be idempotent when stored content is missing: %v", err)
	}
	source, destination := filepath.Join(root, "source.txt"), filepath.Join(root, "destination.txt")
	if err := os.WriteFile(source, []byte("high-risk move"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := backend.Move(ctx, source, destination); err == nil {
		t.Fatal("stale step-up moved a Host Entry")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("denied Host Entry move changed source: %v", err)
	}
}

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

func TestHostFilesInfoPreservesNotExistThroughBroker(t *testing.T) {
	root := t.TempDir()
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
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "host-files-missing-info-test"})

	if _, err := backend.Info(ctx, filepath.Join(root, "new-upload.env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing Host Files info error = %v, want os.ErrNotExist", err)
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

func TestHostFilesProtocolRejectsOperationForbiddenFieldsForEveryOperation(t *testing.T) {
	operations := []string{
		operationHostFilesRoots, operationHostFilesList, operationHostFilesInfo, operationHostFilesReadText,
		operationHostFilesPermissions, operationHostFilesSetPermissions,
		operationHostFilesCanonical, operationHostFilesAvailable, operationHostFilesMkdir,
		operationHostFilesTrash, operationHostFilesRestore, operationHostFilesPurge, operationHostFilesMove,
		operationHostFilesOpenRead, operationHostFilesReadChunk, operationHostFilesCloseRead, operationHostFilesUpload,
		operationHostFilesSaveText, operationHostFilesRollback, operationHostFilesRemove, operationHostFilesPrepare,
		operationHostFilesSameFS, operationHostFilesAppend, operationHostFilesLogOpen, operationHostFilesLogHistory,
		operationHostFilesLogFollow, operationHostFilesLogClose, operationHostFilesCrossMove, operationHostFilesPrepareAppend,
	}
	for _, operation := range operations {
		t.Run(operation, func(t *testing.T) {
			request := wireRequest{
				Operation:    operation,
				SessionToken: strings.Repeat("s", 32),
				HostFiles:    &hostFilesWireRequest{ExternalToken: "smuggled-field"},
			}
			if err := validateHostFilesRequest(request); err == nil {
				t.Fatal("accepted an operation-forbidden field")
			}
		})
	}
}

func TestHostFilesPermissionProtocolRejectsMixedAndArbitraryRights(t *testing.T) {
	mode := uint32(0o640)
	read := hostfiles.WindowsAccessRead
	arbitrary := uint32(0x80000000)
	tests := []hostfiles.PermissionChange{
		{Mode: &mode, Owner: "S-1-5-18"},
		{Principal: "S-1-5-18", AccessMask: new(uint32)},
		{Principal: "S-1-5-18", AccessMask: &arbitrary},
		{AccessMask: &read},
	}
	for _, change := range tests {
		request := wireRequest{
			Operation: operationHostFilesSetPermissions, SessionToken: strings.Repeat("s", 32),
			HostFiles: &hostFilesWireRequest{Path: filepath.Join(t.TempDir(), "entry"), Permissions: &change},
		}
		if err := validateHostFilesRequest(request); err == nil {
			t.Fatalf("accepted invalid permission change: %+v", change)
		}
	}
}

func TestHostFilesPrepareAppendRequiresAnAbsolutePath(t *testing.T) {
	request := wireRequest{
		Operation:    operationHostFilesPrepareAppend,
		SessionToken: strings.Repeat("s", 32),
		HostFiles:    &hostFilesWireRequest{Path: filepath.Join(t.TempDir(), "events.log")},
	}
	if err := validateHostFilesRequest(request); err != nil {
		t.Fatalf("valid prepare-append request rejected: %v", err)
	}
	request.HostFiles.Path = "relative.log"
	if err := validateHostFilesRequest(request); err == nil {
		t.Fatal("prepare-append accepted a relative path")
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

func TestHostFilesBatchUploadCrossesBrokerAsOneOperation(t *testing.T) {
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
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "host-files-batch-test"})
	results, err := backend.UploadBatch(ctx, hostRoot, []hostfiles.UploadBatchInput{
		{Name: "first.txt", Source: strings.NewReader("first"), MaxBytes: 1024, StoredName: "first-old"},
		{Name: "second.txt", Source: strings.NewReader("second"), MaxBytes: 1024, StoredName: "second-old"},
		{Name: "folder/nested.txt", Source: strings.NewReader("nested"), MaxBytes: 1024, StoredName: "nested-old"},
	}, false, false)
	if err != nil || len(results) != 3 {
		t.Fatalf("batch results=%+v err=%v", results, err)
	}
	for name, want := range map[string]string{"first.txt": "first", "second.txt": "second", "folder/nested.txt": "nested"} {
		content, readErr := os.ReadFile(filepath.Join(hostRoot, filepath.FromSlash(name)))
		if readErr != nil || string(content) != want {
			t.Fatalf("%s content=%q err=%v", name, content, readErr)
		}
	}
	entries, err := os.ReadDir(stagingRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging entries=%d error=%v", len(entries), err)
	}
}

func TestHostFilesBatchUploadSynchronizesQuickRunsInsideBrokerTransaction(t *testing.T) {
	root := t.TempDir()
	hostRoot, stagingRoot := filepath.Join(root, "host"), filepath.Join(root, "exchange")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(hostRoot, "deploy.cmd")
	original, replacement := []byte("@echo off\r\necho old\r\n"), []byte("@echo off\r\necho new\r\n")
	if err := os.WriteFile(scriptPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{Topology: fixtureHostFilesTopology{root: hostRoot}})
	if err != nil {
		t.Fatal(err)
	}
	database := openBrokerDatabase(t)
	for _, statement := range []string{
		`CREATE TABLE trash_entries (id TEXT PRIMARY KEY, original_path TEXT, original_path_key TEXT, stored_path TEXT, stored_path_key TEXT, deleted_at INTEGER, size INTEGER, is_directory INTEGER)`,
		`CREATE TABLE quick_runs (id TEXT PRIMARY KEY, script_path_key TEXT, script_sha256 TEXT, revision INTEGER, updated_at INTEGER)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	originalDigest := fmt.Sprintf("%x", sha256.Sum256(original))
	if _, err := database.Exec(`INSERT INTO quick_runs VALUES ('broker-quick', ?, ?, 6, 1)`, hostfiles.ComparisonKey(scriptPath), originalDigest); err != nil {
		t.Fatal(err)
	}
	service := newBrokerHostFilesService(manager, stagingRoot, nil, context.Background(), database)
	backend, closeServer := hostFilesTestBackend(t, service)
	defer closeServer()
	backend.stagingRoot = stagingRoot
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "host-files-batch-sync-test"})
	results, err := backend.UploadBatch(ctx, hostRoot, []hostfiles.UploadBatchInput{{
		Name: "deploy.cmd", Source: bytes.NewReader(replacement), MaxBytes: 1024, StoredName: "broker-quick-old",
	}}, true, true)
	if err != nil || len(results) != 1 || results[0].QuickRunsSynchronized != 1 {
		t.Fatalf("batch synchronization results=%+v err=%v", results, err)
	}
	var digest string
	var revision int64
	if err := database.QueryRow(`SELECT script_sha256, revision FROM quick_runs WHERE id = 'broker-quick'`).Scan(&digest, &revision); err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256(replacement))
	if digest != wantDigest || revision != 7 || results[0].ScriptSHA256 != wantDigest {
		t.Fatalf("broker-synchronized Quick Run digest=%q revision=%d result=%+v", digest, revision, results[0])
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
	return hostFilesTestBackendWithAuthorizer(t, service, &fixtureMySQLAuthorizer{actor: Actor{UserID: "administrator", Role: "administrator"}})
}

func hostFilesTestBackendWithAuthorizer(t *testing.T, service HostFilesService, authorizer Authorizer) (*HostFilesBackend, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: authorizer, Executor: &fixtureExecutor{}, HostFiles: service})
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
