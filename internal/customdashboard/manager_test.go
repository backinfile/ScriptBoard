package customdashboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

func testManager(t *testing.T) *Manager {
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
	manager, err := New(Options{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close(); db.Close() })
	return manager
}

func TestDashboardLifecycleKeepsCardsInsideTheirDashboard(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	first, err := manager.CreateDashboard(ctx, DashboardInput{Name: "API 与额度", Slug: "api-credits", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CreateDashboard(ctx, DashboardInput{Name: "基础设施", Slug: "infrastructure"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateCard(ctx, first.ID, CardInput{Name: "主模型额度", Type: CardQuota, SourceURL: "https://example.test/usage", ValuePath: "remaining", SecondaryPath: "limit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateCard(ctx, second.ID, CardInput{Name: "节点数", Type: CardNumber, SourceURL: "https://example.test/nodes", ValuePath: "nodes"}); err != nil {
		t.Fatal(err)
	}

	view, err := manager.GetDashboard(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Cards) != 1 || view.Cards[0].Name != "主模型额度" {
		t.Fatalf("unexpected cards: %#v", view.Cards)
	}
	public, err := manager.GetPublicDashboard(ctx, "api-credits")
	if err != nil || public.ID != first.ID {
		t.Fatalf("public dashboard: %#v %v", public, err)
	}
	if _, err := manager.GetPublicDashboard(ctx, "infrastructure"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("private dashboard exposed: %v", err)
	}

	if err := manager.DeleteDashboard(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetDashboard(ctx, first.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted dashboard still exists: %v", err)
	}
}

func TestRefreshCardEvaluatesBothValueExpressionsAndKeepsLastSuccessOnFailure(t *testing.T) {
	failing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"subscription":{"used":368,"limit":1000}}`))
	}))
	defer server.Close()
	manager := testManager(t)
	manager.client = server.Client()
	ctx := context.Background()
	dashboard, _ := manager.CreateDashboard(ctx, DashboardInput{Name: "API", Slug: "api"})
	card, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "剩余额度", Type: CardQuota, SourceURL: server.URL, ValuePath: "(subscription.limit - subscription.used) / subscription.limit * 100", SecondaryPath: "subscription.limit - subscription.used"})
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := manager.RefreshCard(ctx, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := refreshed.Snapshot.Number; got != 63.2 {
		t.Fatalf("number=%v", got)
	}
	if got := refreshed.Snapshot.Secondary; got != float64(632) {
		t.Fatalf("secondary=%v", got)
	}
	failing = true
	stale, err := manager.RefreshCard(ctx, card.ID)
	if err == nil {
		t.Fatal("expected refresh failure")
	}
	if stale.Snapshot.Number != 63.2 || !stale.Stale || stale.LastError == "" {
		t.Fatalf("last success not retained: %#v", stale)
	}
}

func TestDefaultDashboardClientBlocksLoopbackSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("blocked dashboard request reached loopback")
	}))
	defer server.Close()
	manager := testManager(t)
	dashboard, _ := manager.CreateDashboard(context.Background(), DashboardInput{Name: "Security", Slug: "security"})
	card, err := manager.CreateCard(context.Background(), dashboard.ID, CardInput{Name: "Blocked", Type: CardNumber, SourceURL: server.URL, ValuePath: "value"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshCard(context.Background(), card.ID); err == nil {
		t.Fatal("default dashboard client reached a loopback source")
	}
}

func TestDashboardClientRejectsRedirectBeforeForwardingCredentials(t *testing.T) {
	var targetRequests int
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetRequests++ }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer dashboard-secret" {
			t.Errorf("source authorization = %q", request.Header.Get("Authorization"))
		}
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer source.Close()
	manager := testManagerWithClient(t, source.Client())
	dashboard, _ := manager.CreateDashboard(context.Background(), DashboardInput{Name: "Security", Slug: "security"})
	card, err := manager.CreateCard(context.Background(), dashboard.ID, CardInput{
		Name: "Redirect", Type: CardNumber, SourceURL: source.URL, ValuePath: "value",
		Headers: map[string]string{"Authorization": "Bearer dashboard-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshCard(context.Background(), card.ID); err == nil {
		t.Fatal("dashboard redirect was accepted")
	}
	if targetRequests != 0 {
		t.Fatalf("dashboard credential crossed redirect origin: requests = %d", targetRequests)
	}
}

func TestDashboardCardRejectsURLCredentialsAndReservedHeaders(t *testing.T) {
	manager := testManager(t)
	dashboard, _ := manager.CreateDashboard(context.Background(), DashboardInput{Name: "Security", Slug: "security"})
	for _, input := range []CardInput{
		{Name: "URL credential", Type: CardNumber, SourceURL: "https://user:secret@example.com/data", ValuePath: "value"},
		{Name: "Proxy auth", Type: CardNumber, SourceURL: "https://example.com/data", ValuePath: "value", Headers: map[string]string{"Proxy-Authorization": "Basic secret"}},
		{Name: "Host override", Type: CardNumber, SourceURL: "https://example.com/data", ValuePath: "value", Headers: map[string]string{"Host": "metadata.internal"}},
	} {
		if _, err := manager.CreateCard(context.Background(), dashboard.ID, input); err == nil {
			t.Fatalf("unsafe dashboard card was accepted: %+v", input)
		}
	}
}

func testManagerWithClient(t *testing.T, client *http.Client) *Manager {
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
	manager, err := New(Options{DB: db, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close(); db.Close() })
	return manager
}

func TestExtractSupportsArrayAndObjectPaths(t *testing.T) {
	value := map[string]any{"subscriptions": []any{map[string]any{"subscription": map[string]any{"amount_used": float64(12)}}}}
	got, err := Extract(value, "subscriptions[0].subscription.amount_used")
	if err != nil {
		t.Fatal(err)
	}
	if got != float64(12) {
		t.Fatalf("got %#v", got)
	}
}

func TestMoveCardChangesOnlyItsDashboardOrder(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard, err := manager.CreateDashboard(ctx, DashboardInput{Name: "排序面板", Slug: "ordered"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.CreateDashboard(ctx, DashboardInput{Name: "其他面板", Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "第一项", Type: CardNumber, SourceURL: "https://example.test/first", ValuePath: "value"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "第二项", Type: CardNumber, SourceURL: "https://example.test/second", ValuePath: "value"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "第三项", Type: CardNumber, SourceURL: "https://example.test/third", ValuePath: "value"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateCard(ctx, other.ID, CardInput{Name: "不参与排序", Type: CardNumber, SourceURL: "https://example.test/other", ValuePath: "value"}); err != nil {
		t.Fatal(err)
	}

	if dashboardID, err := manager.MoveCard(ctx, third.ID, -1); err != nil || dashboardID != dashboard.ID {
		t.Fatalf("move third up: dashboard=%q err=%v", dashboardID, err)
	}
	view, err := manager.GetDashboard(ctx, dashboard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{view.Cards[0].ID, view.Cards[1].ID, view.Cards[2].ID}; got[0] != first.ID || got[1] != third.ID || got[2] != second.ID {
		t.Fatalf("unexpected order after moving up: %v", got)
	}
	if _, err := manager.MoveCard(ctx, first.ID, 1); err != nil {
		t.Fatal(err)
	}
	view, err = manager.GetDashboard(ctx, dashboard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{view.Cards[0].ID, view.Cards[1].ID, view.Cards[2].ID}; got[0] != third.ID || got[1] != first.ID || got[2] != second.ID {
		t.Fatalf("unexpected order after moving down: %v", got)
	}
	otherView, err := manager.GetDashboard(ctx, other.ID)
	if err != nil || len(otherView.Cards) != 1 || otherView.Cards[0].Name != "不参与排序" {
		t.Fatalf("other dashboard changed: %#v err=%v", otherView.Cards, err)
	}
	if _, err := manager.MoveCard(ctx, first.ID, 0); err == nil {
		t.Fatal("invalid direction was accepted")
	}
}
