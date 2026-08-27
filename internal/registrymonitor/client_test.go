package registrymonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestInspectReadsImageCreatedTimeFromRegistryManifest(t *testing.T) {
	wantTime := time.Date(2026, 8, 17, 14, 20, 0, 0, time.UTC)
	configDigest := "sha256:" + strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/team/api/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"2.3.0"}})
		case "/api/v2.0/projects/team/repositories/api/artifacts/2.3.0":
			http.NotFound(response, request)
		case "/v2/team/api/manifests/2.3.0":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.oci.image.manifest.v1+json",
				"config": map[string]any{
					"mediaType": "application/vnd.oci.image.config.v1+json",
					"digest":    configDigest,
					"size":      256,
				},
			})
		case "/v2/team/api/blobs/" + configDigest:
			_ = json.NewEncoder(response).Encode(map[string]any{"created": wantTime.Format(time.RFC3339)})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	results, err := New(server.Client()).Inspect(context.Background(), Config{Endpoint: server.URL, Images: []string{"team/api"}})
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	if !results[0].PushedAt.Equal(wantTime) || !results[0].PushTimeAvailable || results[0].TimeSource != ImageTimeCreated {
		t.Fatalf("image created time was not populated: %#v", results[0])
	}
}

func TestInspectReadsImageCreatedTimeFromRegistryIndex(t *testing.T) {
	wantTime := time.Date(2026, 8, 16, 8, 15, 0, 0, time.UTC)
	manifestDigest := "sha256:" + strings.Repeat("b", 64)
	configDigest := "sha256:" + strings.Repeat("c", 64)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/team/api/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"3.0.0"}})
		case "/api/v2.0/projects/team/repositories/api/artifacts/3.0.0":
			http.NotFound(response, request)
		case "/v2/team/api/manifests/3.0.0":
			if !strings.Contains(request.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
				http.Error(response, "OCI index media type was not accepted", http.StatusNotAcceptable)
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.oci.image.index.v1+json",
				"manifests": []map[string]any{{
					"mediaType": "application/vnd.oci.image.manifest.v1+json",
					"digest":    manifestDigest,
					"size":      512,
				}},
			})
		case "/v2/team/api/manifests/" + manifestDigest:
			_ = json.NewEncoder(response).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.oci.image.manifest.v1+json",
				"config":        map[string]any{"digest": configDigest},
			})
		case "/v2/team/api/blobs/" + configDigest:
			_ = json.NewEncoder(response).Encode(map[string]any{"created": wantTime.Format(time.RFC3339)})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	results, err := New(server.Client()).Inspect(context.Background(), Config{Endpoint: server.URL, Images: []string{"team/api"}})
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	if !results[0].PushedAt.Equal(wantTime) || results[0].TimeSource != ImageTimeCreated {
		t.Fatalf("indexed image created time was not populated: %#v", results[0])
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

func TestInspectExpandsNamespaceAndAllImageSelectors(t *testing.T) {
	var catalogRequests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/_catalog":
			catalogRequests++
			if request.URL.Query().Get("last") == "team/web" {
				_ = json.NewEncoder(response).Encode(map[string]any{"repositories": []string{"tools/worker"}})
				return
			}
			response.Header().Set("Link", `</v2/_catalog?n=100&last=other%2Fdb>; rel="prev", </v2/_catalog?n=100&last=team%2Fweb>; rel="next"`)
			_ = json.NewEncoder(response).Encode(map[string]any{"repositories": []string{"other/db", "team/web", "team/api"}})
		case "/v2/team/api/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"1.2.0"}})
		case "/v2/team/web/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"2.1.0"}})
		case "/v2/other/db/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"3.0.0"}})
		case "/v2/tools/worker/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"4.0.0"}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	results, err := New(server.Client()).Inspect(context.Background(), Config{
		Endpoint: server.URL,
		Images:   []string{"team/*", "team/api", "*/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if catalogRequests != 2 {
		t.Fatalf("catalog requests=%d, want 2", catalogRequests)
	}
	wantImages := []string{"team/api", "team/web", "other/db", "tools/worker"}
	if len(results) != len(wantImages) {
		t.Fatalf("results=%#v", results)
	}
	for index, want := range wantImages {
		if results[index].Image != want || results[index].Error != "" {
			t.Fatalf("result[%d]=%#v, want image %q", index, results[index], want)
		}
	}
}

func TestInspectTreatsEmptyCatalogAsSuccessfulEmptyResult(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/_catalog" {
			http.NotFound(response, request)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"repositories": []string{}})
	}))
	defer registry.Close()

	results, err := New(registry.Client()).Inspect(context.Background(), Config{
		Endpoint: registry.URL,
		Images:   []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results=%#v, want an empty successful result", results)
	}
}

func TestInspectOmitsRepositoriesWithoutTags(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/team/empty/tags/list":
			_, _ = response.Write([]byte(`{"name":"team/empty","tags":null}`))
		case "/v2/team/api/tags/list":
			_, _ = response.Write([]byte(`{"name":"team/api","tags":["0.0.2"]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer registry.Close()

	results, err := New(registry.Client()).Inspect(context.Background(), Config{
		Endpoint: registry.URL,
		Images:   []string{"team/empty", "team/api"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Image != "team/api" || results[0].Tag != "0.0.2" || results[0].Error != "" {
		t.Fatalf("results=%#v, want only the tagged repository", results)
	}
}

func TestInspectRejectsCatalogRedirects(t *testing.T) {
	redirectedRequests := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirectedRequests++
		_ = json.NewEncoder(response).Encode(map[string]any{"repositories": []string{"private/image"}})
	}))
	defer redirectTarget.Close()
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, redirectTarget.URL+"/v2/_catalog", http.StatusFound)
	}))
	defer registry.Close()

	results, err := New(registry.Client()).Inspect(context.Background(), Config{Endpoint: registry.URL, Images: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if redirectedRequests != 0 || len(results) != 1 || !strings.Contains(results[0].Error, "302") {
		t.Fatalf("redirected_requests=%d results=%#v", redirectedRequests, results)
	}
}

func TestInspectBoundsCatalogExpansion(t *testing.T) {
	repositories := make([]string, maxCatalogRepositories+1)
	for index := range repositories {
		repositories[index] = fmt.Sprintf("team/image-%04d", index)
	}
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{"repositories": repositories})
	}))
	defer registry.Close()

	results, err := New(registry.Client()).Inspect(context.Background(), Config{Endpoint: registry.URL, Images: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Error, "过多") {
		t.Fatalf("results=%#v", results)
	}
}

func TestInspectBoundsCatalogPagesAndSurfacesCancellation(t *testing.T) {
	requests := 0
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Link", fmt.Sprintf(`</v2/_catalog?n=100&last=page-%d>; rel="next"`, requests))
		_ = json.NewEncoder(response).Encode(map[string]any{"repositories": []string{}})
	}))
	defer registry.Close()

	results, err := New(registry.Client()).Inspect(context.Background(), Config{Endpoint: registry.URL, Images: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if requests != maxCatalogPages || len(results) != 1 || !strings.Contains(results[0].Error, "分页过多") {
		t.Fatalf("requests=%d results=%#v", requests, results)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	results, err = New(registry.Client()).Inspect(cancelled, Config{Endpoint: registry.URL, Images: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Error, "context canceled") {
		t.Fatalf("cancelled results=%#v", results)
	}
}

func TestExpandImageSelectorsBoundsResultsAndHonorsCancellation(t *testing.T) {
	repositories := make([]string, maxExpandedImages+1)
	for index := range repositories {
		repositories[index] = fmt.Sprintf("team/image-%04d", index)
	}
	if _, err := expandImageSelectors(context.Background(), []string{"*"}, repositories); err == nil || !strings.Contains(err.Error(), "匹配结果过多") {
		t.Fatalf("expanded result error=%v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := expandImageSelectors(cancelled, []string{"*"}, []string{"team/api"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled expansion error=%v", err)
	}
}

func TestCredentialedRegistryRequestsDoNotFollowRedirects(t *testing.T) {
	redirectedRequests := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirectedRequests++
		_ = json.NewEncoder(response).Encode(map[string]string{"token": "escaped"})
	}))
	defer redirectTarget.Close()
	tokenService := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, redirectTarget.URL+"/token", http.StatusFound)
	}))
	defer tokenService.Close()
	var registryURL string
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/v2/team/api/tags/list":
			response.Header().Set("WWW-Authenticate", `Bearer realm="`+tokenService.URL+`/token",service="registry.test",scope="repository:team/api:pull"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
		case request.URL.Path == "/v2/team/web/tags/list":
			_ = json.NewEncoder(response).Encode(map[string]any{"tags": []string{"1.0.0"}})
		case request.URL.Path == "/api/v2.0/projects/team/repositories/web/artifacts/1.0.0":
			http.Redirect(response, request, redirectTarget.URL+"/artifact", http.StatusFound)
		default:
			http.NotFound(response, request)
		}
	}))
	defer registry.Close()
	registryURL = registry.URL

	results, err := New(registry.Client()).Inspect(context.Background(), Config{
		Endpoint: registryURL, Images: []string{"team/api", "team/web"}, AuthMode: "basic", Username: "robot", Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if redirectedRequests != 0 || len(results) != 2 || !strings.Contains(results[0].Error, "302") || results[1].PushTimeAvailable {
		t.Fatalf("redirected_requests=%d results=%#v", redirectedRequests, results)
	}
}

func TestValidateConfigAcceptsOnlyCompleteImageWildcards(t *testing.T) {
	for _, selector := range []string{"team/*", "*", "*/*"} {
		if err := ValidateConfig(Config{Endpoint: "https://registry.example", Images: []string{selector}}); err != nil {
			t.Fatalf("selector %q rejected: %v", selector, err)
		}
	}
	for _, selector := range []string{"team*", "team/*/api", "*/api"} {
		if err := ValidateConfig(Config{Endpoint: "https://registry.example", Images: []string{selector}}); err == nil {
			t.Fatalf("invalid selector %q accepted", selector)
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
