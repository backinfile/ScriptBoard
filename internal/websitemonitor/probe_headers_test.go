package websitemonitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
			Kind: KindHTTP, URL: server.URL, HTTPMethod: http.MethodGet, Timeout: time.Second,
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
			Kind: KindWebSocket, URL: "ws" + strings.TrimPrefix(server.URL, "http"), Timeout: time.Second,
			WebSocketSuccess: WebSocketHandshake,
			RequestHeaders:   []RequestHeader{{Name: "Authorization", Value: "Bearer socket-token"}},
		})
		if !result.Success {
			t.Fatalf("result = %#v", result)
		}
	})
}
