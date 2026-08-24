package clusterstatus

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type fakeFactory struct {
	client Client
	err    error
	opened []Connection
}

type connectionFactory struct {
	clients map[string]Client
}

func (factory connectionFactory) Open(_ context.Context, connection Connection) (Client, error) {
	return factory.clients[connection.KubeconfigPath], nil
}

func (factory *fakeFactory) Open(_ context.Context, connection Connection) (Client, error) {
	factory.opened = append(factory.opened, connection)
	return factory.client, factory.err
}

type fakeClient struct {
	capabilities Capabilities
	fingerprint  string
	snapshot     Snapshot
	details      map[string]Detail
	operations   []Operation
	closed       int
}

func (client *fakeClient) Close() error { client.closed++; return nil }
func (client *fakeClient) Capabilities(context.Context) (Capabilities, error) {
	return client.capabilities, nil
}
func (client *fakeClient) Fingerprint() string                        { return client.fingerprint }
func (client *fakeClient) Snapshot(context.Context) (Snapshot, error) { return client.snapshot, nil }
func (client *fakeClient) Detail(_ context.Context, key string) (Detail, error) {
	return client.details[key], nil
}
func (client *fakeClient) Logs(context.Context, string, int) ([]LogLine, error) { return nil, nil }
func (client *fakeClient) Operate(_ context.Context, operation Operation) error {
	client.operations = append(client.operations, operation)
	return nil
}

func testManager(t *testing.T, factory Factory) *Manager {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := New(Options{DB: db, Factory: factory, Now: func() time.Time {
		return time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close(); _ = db.Close() })
	return manager
}

func TestSaveConnectionTestsAndPersistsACluster(t *testing.T) {
	client := &fakeClient{
		fingerprint:  "sha256:cluster-one",
		capabilities: Capabilities{Workloads: true, Nodes: true, Metrics: true, Logs: true, Redeploy: true, Scale: true},
	}
	factory := &fakeFactory{client: client}
	manager := testManager(t, factory)

	result, err := manager.SaveConnection(context.Background(), Connection{
		Name: "  edge-home  ", KubeconfigPath: "  /etc/scriptboard/kubeconfig  ", Context: " default ", Mode: ModeLimited,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Connected || result.Fingerprint != client.fingerprint || !result.Capabilities.Scale {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(factory.opened) != 1 || factory.opened[0].Name != "edge-home" || factory.opened[0].Context != "default" {
		t.Fatalf("connection was not normalized before testing: %#v", factory.opened)
	}

	stored, ok, err := manager.Connection(context.Background(), result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || stored.Name != "edge-home" || stored.KubeconfigPath != "/etc/scriptboard/kubeconfig" || stored.Mode != ModeLimited {
		t.Fatalf("stored connection: %#v ok=%v", stored, ok)
	}
}

func TestConnectionTestDoesNotReplaceConfiguredCluster(t *testing.T) {
	client := &fakeClient{fingerprint: "sha256:first", capabilities: Capabilities{Workloads: true}}
	manager := testManager(t, &fakeFactory{client: client})
	ctx := context.Background()
	saved, err := manager.SaveConnection(ctx, Connection{Name: "first", KubeconfigPath: "/first", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	client.fingerprint = "sha256:candidate"
	result, err := manager.TestConnection(ctx, Connection{Name: "candidate", KubeconfigPath: "/candidate", Mode: ModeLimited})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Connected || result.Fingerprint != "sha256:candidate" {
		t.Fatalf("test result = %#v", result)
	}
	stored, ok, err := manager.Connection(ctx, saved.ID)
	if err != nil || !ok || stored.Name != "first" {
		t.Fatalf("stored connection = %#v, ok=%v, err=%v", stored, ok, err)
	}
}

func TestMultipleConnectionsKeepSnapshotsAndHistoryIndependent(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	production := &fakeClient{fingerprint: "sha256:production", capabilities: Capabilities{Workloads: true}, snapshot: Snapshot{
		CollectedAt: now, Workloads: []Workload{{Key: "default/Deployment/api", Namespace: "default", Kind: "Deployment", Name: "api", Image: "api:production", Status: "ready", Ready: 2, Desired: 2}},
	}}
	staging := &fakeClient{fingerprint: "sha256:staging", capabilities: Capabilities{Workloads: true}, snapshot: Snapshot{
		CollectedAt: now, Workloads: []Workload{{Key: "default/Deployment/api", Namespace: "default", Kind: "Deployment", Name: "api", Image: "api:staging", Status: "ready", Ready: 1, Desired: 1}},
	}}
	manager := testManager(t, connectionFactory{clients: map[string]Client{"/production": production, "/staging": staging}})
	ctx := context.Background()
	productionStatus, err := manager.SaveConnection(ctx, Connection{Name: "production", KubeconfigPath: "/production", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	stagingStatus, err := manager.SaveConnection(ctx, Connection{Name: "staging", KubeconfigPath: "/staging", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	if productionStatus.ID == "" || stagingStatus.ID == "" || productionStatus.ID == stagingStatus.ID {
		t.Fatalf("connection IDs are not stable and distinct: production=%q staging=%q", productionStatus.ID, stagingStatus.ID)
	}
	if err := manager.Refresh(ctx, productionStatus.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx, stagingStatus.ID); err != nil {
		t.Fatal(err)
	}
	productionView, err := manager.View(ctx, productionStatus.ID, Query{})
	if err != nil {
		t.Fatal(err)
	}
	stagingView, err := manager.View(ctx, stagingStatus.ID, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if got := productionView.Workloads[0].Image; got != "api:production" {
		t.Fatalf("production image = %q", got)
	}
	if got := stagingView.Workloads[0].Image; got != "api:staging" {
		t.Fatalf("staging image = %q", got)
	}
	productionDetail, err := manager.Detail(ctx, productionStatus.ID, "default/Deployment/api")
	if err != nil {
		t.Fatal(err)
	}
	stagingDetail, err := manager.Detail(ctx, stagingStatus.ID, "default/Deployment/api")
	if err != nil {
		t.Fatal(err)
	}
	if productionDetail.Versions[0].Image != "api:production" || stagingDetail.Versions[0].Image != "api:staging" {
		t.Fatalf("history crossed connections: production=%#v staging=%#v", productionDetail.Versions, stagingDetail.Versions)
	}
}

func TestReplacingConnectionClearsClusterBoundHistory(t *testing.T) {
	client := &fakeClient{fingerprint: "sha256:first", capabilities: Capabilities{Workloads: true}}
	factory := &fakeFactory{client: client}
	manager := testManager(t, factory)
	ctx := context.Background()
	saved, err := manager.SaveConnection(ctx, Connection{Name: "first", KubeconfigPath: "/first", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.SaveConnection(ctx, Connection{Name: "other", KubeconfigPath: "/other", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO kubernetes_versions (connection_id, workload_key, observed_at, image, revision) VALUES ('` + saved.ID + `','production/Deployment/api',1,'api:v1','rev 1')`,
		`INSERT INTO kubernetes_metric_minutes (connection_id, workload_key, bucket_at, cpu_millicores, memory_bytes, ready, desired, restarts) VALUES ('` + saved.ID + `','production/Deployment/api',1,10,20,1,1,0)`,
		`INSERT INTO kubernetes_versions (connection_id, workload_key, observed_at, image, revision) VALUES ('` + other.ID + `','production/Deployment/api',1,'api:other','rev other')`,
		`INSERT INTO kubernetes_metric_minutes (connection_id, workload_key, bucket_at, cpu_millicores, memory_bytes, ready, desired, restarts) VALUES ('` + other.ID + `','production/Deployment/api',1,10,20,1,1,0)`,
	} {
		if _, err := manager.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	client.fingerprint = "sha256:second"
	saved.Name = "second"
	saved.KubeconfigPath = "/second"
	if _, err := manager.SaveConnection(ctx, saved.Connection); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"kubernetes_versions", "kubernetes_metric_minutes"} {
		var count int
		if err := manager.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE connection_id=?", saved.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d error=%v", table, count, err)
		}
		if err := manager.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE connection_id=?", other.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s other connection count=%d error=%v", table, count, err)
		}
	}
}

func TestDeleteConnectionClearsHistoryAndRuntime(t *testing.T) {
	client := &fakeClient{fingerprint: "sha256:delete", capabilities: Capabilities{Workloads: true}}
	manager := testManager(t, &fakeFactory{client: client})
	ctx := context.Background()
	saved, err := manager.SaveConnection(ctx, Connection{Name: "obsolete", KubeconfigPath: "/obsolete", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO kubernetes_versions (connection_id, workload_key, observed_at, image, revision) VALUES (?, 'default/Deployment/api', 1, 'api:v1', 'rev 1')`,
		`INSERT INTO kubernetes_metric_minutes (connection_id, workload_key, bucket_at, cpu_millicores, memory_bytes, ready, desired, restarts) VALUES (?, 'default/Deployment/api', 1, 10, 20, 1, 1, 0)`,
	} {
		if _, err := manager.db.ExecContext(ctx, statement, saved.ID); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := manager.DeleteConnection(ctx, saved.ID)
	if err != nil || !deleted {
		t.Fatalf("delete connection: deleted=%v err=%v", deleted, err)
	}
	if _, ok, err := manager.Connection(ctx, saved.ID); err != nil || ok {
		t.Fatalf("deleted connection still exists: ok=%v err=%v", ok, err)
	}
	for _, table := range []string{"kubernetes_versions", "kubernetes_metric_minutes"} {
		var count int
		if err := manager.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE connection_id=?", saved.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d error=%v", table, count, err)
		}
	}
	if client.closed != 1 {
		t.Fatalf("runtime client close count=%d", client.closed)
	}
	if deleted, err := manager.DeleteConnection(ctx, saved.ID); err != nil || deleted {
		t.Fatalf("delete missing connection: deleted=%v err=%v", deleted, err)
	}
}

func TestWorkloadKeepsVersionHistoryAcrossRollouts(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	client := &fakeClient{fingerprint: "sha256:cluster", capabilities: Capabilities{Workloads: true}, snapshot: Snapshot{
		CollectedAt: now, Workloads: []Workload{{Key: "production/Deployment/api", Namespace: "production", Kind: "Deployment", Name: "api", Image: "api:v1", Revision: "rev 1", Status: "ready", Ready: 2, Desired: 2}},
	}}
	manager := testManager(t, &fakeFactory{client: client})
	ctx := context.Background()
	saved, err := manager.SaveConnection(ctx, Connection{Name: "cluster", KubeconfigPath: "/cluster", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	view, err := manager.View(ctx, saved.ID, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Workloads) != 1 || view.Workloads[0].Image != "api:v1" {
		t.Fatalf("current workload state = %#v", view.Workloads)
	}
	client.snapshot.Workloads[0].Image = "api:v2"
	client.snapshot.Workloads[0].Revision = "rev 2"
	client.snapshot.CollectedAt = now.Add(time.Minute)
	manager.now = func() time.Time { return now.Add(time.Minute) }
	if err := manager.Refresh(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	view, err = manager.View(ctx, saved.ID, Query{Sort: "name", Direction: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Workloads) != 1 || view.Workloads[0].Image != "api:v2" {
		t.Fatalf("current workload: %#v", view.Workloads)
	}
	detail, err := manager.Detail(ctx, saved.ID, "production/Deployment/api")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Versions) != 2 || detail.Versions[0].Image != "api:v2" || detail.Versions[1].Image != "api:v1" {
		t.Fatalf("versions: %#v", detail.Versions)
	}
}

func TestFilteredViewKeepsAllNamespaceOptionsAndHidesConnectionPath(t *testing.T) {
	client := &fakeClient{fingerprint: "sha256:cluster", capabilities: Capabilities{Workloads: true}, snapshot: Snapshot{Workloads: []Workload{
		{Key: "production/Deployment/api", Namespace: "production", Kind: "Deployment", Name: "api", Status: "ready"},
		{Key: "operations/CronJob/backup", Namespace: "operations", Kind: "CronJob", Name: "backup", Status: "degraded"},
	}}}
	manager := testManager(t, &fakeFactory{client: client})
	ctx := context.Background()
	saved, err := manager.SaveConnection(ctx, Connection{Name: "cluster", KubeconfigPath: "/private/kubeconfig", Mode: ModeObserve})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	view, err := manager.View(ctx, saved.ID, Query{Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Workloads) != 1 || len(view.AvailableNamespaces) != 2 || view.AvailableNamespaces[0] != "operations" || view.AvailableNamespaces[1] != "production" {
		t.Fatalf("filtered view = %#v", view)
	}
	if view.Connection.KubeconfigPath != "" || view.Connection.Fingerprint != "" {
		t.Fatalf("public connection leaked private metadata: %#v", view.Connection)
	}
}

func TestWorkloadsSortByNodeName(t *testing.T) {
	workloads := []Workload{
		{Key: "default/Deployment/api", Nodes: "worker-02"},
		{Key: "default/Deployment/web", Nodes: "worker-01"},
	}
	sortWorkloads(workloads, "node", "asc")
	if workloads[0].Nodes != "worker-01" || workloads[1].Nodes != "worker-02" {
		t.Fatalf("workloads sorted by node = %#v", workloads)
	}
}

func TestLimitedOperationsAllowOnlyOneReplicaStep(t *testing.T) {
	client := &fakeClient{fingerprint: "sha256:cluster", capabilities: Capabilities{Workloads: true, Scale: true}, snapshot: Snapshot{
		CollectedAt: time.Now(), Workloads: []Workload{{Key: "production/Deployment/api", Namespace: "production", Kind: "Deployment", Name: "api", Desired: 2, Ready: 2}},
	}}
	manager := testManager(t, &fakeFactory{client: client})
	ctx := context.Background()
	saved, err := manager.SaveConnection(ctx, Connection{Name: "cluster", KubeconfigPath: "/cluster", Mode: ModeLimited})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Operate(ctx, saved.ID, Operation{Kind: OperationScale, WorkloadKey: "production/Deployment/api", Replicas: 5}); err == nil {
		t.Fatal("large replica change was accepted")
	}
	if err := manager.Operate(ctx, saved.ID, Operation{Kind: OperationScale, WorkloadKey: "production/Deployment/api", Replicas: 3}); err != nil {
		t.Fatal(err)
	}
	if len(client.operations) != 1 || client.operations[0].Replicas != 3 {
		t.Fatalf("operations: %#v", client.operations)
	}
}
