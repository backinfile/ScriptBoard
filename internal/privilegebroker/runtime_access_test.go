package privilegebroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/logstream"
)

type fixtureApplicationService struct {
	snapshot appstatus.RawSnapshot
	detail   appstatus.RuntimeDetail
	operated string
	action   appstatus.ContainerAction
	calls    int
	logs     logstream.Source
}

func (service *fixtureApplicationService) Snapshot(context.Context) appstatus.RawSnapshot {
	return service.snapshot
}

func (service *fixtureApplicationService) RuntimeDetail(context.Context, appstatus.DetailRequest) appstatus.RuntimeDetail {
	return service.detail
}

func TestApplicationProbeReadsDockerDetailThroughBroker(t *testing.T) {
	expected := appstatus.RuntimeDetail{State: appstatus.RuntimeAvailable, Kind: appstatus.KindDocker, Docker: &appstatus.DockerRuntimeDetail{ContainerID: "container-1", Image: "example/api:1"}}
	server, client := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.applications = &fixtureApplicationService{detail: expected}
	defer server.Close()

	actual := NewApplicationProbe(client).RuntimeDetail(context.Background(), appstatus.DetailRequest{Application: appstatus.Application{Kind: appstatus.KindDocker, Identity: "api"}})
	if actual.State != appstatus.RuntimeAvailable || actual.Docker == nil || actual.Docker.ContainerID != "container-1" {
		t.Fatalf("broker detail = %#v", actual)
	}
}

func (service *fixtureApplicationService) OperateContainer(_ context.Context, name string, action appstatus.ContainerAction) error {
	service.calls++
	service.operated, service.action = name, action
	return nil
}

func (service *fixtureApplicationService) LogSource(context.Context, appstatus.LogRequest) (logstream.Source, error) {
	if service.logs == nil {
		return nil, appstatus.ErrApplicationLogsUnsupported
	}
	return service.logs, nil
}

func TestApplicationProbeOperatesDockerContainerThroughBroker(t *testing.T) {
	applications := &fixtureApplicationService{}
	server, client := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "administrator"}}, &fixtureExecutor{})
	server.applications = applications
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "container-operation-1"})
	if err := NewApplicationProbe(client).OperateContainer(ctx, "api", appstatus.ContainerRestart); err != nil {
		t.Fatal(err)
	}
	if applications.operated != "api" || applications.action != appstatus.ContainerRestart {
		t.Fatalf("operation = %q %q", applications.operated, applications.action)
	}
}

func TestApplicationOperationAcceptsValidSessionWithoutRecentStepUp(t *testing.T) {
	now := time.Unix(1786957701, 0).UTC()
	database := openBrokerDatabase(t)
	security, err := NewDatabaseSecurity(database, auditlog.New(database), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := "application-session-token-0123456789"
	insertBrokerSession(t, database, token, "maintainer", now.Add(-4*time.Hour).Unix(), now.Add(time.Hour).Unix())
	applications := &fixtureApplicationService{}
	server, client := brokerFixture(t, security, &fixtureExecutor{})
	server.applications = applications
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: token, RequestID: "application-session-test"})
	if err := NewApplicationProbe(client).OperateContainer(ctx, "api", appstatus.ContainerRestart); err != nil {
		t.Fatalf("valid session could not operate container: %v", err)
	}
	if applications.operated != "api" || applications.action != appstatus.ContainerRestart {
		t.Fatalf("operation = %q %q", applications.operated, applications.action)
	}
}

func (service *fixtureApplicationService) Close() error { return nil }

type fixtureLogSource struct{}

func (fixtureLogSource) Metadata() logstream.Metadata {
	return logstream.Metadata{Kind: "docker", Name: "api", SourceVersion: "container-1", Running: true}
}

func (fixtureLogSource) History(context.Context, string) (logstream.Page, error) {
	return logstream.Page{SourceVersion: "container-1", Entries: []logstream.Entry{{Source: logstream.SourceStdout, Text: "ready"}}}, nil
}

func (fixtureLogSource) Follow(ctx context.Context, _ string, emit func(logstream.Event) error) error {
	if err := emit(logstream.Event{Kind: logstream.EventEntry, Entry: &logstream.Entry{Cursor: "cursor-1", Text: "live"}}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

type invalidCursorLogSource struct{ fixtureLogSource }

func (invalidCursorLogSource) History(context.Context, string) (logstream.Page, error) {
	return logstream.Page{}, logstream.ErrInvalidCursor
}

type floodingLogSource struct{ fixtureLogSource }

func (floodingLogSource) Follow(_ context.Context, _ string, emit func(logstream.Event) error) error {
	for index := 0; index < 600; index++ {
		if err := emit(logstream.Event{Kind: logstream.EventEntry, Entry: &logstream.Entry{Cursor: fmt.Sprintf("cursor-%d", index), Text: "entry"}}); err != nil {
			return err
		}
	}
	return nil
}

type oversizedLogSource struct{ fixtureLogSource }

func (oversizedLogSource) Follow(_ context.Context, _ string, emit func(logstream.Event) error) error {
	return emit(logstream.Event{Kind: logstream.EventEntry, Entry: &logstream.Entry{Cursor: strings.Repeat("c", logstream.DefaultPageBytes), Text: "entry"}})
}

func TestApplicationProbeReadsDockerSnapshotThroughBroker(t *testing.T) {
	expected := appstatus.RawSnapshot{
		CollectedAt: time.Unix(1_700_000_000, 0).UTC(), DockerAvailable: true,
		Containers: []appstatus.RawContainer{{ID: "container-1", Name: "api", Image: "example/api:1"}},
	}
	server, client := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.applications = &fixtureApplicationService{snapshot: expected}
	defer server.Close()

	actual := NewApplicationProbe(client).Snapshot(context.Background())
	if !actual.DockerAvailable || len(actual.Containers) != 1 || actual.Containers[0].ID != "container-1" || !actual.CollectedAt.Equal(expected.CollectedAt) {
		t.Fatalf("broker snapshot = %#v", actual)
	}
}

func TestApplicationProbeReadsDockerLogsThroughBroker(t *testing.T) {
	server, client := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.applications = &fixtureApplicationService{logs: fixtureLogSource{}}
	defer server.Close()

	source, err := NewApplicationProbe(client).LogSource(context.Background(), appstatus.LogRequest{Application: appstatus.Application{Kind: appstatus.KindDocker, Identity: "api"}})
	if err != nil {
		t.Fatal(err)
	}
	if metadata := source.Metadata(); metadata.SourceVersion != "container-1" || metadata.Name != "api" {
		t.Fatalf("metadata = %#v", metadata)
	}
	page, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Text != "ready" {
		t.Fatalf("history = %#v", page)
	}
}

func TestApplicationProbeFollowsDockerLogsThroughBoundedBrokerCalls(t *testing.T) {
	server, client := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.applications = &fixtureApplicationService{logs: fixtureLogSource{}}
	defer server.Close()

	source, err := NewApplicationProbe(client).LogSource(context.Background(), appstatus.LogRequest{Application: appstatus.Application{Kind: appstatus.KindDocker, Identity: "api"}})
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("entry received")
	err = source.Follow(context.Background(), "", func(event logstream.Event) error {
		if event.Entry != nil && event.Entry.Text == "live" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("follow error = %v", err)
	}
}

func TestApplicationLogFollowBrokerCallIsBoundedTo512Events(t *testing.T) {
	server, client := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.applications = &fixtureApplicationService{logs: floodingLogSource{}}
	defer server.Close()

	probe := NewApplicationProbe(client)
	response, err := probe.call(context.Background(), operationApplicationLogFollow, &runtimeWireRequest{Log: &appstatus.LogRequest{Application: appstatus.Application{Kind: appstatus.KindDocker, Identity: "api"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 512 || response.Events[511].Entry.Cursor != "cursor-511" {
		t.Fatalf("events=%d last=%#v", len(response.Events), response.Events[len(response.Events)-1])
	}
}

func TestApplicationLogFollowBrokerCallBoundsSerializedBytes(t *testing.T) {
	server, client := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.applications = &fixtureApplicationService{logs: oversizedLogSource{}}
	defer server.Close()

	response, err := NewApplicationProbe(client).call(context.Background(), operationApplicationLogFollow, &runtimeWireRequest{Log: &appstatus.LogRequest{Application: appstatus.Application{Kind: appstatus.KindDocker, Identity: "api"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 0 {
		t.Fatalf("oversized event was returned: %d events", len(response.Events))
	}
}

func TestApplicationProbePreservesInvalidDockerLogCursorError(t *testing.T) {
	server, client := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.applications = &fixtureApplicationService{logs: invalidCursorLogSource{}}
	defer server.Close()

	source, err := NewApplicationProbe(client).LogSource(context.Background(), appstatus.LogRequest{Application: appstatus.Application{Kind: appstatus.KindDocker, Identity: "api"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.History(context.Background(), "invalid"); !errors.Is(err, logstream.ErrInvalidCursor) {
		t.Fatalf("history error = %v", err)
	}
}

func TestRuntimeProtocolRejectsUnboundPrivilegedOperations(t *testing.T) {
	connection := clusterstatus.Connection{Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Mode: clusterstatus.ModeLimited}
	requests := []wireRequest{
		{Version: ProtocolVersion, Operation: operationApplicationOperate, RequestID: "runtime-invalid-1", Runtime: &runtimeWireRequest{ContainerName: "api", ContainerAction: appstatus.ContainerRestart}},
		{Version: ProtocolVersion, Operation: operationKubernetesOperate, RequestID: "runtime-invalid-2", SessionToken: strings.Repeat("s", 32), Runtime: &runtimeWireRequest{Kubernetes: &clusterstatus.Connection{Name: connection.Name, KubeconfigPath: connection.KubeconfigPath, Mode: clusterstatus.ModeObserve}, KubernetesOperation: &clusterstatus.Operation{Kind: clusterstatus.OperationRedeploy, WorkloadKey: "default/Deployment/api"}}},
		{Version: ProtocolVersion, Operation: operationApplicationSnapshot, RequestID: "runtime-invalid-3", Runtime: &runtimeWireRequest{Kubernetes: &connection}},
		{Version: ProtocolVersion, Operation: operationKubernetesOperate, RequestID: "runtime-invalid-4", SessionToken: strings.Repeat("s", 32), Runtime: &runtimeWireRequest{Kubernetes: &connection, KubernetesOperation: &clusterstatus.Operation{Kind: clusterstatus.OperationScale, WorkloadKey: "default/Deployment/api", Replicas: 1001}}},
		{Version: ProtocolVersion, Operation: operationKubernetesOperate, RequestID: "runtime-invalid-5", SessionToken: strings.Repeat("s", 32), Runtime: &runtimeWireRequest{Kubernetes: &connection, KubernetesOperation: &clusterstatus.Operation{Kind: clusterstatus.OperationRedeploy, WorkloadKey: "default/Deployment/api", Replicas: 1}}},
		{Version: ProtocolVersion, Operation: operationApplicationSnapshot, RequestID: "runtime-invalid-6", Runtime: &runtimeWireRequest{WorkloadKey: "default/Deployment/api"}},
	}
	for index, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("invalid runtime request %d was accepted", index)
		}
	}
}

func TestRuntimeMutationCapabilityCannotBeReplayed(t *testing.T) {
	applications := &fixtureApplicationService{}
	server, client := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "administrator"}}, &fixtureExecutor{})
	server.applications = applications
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "runtime-replay-1"})
	parameters, _ := json.Marshal(runtimeWireRequest{ContainerName: "api", ContainerAction: appstatus.ContainerRestart})
	capability, binding, err := client.authorize(ctx, ActionApplicationOperate, "api", "application-runtime-v1", parameters)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.execute(ctx, capability, binding, parameters); err != nil {
		t.Fatal(err)
	}
	if _, err := client.execute(ctx, capability, binding, parameters); err == nil || applications.calls != 1 {
		t.Fatalf("replay error=%v calls=%d", err, applications.calls)
	}
}

type failingRuntimeAuditor struct{}

func (failingRuntimeAuditor) Record(context.Context, AuditRecord) error {
	return errors.New("audit unavailable")
}

func TestRuntimeMutationFailsClosedBeforeExecutionWhenIntentAuditFails(t *testing.T) {
	applications := &fixtureApplicationService{}
	server, client := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "administrator"}}, &fixtureExecutor{})
	server.applications = applications
	server.auditor = failingRuntimeAuditor{}
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "runtime-audit-failure-1"})
	err := NewApplicationProbe(client).OperateContainer(ctx, "api", appstatus.ContainerRestart)
	if err == nil || applications.calls != 0 {
		t.Fatalf("operation error=%v calls=%d", err, applications.calls)
	}
}

type fixtureKubernetesFactory struct {
	client           *fixtureKubernetesClient
	connection       clusterstatus.Connection
	openCandidateErr error
}

func (factory fixtureKubernetesFactory) Open(context.Context, clusterstatus.Connection) (clusterstatus.Client, error) {
	return factory.client, nil
}

func (factory fixtureKubernetesFactory) ResolveConnection(_ context.Context, id string) (clusterstatus.Connection, bool, error) {
	return factory.connection, id != "" && id == factory.connection.ID, nil
}

func (factory fixtureKubernetesFactory) OpenCandidate(ctx context.Context, connection clusterstatus.Connection) (clusterstatus.Client, error) {
	if factory.openCandidateErr != nil {
		return nil, factory.openCandidateErr
	}
	return factory.Open(ctx, connection)
}

type fixtureKubernetesClient struct {
	snapshot        clusterstatus.Snapshot
	detail          clusterstatus.Detail
	logs            []clusterstatus.LogLine
	operated        clusterstatus.Operation
	capabilitiesErr error
}

func (client *fixtureKubernetesClient) Close() error { return nil }
func (client *fixtureKubernetesClient) Capabilities(context.Context) (clusterstatus.Capabilities, error) {
	if client.capabilitiesErr != nil {
		return clusterstatus.Capabilities{}, client.capabilitiesErr
	}
	return clusterstatus.Capabilities{Workloads: true, Nodes: true, Redeploy: true, Scale: true, RunCron: true}, nil
}
func (client *fixtureKubernetesClient) Fingerprint() string { return "cluster-fingerprint" }
func (client *fixtureKubernetesClient) Snapshot(context.Context) (clusterstatus.Snapshot, error) {
	return client.snapshot, nil
}
func (client *fixtureKubernetesClient) Detail(context.Context, string) (clusterstatus.Detail, error) {
	return client.detail, nil
}
func (client *fixtureKubernetesClient) Logs(context.Context, string, int) ([]clusterstatus.LogLine, error) {
	return client.logs, nil
}
func (client *fixtureKubernetesClient) Operate(_ context.Context, operation clusterstatus.Operation) error {
	client.operated = operation
	return nil
}

func TestKubernetesFactoryReadsClusterSnapshotThroughBroker(t *testing.T) {
	expected := clusterstatus.Snapshot{ServerVersion: "v1.35.0", Workloads: []clusterstatus.Workload{{Key: "default/Deployment/api", Name: "api"}}}
	connection := clusterstatus.Connection{ID: "k8s-local", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeObserve}
	server, brokerClient := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{client: &fixtureKubernetesClient{snapshot: expected}, connection: connection}
	defer server.Close()

	client, err := NewKubernetesFactory(brokerClient).Open(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if actual.ServerVersion != "v1.35.0" || len(actual.Workloads) != 1 || actual.Workloads[0].Name != "api" {
		t.Fatalf("broker Kubernetes snapshot = %#v", actual)
	}
}

func TestKubernetesOperationFailureReturnsCauseAndWritesDiagnosticLog(t *testing.T) {
	connection := clusterstatus.Connection{ID: "k8s-local", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeObserve}
	server, brokerClient := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{client: &fixtureKubernetesClient{capabilitiesErr: errors.New("Kubernetes POST selfsubjectaccessreviews returned 403 Forbidden: token=secret-value")}, connection: connection}
	var diagnostics bytes.Buffer
	server.errorLogger = log.New(&diagnostics, "", 0)
	defer server.Close()

	_, err := NewKubernetesFactory(brokerClient).Open(context.Background(), connection)
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("Kubernetes operation error = %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") || !strings.Contains(err.Error(), "token=[REDACTED]") {
		t.Fatalf("Kubernetes operation response was not redacted: %v", err)
	}
	logged := diagnostics.String()
	for _, expected := range []string{`operation="kubernetes_open"`, `stage="operation"`, `connection_id="k8s-local"`, `connection_name="local"`, `context="default"`, `candidate=false`, "403 Forbidden", "token=[REDACTED]"} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("Kubernetes diagnostic log missing %q: %s", expected, logged)
		}
	}
	if strings.Contains(logged, "secret-value") || strings.Contains(logged, connection.KubeconfigPath) {
		t.Fatalf("Kubernetes diagnostic log exposed a secret or kubeconfig path: %s", logged)
	}
}

func TestKubernetesCandidateOpenReturnsActionableCredentialGuidance(t *testing.T) {
	connection := clusterstatus.Connection{ID: "new-k8s", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeObserve}
	server, brokerClient := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "maintainer"}}, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{openCandidateErr: &clusterstatus.KubeconfigOpenError{
		Kind:  clusterstatus.KubeconfigRequiresEmbeddedCA,
		Cause: errors.New("new kubeconfig connections must embed certificate authority data"),
	}}
	defer server.Close()

	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "kubernetes-candidate-open"})
	_, err := NewKubernetesFactory(brokerClient).Open(ctx, connection)
	if err == nil || !strings.Contains(err.Error(), "embed certificate authority data") {
		t.Fatalf("candidate open error = %v", err)
	}
}

func TestKubernetesOpenFailureGuidanceDoesNotExposeUnknownBrokerDetails(t *testing.T) {
	message := kubernetesOpenFailureMessage(errors.New(`open C:\\private\\cluster.yaml: token=secret`))
	if strings.Contains(message, "private") || strings.Contains(message, "secret") || !strings.Contains(message, "embedded, supported credentials") {
		t.Fatalf("unsafe Kubernetes open guidance = %q", message)
	}
}

func TestKubernetesFactoryRejectsConnectionFieldSubstitution(t *testing.T) {
	configured := clusterstatus.Connection{ID: "k8s-local", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeObserve}
	server, brokerClient := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{client: &fixtureKubernetesClient{}, connection: configured}
	defer server.Close()

	tampered := configured
	tampered.KubeconfigPath = filepath.Join(t.TempDir(), "privileged.yaml")
	if _, err := NewKubernetesFactory(brokerClient).Open(context.Background(), tampered); err == nil {
		t.Fatal("Broker accepted substituted kubeconfig path")
	}
	tampered = configured
	tampered.Mode = clusterstatus.ModeLimited
	if _, err := NewKubernetesFactory(brokerClient).Open(context.Background(), tampered); err == nil {
		t.Fatal("Broker accepted substituted connection mode")
	}
}

func TestKubernetesFactoryReadsDetailAndLogsThroughBroker(t *testing.T) {
	connection := clusterstatus.Connection{ID: "k8s-local", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeObserve}
	fixture := &fixtureKubernetesClient{
		detail: clusterstatus.Detail{Workload: clusterstatus.Workload{Key: "default/Deployment/api", Name: "api"}},
		logs:   []clusterstatus.LogLine{{Pod: "api-1", Container: "api", Text: "ready"}},
	}
	server, brokerClient := brokerFixture(t, &fixtureAuthorizer{}, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{client: fixture, connection: connection}
	defer server.Close()

	client, err := NewKubernetesFactory(brokerClient).Open(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := client.Detail(context.Background(), "default/Deployment/api")
	if err != nil || detail.Workload.Name != "api" {
		t.Fatalf("detail = %#v, error = %v", detail, err)
	}
	logs, err := client.Logs(context.Background(), "default/Deployment/api", 100)
	if err != nil || len(logs) != 1 || logs[0].Text != "ready" {
		t.Fatalf("logs = %#v, error = %v", logs, err)
	}
}

func TestKubernetesFactoryRunsLimitedOperationThroughBroker(t *testing.T) {
	connection := clusterstatus.Connection{ID: "k8s-local", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeLimited}
	fixture := &fixtureKubernetesClient{snapshot: clusterstatus.Snapshot{Workloads: []clusterstatus.Workload{{Key: "default/Deployment/api", Kind: "Deployment", Desired: 2}}}}
	server, brokerClient := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "maintainer"}}, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{client: fixture, connection: connection}
	defer server.Close()

	client, err := NewKubernetesFactory(brokerClient).Open(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "kubernetes-operation-1"})
	operation := clusterstatus.Operation{Kind: clusterstatus.OperationRedeploy, WorkloadKey: "default/Deployment/api"}
	if err := client.Operate(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if fixture.operated.Kind != clusterstatus.OperationRedeploy || fixture.operated.WorkloadKey != operation.WorkloadKey {
		t.Fatalf("operation = %#v", fixture.operated)
	}
}

func TestKubernetesOperationAcceptsValidSessionWithoutRecentStepUp(t *testing.T) {
	now := time.Unix(1786957701, 0).UTC()
	database := openBrokerDatabase(t)
	security, err := NewDatabaseSecurity(database, auditlog.New(database), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := "kubernetes-session-token-0123456789"
	insertBrokerSession(t, database, token, "maintainer", now.Add(-4*time.Hour).Unix(), now.Add(time.Hour).Unix())
	connection := clusterstatus.Connection{ID: "k8s-local", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeLimited}
	fixture := &fixtureKubernetesClient{snapshot: clusterstatus.Snapshot{Workloads: []clusterstatus.Workload{{Key: "default/Deployment/api", Kind: "Deployment", Desired: 2}}}}
	server, brokerClient := brokerFixture(t, security, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{client: fixture, connection: connection}
	defer server.Close()

	client, err := NewKubernetesFactory(brokerClient).Open(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: token, RequestID: "kubernetes-session-test"})
	operation := clusterstatus.Operation{Kind: clusterstatus.OperationRedeploy, WorkloadKey: "default/Deployment/api"}
	if err := client.Operate(ctx, operation); err != nil {
		t.Fatalf("valid session could not operate Kubernetes workload: %v", err)
	}
	if fixture.operated.Kind != operation.Kind || fixture.operated.WorkloadKey != operation.WorkloadKey {
		t.Fatalf("operation = %#v", fixture.operated)
	}
}

func TestKubernetesFactoryRejectsScaleLargerThanOneStep(t *testing.T) {
	connection := clusterstatus.Connection{ID: "k8s-local", Name: "local", KubeconfigPath: filepath.Join(t.TempDir(), "kubeconfig.yaml"), Context: "default", Mode: clusterstatus.ModeLimited}
	fixture := &fixtureKubernetesClient{snapshot: clusterstatus.Snapshot{Workloads: []clusterstatus.Workload{{Key: "default/Deployment/api", Kind: "Deployment", Desired: 2}}}}
	server, brokerClient := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "maintainer"}}, &fixtureExecutor{})
	server.kubernetes = fixtureKubernetesFactory{client: fixture, connection: connection}
	defer server.Close()

	client, err := NewKubernetesFactory(brokerClient).Open(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "kubernetes-operation-invalid-step"})
	err = client.Operate(ctx, clusterstatus.Operation{Kind: clusterstatus.OperationScale, WorkloadKey: "default/Deployment/api", Replicas: 4})
	if err == nil || fixture.operated.Kind != "" {
		t.Fatalf("scale error=%v operation=%#v", err, fixture.operated)
	}
}

func TestBrokerRejectsKubernetesOperationForWrongWorkloadType(t *testing.T) {
	// Keep synthetic workload identifiers visibly test-only so secret scanning stays meaningful.
	const cronJobKey = "test/CronJob/job"
	for name, test := range map[string]struct {
		workload  clusterstatus.Workload
		operation clusterstatus.Operation
	}{
		"scale CronJob":    {clusterstatus.Workload{Key: cronJobKey, Kind: "CronJob", Desired: 1}, clusterstatus.Operation{Kind: clusterstatus.OperationScale, WorkloadKey: cronJobKey, Replicas: 2}},
		"redeploy CronJob": {clusterstatus.Workload{Key: cronJobKey, Kind: "CronJob"}, clusterstatus.Operation{Kind: clusterstatus.OperationRedeploy, WorkloadKey: cronJobKey}},
		"run Deployment":   {clusterstatus.Workload{Key: "default/Deployment/api", Kind: "Deployment"}, clusterstatus.Operation{Kind: clusterstatus.OperationRunCron, WorkloadKey: "default/Deployment/api"}},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fixtureKubernetesClient{snapshot: clusterstatus.Snapshot{Workloads: []clusterstatus.Workload{test.workload}}}
			if err := validateAndOperateKubernetes(context.Background(), client, test.operation); err == nil || client.operated.Kind != "" {
				t.Fatalf("operation error=%v executed=%#v", err, client.operated)
			}
		})
	}
}
