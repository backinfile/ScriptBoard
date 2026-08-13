package websitemonitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNetworkProbeSendsCustomHeadersForHTTPAndWebSocket(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer monitor-token" || request.Header.Get("X-Tenant") != "north" || request.Host != "health.internal" {
				http.Error(response, "missing headers", http.StatusUnauthorized)
				return
			}
			response.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		result := (NetworkProbe{}).Check(context.Background(), Config{
			Scope: ScopeLocal, Kind: KindHTTP, URL: server.URL, HTTPMethod: http.MethodGet, Timeout: time.Second,
			RequestHeaders: []RequestHeader{{Name: "Authorization", Value: "Bearer monitor-token"}, {Name: "X-Tenant", Value: "north"}, {Name: "Host", Value: "health.internal"}},
		})
		if !result.Success || result.StatusCode != http.StatusNoContent {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("websocket", func(t *testing.T) {
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer socket-token" {
				http.Error(response, "missing header", http.StatusUnauthorized)
				return
			}
			connection, err := upgrader.Upgrade(response, request, nil)
			if err == nil {
				_ = connection.Close()
			}
		}))
		defer server.Close()

		result := (NetworkProbe{}).Check(context.Background(), Config{
			Scope: ScopeLocal, Kind: KindWebSocket, URL: "ws" + strings.TrimPrefix(server.URL, "http"), Timeout: time.Second,
			WebSocketSuccess: WebSocketHandshake,
			RequestHeaders:   []RequestHeader{{Name: "Authorization", Value: "Bearer socket-token"}},
		})
		if !result.Success {
			t.Fatalf("result = %#v", result)
		}
	})
}

func TestNetworkProbeDoesNotRedirectCredentialedRequests(t *testing.T) {
	t.Parallel()

	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	result := (NetworkProbe{}).Check(context.Background(), Config{
		Scope: ScopeLocal, Kind: KindHTTP, URL: redirect.URL, HTTPMethod: http.MethodGet, Timeout: time.Second,
		RequestHeaders: []RequestHeader{{Name: "Authorization", Value: "Bearer monitor-token"}},
	})
	if result.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want redirect response", result.StatusCode)
	}
	if targetRequests.Load() != 0 {
		t.Fatal("credentialed website probe followed a redirect")
	}
}
