package privilegebroker

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/statebackup"
)

func TestStateBackupBrokerUsesTypedStepUpOperationsAndRedactsPrivateCheckpoint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &stateBackupAuthorizer{actor: Actor{UserID: "admin", Username: "admin", Role: "administrator", AuthenticationAssurance: 1}}
	service := &stateBackupBrokerFixture{}
	auditor := &stateBackupAuditor{}
	server, err := NewServer(ServerOptions{Listener: listener, VerifyPeer: func(net.Conn) error { return nil }, Authorizer: authorizer, Executor: &fixtureExecutor{}, Auditor: auditor, StateBackups: service})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	client := NewStateBackups(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "state-backup-test"})
	passphrase := []byte("broker state backup fixture passphrase")
	destination := filepath.Join(t.TempDir(), "state.sbsb")
	artifact, err := client.Create(ctx, destination, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Manifest.AuditCheckpoint != nil || service.createPassphrase != string(passphrase) {
		t.Fatalf("artifact=%#v service passphrase=%q", artifact, service.createPassphrase)
	}
	_, _, safeParameters := stateBackupMutationBinding(operationStateBackupCreate, stateBackupWireRequest{Destination: destination})
	if authorizer.last.ParametersSHA256 != parametersDigest(safeParameters) || strings.Contains(string(safeParameters), string(passphrase)) {
		t.Fatalf("authorization binding=%+v parameters=%s", authorizer.last, safeParameters)
	}
	stage, err := client.Stage(ctx, destination, passphrase, "ABCDEFGHIJKLMNOPQRSTUVWX")
	if err != nil || stage.ID != "YZabcdefghijklmnopqrstuv" || stage.Manifest.AuditCheckpoint != nil {
		t.Fatalf("stage=%#v err=%v", stage, err)
	}
	stages, err := client.List(ctx)
	if err != nil || len(stages) != 1 || stages[0].Manifest.AuditCheckpoint != nil {
		t.Fatalf("stages=%#v err=%v", stages, err)
	}
	if err := client.Discard(ctx, stage.ID); err != nil || service.discarded != stage.ID {
		t.Fatalf("discarded=%q err=%v", service.discarded, err)
	}
	if len(auditor.records) != 6 {
		t.Fatalf("audit records=%#v", auditor.records)
	}
}

func TestStateBackupProtocolRejectsUnboundedOrMixedSecretFields(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationStateBackupCreate, RequestID: "backup-validation", SessionToken: strings.Repeat("s", 32), StateBackup: &stateBackupWireRequest{Destination: filepath.Join(t.TempDir(), "state.sbsb"), Passphrase: []byte(strings.Repeat("p", 16))}}
	requests := []wireRequest{
		func() wireRequest { value := valid; value.Action = ActionUFWEnable; return value }(),
		func() wireRequest { value := valid; value.StateBackup.Passphrase = []byte("short"); return value }(),
		func() wireRequest {
			value := valid
			value.StateBackup.ArchivePath = value.StateBackup.Destination
			return value
		}(),
		{Version: ProtocolVersion, Operation: operationStateBackupStage, RequestID: "backup-validation", SessionToken: strings.Repeat("s", 32), StateBackup: &stateBackupWireRequest{ArchivePath: filepath.Join(t.TempDir(), "state.sbsb"), Passphrase: []byte(strings.Repeat("p", 16)), ConfirmBackupID: "wrong"}},
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("state backup protocol accepted invalid request: %+v", request)
		}
	}
}

type stateBackupAuthorizer struct {
	actor Actor
	last  AuthorizationRequest
}

func (authorizer *stateBackupAuthorizer) Authorize(_ context.Context, request AuthorizationRequest) (Actor, error) {
	authorizer.last = request
	return authorizer.actor, nil
}

func (authorizer *stateBackupAuthorizer) AuthorizeSession(_ context.Context, request AuthorizationRequest) (Actor, error) {
	authorizer.last = request
	return authorizer.actor, nil
}

type stateBackupAuditor struct{ records []AuditRecord }

func (auditor *stateBackupAuditor) Record(_ context.Context, record AuditRecord) error {
	auditor.records = append(auditor.records, record)
	return nil
}

type stateBackupBrokerFixture struct {
	createPassphrase string
	discarded        string
}

func (service *stateBackupBrokerFixture) Create(_ context.Context, destination string, passphrase []byte) (statebackup.Artifact, error) {
	service.createPassphrase = string(passphrase)
	return statebackup.Artifact{Path: destination, Manifest: brokerStateBackupManifest()}, nil
}

func (*stateBackupBrokerFixture) Inspect(_ context.Context, _ string, _ []byte) (statebackup.Manifest, error) {
	return brokerStateBackupManifest(), nil
}

func (*stateBackupBrokerFixture) Stage(_ context.Context, _ string, _ []byte, _ string) (statebackup.Stage, error) {
	return statebackup.Stage{ID: "YZabcdefghijklmnopqrstuv", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour), Manifest: brokerStateBackupManifest()}, nil
}

func (*stateBackupBrokerFixture) List(_ context.Context) ([]statebackup.Stage, error) {
	return []statebackup.Stage{{ID: "YZabcdefghijklmnopqrstuv", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour), Manifest: brokerStateBackupManifest()}}, nil
}

func (service *stateBackupBrokerFixture) Discard(_ context.Context, stageID string) error {
	service.discarded = stageID
	return nil
}

func brokerStateBackupManifest() statebackup.Manifest {
	return statebackup.Manifest{FormatVersion: 1, ID: "ABCDEFGHIJKLMNOPQRSTUVWX", CreatedAt: "2026-08-12T10:00:00Z", SchemaVersion: 43, AuditCheckpoint: json.RawMessage(`{"private":true}`)}
}
