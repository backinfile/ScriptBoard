package registrymonitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInspectSupportsBasicAuthenticationAndSelectsHighestSemanticVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "robot$dashboard" || password != "secret" {
			response.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.URL.Path != "/v2/team/api/tags/list" {
			http.NotFound(response, request)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"name": "team/api", "tags": []string{"latest", "v1.9.0", "1.10.0", "dev"}})
	}))
	defer server.Close()

	results, err := New(server.Client()).Inspect(context.Background(), Config{
		Endpoint: server.URL, Images: []string{"team/api"}, Username: "robot$dashboard", Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Tag != "1.10.0" || results[0].Error != "" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestInspectExchangesBearerTokenUsingConfiguredCredentials(t *testing.T) {
	var registryURL string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			username, password, ok := request.BasicAuth()
			if !ok || username != "alice" || password != "token-secret" || request.URL.Query().Get("scope") != "repository:library/nginx:pull" || request.URL.Query().Get("account") != "alice" {
				http.Error(response, "bad token request", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]string{"token": "registry-token"})
		case "/v2/library/nginx/tags/list":
			if request.Header.Get("Authorization") != "Bearer registry-token" {
				response.Header().Set("WWW-Authenticate", `Bearer realm="`+registryURL+`/token",service="registry.test",scope="repository:library/nginx:pull"`)
				http.Error(response, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"1.24.0", "1.25.2"}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	registryURL = server.URL

	results, err := New(server.Client()).Inspect(context.Background(), Config{
		Endpoint: server.URL, Images: []string{"library/nginx"}, Username: "alice", Password: "token-secret",
	})
	if err != nil || len(results) != 1 || results[0].Tag != "1.25.2" {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}

func TestInspectAllowsHTTPTokenServiceForHTTPSRegistry(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			http.NotFound(response, request)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]string{"token": "mixed-transport-token"})
	}))
	defer tokenServer.Close()

	registry := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer mixed-transport-token" {
			response.Header().Set("WWW-Authenticate", `Bearer realm="`+tokenServer.URL+`/token",service="registry.test",scope="repository:team/api:pull"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"2.1.0"}})
	}))
	defer registry.Close()

	results, err := New(registry.Client()).Inspect(context.Background(), Config{
		Endpoint: registry.URL,
		Images:   []string{"team/api"},
	})
	if err != nil || len(results) != 1 || results[0].Tag != "2.1.0" || results[0].Error != "" {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}

func TestInspectReadsHarborArtifactPushTime(t *testing.T) {
	wantTime := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/v2/project/team/backend/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"2.3.0"}})
		case strings.HasPrefix(request.URL.EscapedPath(), "/api/v2.0/projects/project/repositories/team%2Fbackend/artifacts/2.3.0"):
			_ = json.NewEncoder(response).Encode(map[string]string{"push_time": wantTime.Format(time.RFC3339)})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	results, err := New(server.Client()).Inspect(context.Background(), Config{Endpoint: server.URL, Images: []string{"project/team/backend"}})
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	if !results[0].PushedAt.Equal(wantTime) || !results[0].PushTimeAvailable {
		t.Fatalf("push time was not populated: %#v", results[0])
	}
}

func TestInspectAppliesOneDeadlineAcrossAllImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	client := New(server.Client())
	client.inspectTimeout = 40 * time.Millisecond
	parent, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	results, err := client.Inspect(parent, Config{
		Endpoint: server.URL,
		Images:   []string{"team/api", "team/worker", "team/web"},
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 250*time.Millisecond || parent.Err() != nil {
		t.Fatalf("inspect exceeded its card deadline: elapsed=%s parent_err=%v", elapsed, parent.Err())
	}
	if len(results) != 3 {
		t.Fatalf("results=%#v", results)
	}
	for _, result := range results {
		if result.Error == "" {
			t.Fatalf("timed-out image has no error: %#v", result)
		}
	}
}

func TestValidateConfigAllowsHTTPAndRejectsTooManyOrTaggedImages(t *testing.T) {
	images := make([]string, 21)
	for index := range images {
		images[index] = "team/image-" + string(rune('a'+index))
	}
	if err := ValidateConfig(Config{Endpoint: "http://registry.lan:5000", Images: []string{"team/image"}}); err != nil {
		t.Fatalf("http registry rejected: %v", err)
	}
	if err := ValidateConfig(Config{Endpoint: "http://registry.lan:5000", Images: images}); err == nil {
		t.Fatal("too many images accepted")
	}
	if err := ValidateConfig(Config{Endpoint: "http://registry.lan:5000", Images: []string{"team/image:latest"}}); err == nil {
		t.Fatal("tagged image accepted")
	}
	if err := ValidateConfig(Config{Endpoint: "http://user:password@registry.lan:5000", Images: []string{"team/image"}}); err == nil {
		t.Fatal("credentials embedded in Registry URL were accepted")
	}
}

func TestLatestTagPrefersStableSemanticVersionOverPrereleaseAndLatest(t *testing.T) {
	if got := latestTag([]string{"latest", "v2.0.0-rc.1", "1.9.9", "v2.0.0"}); got != "v2.0.0" {
		t.Fatalf("latest tag=%q", got)
	}
}
