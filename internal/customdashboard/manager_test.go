package customdashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	manager, err := New(Options{DB: db, SecretsDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close(); db.Close() })
	return manager
}

func TestRegistryCardStoresCredentialOutsideDatabaseAndRefreshesMultipleImages(t *testing.T) {
	failing := false
	credential := "super-secret"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "robot$board" || password != credential {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/v2/team/api/tags/list":
			_, _ = response.Write([]byte(`{"tags":["1.2.0","1.3.0"]}`))
		case "/v2/team/web/tags/list":
			if failing {
				http.Error(response, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = response.Write([]byte(`{"tags":["2.0.0"]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	manager := testManager(t)
	ctx := context.Background()
	dashboard, _ := manager.CreateDashboard(ctx, DashboardInput{Name: "镜像", Slug: "images"})
	config, _ := json.Marshal(map[string]any{
		"endpoint": server.URL, "images": []string{"team/api", "team/web"},
		"authMode": "basic", "username": "robot$board",
	})
	card, err := manager.CreateCard(ctx, dashboard.ID, CardInput{
		Name: "生产镜像", Type: CardRegistry, Config: config, RefreshSeconds: 60,
		RegistryPassword: "super-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !card.CredentialConfigured {
		t.Fatal("credential should be reported as configured")
	}
	var storedConfig string
	if err := manager.db.QueryRow(`SELECT config_json FROM custom_dashboard_cards WHERE id=?`, card.ID).Scan(&storedConfig); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedConfig, "super-secret") {
		t.Fatal("registry password was stored in SQLite")
	}
	refreshed, err := manager.RefreshCard(ctx, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Snapshot.Images) != 2 || refreshed.Snapshot.Images[0].Tag != "1.3.0" || refreshed.Snapshot.Images[1].Tag != "2.0.0" {
		t.Fatalf("unexpected initial registry snapshot: %#v", refreshed.Snapshot.Images)
	}
	failing = true
	refreshed, err = manager.RefreshCard(ctx, card.ID)
	if err == nil {
		t.Fatal("partial registry failure should be returned")
	}
	if len(refreshed.Snapshot.Images) != 2 || refreshed.Snapshot.Images[1].Tag != "2.0.0" || refreshed.Snapshot.Images[1].Error == "" || !refreshed.Snapshot.Images[1].Stale {
		t.Fatalf("unexpected registry snapshot: %#v", refreshed.Snapshot.Images)
	}
	credential = "rotated-secret"
	if _, err := manager.UpdateCard(ctx, card.ID, CardInput{
		Name: "生产镜像", Type: CardRegistry, Config: config, RefreshSeconds: 300, RegistryPassword: credential,
	}); err != nil {
		t.Fatalf("rotate credential: %v", err)
	}
	updated, err := manager.UpdateCard(ctx, card.ID, CardInput{
		Name: "生产镜像", Type: CardRegistry, Config: config, RefreshSeconds: 300, PreserveRegistryPassword: true,
	})
	if err != nil || !updated.CredentialConfigured {
		t.Fatalf("blank edit did not preserve credential: card=%#v err=%v", updated, err)
	}
	if err := manager.DeleteCard(ctx, card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.secrets.get(card.ID); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("credential survived card deletion: %v", err)
	}
}

func TestRegistryCardImportedWithoutCredentialRequiresOneWhenEdited(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard, _ := manager.CreateDashboard(ctx, DashboardInput{Name: "镜像", Slug: "imported-images"})
	config := json.RawMessage(`{"endpoint":"http://registry.lan:5000","images":["team/api"],"authMode":"basic","username":"robot"}`)
	card, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "镜像版本", Type: CardRegistry, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateCard(ctx, card.ID, CardInput{Name: card.Name, Type: CardRegistry, Config: config, PreserveRegistryPassword: true}); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("missing imported credential accepted: %v", err)
	}
}

func TestPublicRegistryCardDoesNotExposeConnectionConfiguration(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	dashboard, _ := manager.CreateDashboard(ctx, DashboardInput{Name: "镜像", Slug: "images-public", Public: true})
	config := json.RawMessage(`{"endpoint":"http://registry.lan:5000","images":["team/api"],"authMode":"anonymous"}`)
	if _, err := manager.CreateCard(ctx, dashboard.ID, CardInput{Name: "镜像版本", Type: CardRegistry, Config: config}); err != nil {
		t.Fatal(err)
	}
	public, err := manager.GetPublicDashboard(ctx, dashboard.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(public.Cards) != 1 || string(public.Cards[0].Config) != "{}" {
		t.Fatalf("public connection config was exposed: %s", public.Cards[0].Config)
	}
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

func TestRefreshNumberCardKeepsStringValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"release":{"status":"版本 v1.2.3 / 生产环境"}}`))
	}))
	defer server.Close()

	manager := testManager(t)
	ctx := context.Background()
	dashboard, err := manager.CreateDashboard(ctx, DashboardInput{Name: "发布状态", Slug: "release-status"})
	if err != nil {
		t.Fatal(err)
	}
	card, err := manager.CreateCard(ctx, dashboard.ID, CardInput{
		Name: "当前版本", Type: CardNumber, SourceURL: server.URL, ValuePath: "release.status",
	})
	if err != nil {
		t.Fatal(err)
	}

	refreshed, err := manager.RefreshCard(ctx, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := refreshed.Snapshot.Value, "版本 v1.2.3 / 生产环境"; got != want {
		t.Fatalf("value=%#v, want %#v", got, want)
	}
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
