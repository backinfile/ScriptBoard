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
}

func (client *fakeClient) Close() error { return nil }
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

func TestSaveConnectionTestsAndPersistsTheOnlyCluster(t *testing.T) {
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

	stored, ok, err := manager.Connection(context.Background())
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
	if _, err := manager.SaveConnection(ctx, Connection{Name: "first", KubeconfigPath: "/first", Mode: ModeObserve}); err != nil {
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
	stored, ok, err := manager.Connection(ctx)
	if err != nil || !ok || stored.Name != "first" {
		t.Fatalf("stored connection = %#v, ok=%v, err=%v", stored, ok, err)
	}
}

func TestReplacingConnectionClearsClusterBoundHistory(t *testing.T) {
	client := &fakeClient{fingerprint: "sha256:first", capabilities: Capabilities{Workloads: true}}
	factory := &fakeFactory{client: client}
	manager := testManager(t, factory)
	ctx := context.Background()
	if _, err := manager.SaveConnection(ctx, Connection{Name: "first", KubeconfigPath: "/first", Mode: ModeObserve}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO kubernetes_pins (workload_key, namespace, kind, name, sort_order, created_at, updated_at) VALUES ('production/Deployment/api','production','Deployment','api',1,1,1)`,
		`INSERT INTO kubernetes_versions (workload_key, observed_at, image, revision) VALUES ('production/Deployment/api',1,'api:v1','rev 1')`,
		`INSERT INTO kubernetes_metric_minutes (workload_key, bucket_at, cpu_millicores, memory_bytes, ready, desired, restarts) VALUES ('production/Deployment/api',1,10,20,1,1,0)`,
	} {
		if _, err := manager.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	client.fingerprint = "sha256:second"
	if _, err := manager.SaveConnection(ctx, Connection{Name: "second", KubeconfigPath: "/second", Mode: ModeObserve}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"kubernetes_pins", "kubernetes_versions", "kubernetes_metric_minutes"} {
		var count int
		if err := manager.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d error=%v", table, count, err)
		}
	}
}

func TestPinnedWorkloadKeepsVersionHistoryAcrossRollouts(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	client := &fakeClient{fingerprint: "sha256:cluster", capabilities: Capabilities{Workloads: true}, snapshot: Snapshot{
		CollectedAt: now, Workloads: []Workload{{Key: "production/Deployment/api", Namespace: "production", Kind: "Deployment", Name: "api", Image: "api:v1", Revision: "rev 1", Status: "ready", Ready: 2, Desired: 2}},
	}}
	manager := testManager(t, &fakeFactory{client: client})
	ctx := context.Background()
	if _, err := manager.SaveConnection(ctx, Connection{Name: "cluster", KubeconfigPath: "/cluster", Mode: ModeObserve}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Pin(ctx, "production/Deployment/api"); err != nil {
		t.Fatal(err)
	}
	view, err := manager.View(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Workloads) != 1 || !view.Workloads[0].Pinned {
		t.Fatalf("current workload pin state = %#v", view.Workloads)
	}
	client.snapshot.Workloads[0].Image = "api:v2"
	client.snapshot.Workloads[0].Revision = "rev 2"
	client.snapshot.CollectedAt = now.Add(time.Minute)
	manager.now = func() time.Time { return now.Add(time.Minute) }
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	view, err = manager.View(ctx, Query{Sort: "name", Direction: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pinned) != 1 || view.Pinned[0].Image != "api:v2" || !view.Pinned[0].Pinned {
		t.Fatalf("pinned workload: %#v", view.Pinned)
	}
	detail, err := manager.Detail(ctx, "production/Deployment/api")
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
	if _, err := manager.SaveConnection(ctx, Connection{Name: "cluster", KubeconfigPath: "/private/kubeconfig", Mode: ModeObserve}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := manager.View(ctx, Query{Status: "ready"})
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

func TestLimitedOperationsAllowOnlyOneReplicaStep(t *testing.T) {
	client := &fakeClient{fingerprint: "sha256:cluster", capabilities: Capabilities{Workloads: true, Scale: true}, snapshot: Snapshot{
		CollectedAt: time.Now(), Workloads: []Workload{{Key: "production/Deployment/api", Namespace: "production", Kind: "Deployment", Name: "api", Desired: 2, Ready: 2}},
	}}
	manager := testManager(t, &fakeFactory{client: client})
	ctx := context.Background()
	if _, err := manager.SaveConnection(ctx, Connection{Name: "cluster", KubeconfigPath: "/cluster", Mode: ModeLimited}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Operate(ctx, Operation{Kind: OperationScale, WorkloadKey: "production/Deployment/api", Replicas: 5}); err == nil {
		t.Fatal("large replica change was accepted")
	}
	if err := manager.Operate(ctx, Operation{Kind: OperationScale, WorkloadKey: "production/Deployment/api", Replicas: 3}); err != nil {
		t.Fatal(err)
	}
	if len(client.operations) != 1 || client.operations[0].Replicas != 3 {
		t.Fatalf("operations: %#v", client.operations)
	}
}
