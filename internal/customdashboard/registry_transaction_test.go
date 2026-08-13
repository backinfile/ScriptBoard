package customdashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	_ "modernc.org/sqlite"

	"scriptboard/internal/registrymonitor"
)

func TestRegistryPrepareFailureLeavesSQLiteCardUnchanged(t *testing.T) {
	connections := newFixtureRegistryConnections()
	manager := testManagerWithRegistry(t, connections)
	ctx := context.Background()
	dashboard, err := manager.CreateDashboard(ctx, DashboardInput{Name: "Registry", Slug: "registry-transaction"})
	if err != nil {
		t.Fatal(err)
	}
	oldConfig := json.RawMessage(`{"endpoint":"http://old.registry","images":["team/api"],"authMode":"basic","username":"robot"}`)
	card, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "Image", Type: CardRegistry, Config: oldConfig})
	if err != nil {
		t.Fatal(err)
	}
	connections.prepareErr = errors.New("Broker unavailable")
	newConfig := json.RawMessage(`{"endpoint":"http://new.registry","images":["team/api"],"authMode":"basic","username":"robot"}`)
	if _, err := manager.UpdateCard(ctx, card.ID, CardInput{Name: "Changed", Type: CardRegistry, Config: newConfig, RegistryPassword: "secret"}); err == nil {
		t.Fatal("update succeeded after Registry prepare failure")
	}
	stored, err := manager.getCard(ctx, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Image" || string(stored.Config) != string(oldConfig) {
		t.Fatalf("SQLite card changed after prepare failure: %#v", stored)
	}
}

func TestRegistryCommitFailureIsRecoveredFromDurableOperationLog(t *testing.T) {
	connections := newFixtureRegistryConnections()
	connections.commitErr = errors.New("Broker interrupted")
	manager := testManagerWithRegistry(t, connections)
	ctx := context.Background()
	dashboard, err := manager.CreateDashboard(ctx, DashboardInput{Name: "Registry", Slug: "registry-recovery"})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"endpoint":"http://registry.test","images":["team/api"],"authMode":"anonymous"}`)
	if _, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "Image", Type: CardRegistry, Config: config}); err == nil {
		t.Fatal("create hid Registry commit failure")
	}
	var cardID string
	if err := manager.db.QueryRow(`SELECT id FROM custom_dashboard_cards WHERE dashboard_id=?`, dashboard.ID).Scan(&cardID); err != nil {
		t.Fatalf("SQLite transaction was not committed before Broker completion: %v", err)
	}
	var pending int
	if err := manager.db.QueryRow(`SELECT COUNT(*) FROM custom_dashboard_registry_operations`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending operations=%d err=%v", pending, err)
	}
	connections.commitErr = nil
	if err := manager.ReconcileRegistryOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if connections.active[cardID].Endpoint != "http://registry.test" {
		t.Fatalf("active Registry config=%#v", connections.active[cardID])
	}
	if err := manager.db.QueryRow(`SELECT COUNT(*) FROM custom_dashboard_registry_operations`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("reconciled operations=%d err=%v", pending, err)
	}
}

func testManagerWithRegistry(t *testing.T, connections RegistryConnections) *Manager {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	for _, statement := range SchemaStatements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := New(Options{DB: database, Client: &http.Client{}, RegistryConnections: connections, Paused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close(); _ = database.Close() })
	return manager
}

type fixtureRegistryConnections struct {
	pending    map[string]fixtureRegistryMutation
	active     map[string]registrymonitor.Config
	prepareErr error
	commitErr  error
}

type fixtureRegistryMutation struct {
	cardID string
	config registrymonitor.Config
	delete bool
}

func newFixtureRegistryConnections() *fixtureRegistryConnections {
	return &fixtureRegistryConnections{pending: map[string]fixtureRegistryMutation{}, active: map[string]registrymonitor.Config{}}
}

func (connections *fixtureRegistryConnections) Prepare(_ context.Context, operationID, cardID string, config registrymonitor.Config, _ string, _ bool) error {
	if connections.prepareErr != nil {
		return connections.prepareErr
	}
	connections.pending[operationID] = fixtureRegistryMutation{cardID: cardID, config: config}
	return nil
}
func (connections *fixtureRegistryConnections) PrepareDelete(_ context.Context, operationID, cardID string) error {
	if connections.prepareErr != nil {
		return connections.prepareErr
	}
	connections.pending[operationID] = fixtureRegistryMutation{cardID: cardID, delete: true}
	return nil
}
func (connections *fixtureRegistryConnections) Commit(_ context.Context, operationID string) error {
	if connections.commitErr != nil {
		return connections.commitErr
	}
	mutation, ok := connections.pending[operationID]
	if !ok {
		return nil
	}
	if mutation.delete {
		delete(connections.active, mutation.cardID)
	} else {
		connections.active[mutation.cardID] = mutation.config
	}
	delete(connections.pending, operationID)
	return nil
}
func (connections *fixtureRegistryConnections) Abort(_ context.Context, operationID string) error {
	delete(connections.pending, operationID)
	return nil
}
func (connections *fixtureRegistryConnections) Configured(_ context.Context, cardID string) (bool, error) {
	config, ok := connections.active[cardID]
	return ok && config.AuthMode == "basic", nil
}
func (connections *fixtureRegistryConnections) Inspect(context.Context, string) ([]registrymonitor.ImageResult, error) {
	return nil, nil
}
func (connections *fixtureRegistryConnections) Test(context.Context, string, registrymonitor.Config, string, bool) ([]registrymonitor.ImageResult, error) {
	return nil, nil
}
