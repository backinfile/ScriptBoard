package fleetstatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/hoststatus"
	"scriptboard/internal/secretstore"
)

func openTestManager(t *testing.T, client *http.Client, now func() time.Time) *Manager {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "fleet.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	vault, err := secretstore.New(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(Options{DB: db, SecretStore: vault, Client: client, Now: now, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		manager.Close()
		_ = db.Close()
	})
	return manager
}

func TestAccessTokenAuthenticatesAndCanBeRevoked(t *testing.T) {
	manager := openTestManager(t, nil, time.Now)
	issued, secret, err := manager.CreateAccessToken(context.Background(), "Overview hub")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || issued.Hint == "" || issued.Label != "Overview hub" {
		t.Fatalf("issued token = %#v secret=%q", issued, secret)
	}
	if !manager.AuthenticateAccessToken(context.Background(), secret) {
		t.Fatal("issued token did not authenticate")
	}
	if manager.AuthenticateAccessToken(context.Background(), secret+"tampered") {
		t.Fatal("tampered token authenticated")
	}
	if err := manager.RevokeAccessToken(context.Background(), issued.ID); err != nil {
		t.Fatal(err)
	}
	if manager.AuthenticateAccessToken(context.Background(), secret) {
		t.Fatal("revoked token authenticated")
	}
}

func TestAddPeerFetchesAndPersistsBoundedOverview(t *testing.T) {
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	remote := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != ExportPath || request.Header.Get("Authorization") != "Bearer remote-secret-value" {
			http.Error(response, "forbidden", http.StatusForbidden)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(Export{
			ProtocolVersion: ProtocolVersion,
			Overview: hoststatus.Overview{
				Facts:       hoststatus.Facts{Hostname: "prod-web-01", Platform: "Ubuntu", PlatformVersion: "24.04", Architecture: "amd64"},
				Current:     hoststatus.Sample{At: now, CPU: &hoststatus.CPU{UsedPercent: 41.2}, Memory: &hoststatus.Memory{UsedPercent: 72, TotalBytes: 16 << 30}, Storage: &hoststatus.StorageSummary{UsedPercent: 38}, Network: &hoststatus.NetworkSummary{ReceivedBytesPerSecond: 2048}},
				CollectedAt: now,
			},
		})
	}))
	defer remote.Close()

	manager := openTestManager(t, remote.Client(), func() time.Time { return now })
	peer, err := manager.AddPeer(context.Background(), AddPeerInput{Name: "Production", Endpoint: remote.URL, AccessToken: "remote-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	if peer.Overview.Facts.Hostname != "prod-web-01" || peer.LastError != "" || peer.LastSeenAt.IsZero() {
		t.Fatalf("peer = %#v", peer)
	}
	peers, err := manager.ListPeers(context.Background())
	if err != nil || len(peers) != 1 || peers[0].Overview.Current.CPU.UsedPercent != 41.2 {
		t.Fatalf("peers=%#v err=%v", peers, err)
	}
}

func TestAddPeerSupportsTLS(t *testing.T) {
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != ExportPath || request.Header.Get("Authorization") != "Bearer tls-secret-value" {
			http.Error(response, "forbidden", http.StatusForbidden)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(Export{ProtocolVersion: ProtocolVersion, Overview: hoststatus.Overview{
			Facts: hoststatus.Facts{Hostname: "secure-node"}, Current: hoststatus.Sample{At: now, CPU: &hoststatus.CPU{UsedPercent: 8}}, CollectedAt: now,
		}})
	}))
	defer remote.Close()

	manager := openTestManager(t, remote.Client(), func() time.Time { return now })
	peer, err := manager.AddPeer(context.Background(), AddPeerInput{Name: "Secure node", Endpoint: remote.URL, AccessToken: "tls-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	if peer.Overview.Facts.Hostname != "secure-node" || peer.LastError != "" {
		t.Fatalf("peer = %#v", peer)
	}
}

func TestRefreshFailureKeepsLastSuccessfulSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	failing := false
	remote := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if failing {
			http.Error(response, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(Export{ProtocolVersion: ProtocolVersion, Overview: hoststatus.Overview{
			Facts: hoststatus.Facts{Hostname: "node-a"}, Current: hoststatus.Sample{At: now, CPU: &hoststatus.CPU{UsedPercent: 12}}, CollectedAt: now,
		}})
	}))
	defer remote.Close()

	manager := openTestManager(t, remote.Client(), func() time.Time { return now })
	peer, err := manager.AddPeer(context.Background(), AddPeerInput{Name: "Node A", Endpoint: remote.URL, AccessToken: "long-enough-secret"})
	if err != nil {
		t.Fatal(err)
	}
	failing = true
	now = now.Add(time.Minute)
	if err := manager.RefreshPeer(context.Background(), peer.ID); err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	peers, err := manager.ListPeers(context.Background())
	if err != nil || len(peers) != 1 {
		t.Fatalf("peers=%#v err=%v", peers, err)
	}
	if peers[0].Overview.Current.CPU == nil || peers[0].Overview.Current.CPU.UsedPercent != 12 || peers[0].LastError == "" {
		t.Fatalf("failed refresh discarded the last snapshot: %#v", peers[0])
	}
}
