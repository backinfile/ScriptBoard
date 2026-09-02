package web_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/redismanager"
	app "scriptboard/internal/web"
)

type redisWebBackend struct {
	lastScan    redismanager.ScanRequest
	lastKey     string
	overviewErr error
}

func (redisWebBackend) StoreCredential(context.Context, redismanager.Instance, string) error {
	return nil
}
func (redisWebBackend) DeleteCredential(context.Context, string) error { return nil }
func (redisWebBackend) Test(context.Context, redismanager.Instance) (redismanager.ConnectionTest, error) {
	return redismanager.ConnectionTest{OK: true, Version: "8.0.0", CanInfo: true, CanScan: true}, nil
}

func (backend redisWebBackend) Overview(context.Context, redismanager.Instance) (redismanager.Overview, error) {
	if backend.overviewErr != nil {
		return redismanager.Overview{}, backend.overviewErr
	}
	return redismanager.Overview{Version: "8.0.0", KeyCount: 42, UsedMemory: 4 << 20}, nil
}
func (backend *redisWebBackend) Scan(_ context.Context, _ redismanager.Instance, request redismanager.ScanRequest) (redismanager.ScanPage, error) {
	backend.lastScan = request
	return redismanager.ScanPage{Cursor: 73, Keys: []redismanager.KeySummary{{Name: "order::42", Type: "hash", SizeBytes: 512}, {Name: "session::7", Type: "string", SizeBytes: 16}, {Name: "cache:item", Type: "string", SizeBytes: 12}, {Name: "ungrouped", Type: "string", SizeBytes: 8}}}, nil
}
func (backend *redisWebBackend) ReadKey(_ context.Context, _ redismanager.Instance, key string) (redismanager.KeyValue, error) {
	backend.lastKey = key
	return redismanager.KeyValue{Name: key, Type: "hash", Items: []redismanager.KeyValueItem{{Field: "status", Value: "paid"}}}, nil
}

func TestAdministratorCanRegisterAndInspectRedisConnection(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	backend := &redisWebBackend{}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot, RedisBackend: backend})
	response, err := client.Get(serverURL + "/resources/databases?engine=redis")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/databases/redis/instances", url.Values{
		"name": {"Missing CSRF"}, "environment": {"production"}, "host": {"redis.internal"},
		"port": {"6379"}, "database": {"0"}, "tls_mode": {"disabled"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("create Redis connection without CSRF status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}
	response, err = client.PostForm(serverURL+"/resources/databases/redis/instances", url.Values{
		"csrf_token": {formToken(t, body)}, "name": {"Cache production"}, "environment": {"production"},
		"host": {"redis.internal"}, "port": {"6379"}, "username": {"scriptboard"}, "database": {"2"},
		"password": {"redis-password"}, "tls_mode": {"insecure_skip_verify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "engine=redis") {
		t.Fatalf("create Redis connection status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	page := string(getBody(t, client, serverURL+response.Header.Get("Location"), http.StatusOK))
	for _, expected := range []string{"Cache production", "redis.internal:6379", "8.0.0", "4.0 MiB", `class="mysql-tabs"`, `tab=keys`, `tab=diagnostics`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("Redis workspace missing %q: %s", expected, page)
		}
	}
	for _, expected := range []string{`class="mysql-detail database-detail"`, `data-database-engine="redis"`, `data-database-detail-tabs`, `data-database-tabs`, `data-database-tab-panel="overview"`, `data-lucide="search"`, `data-lucide="shield-check"`, `connection_page=1`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("Redis detail does not follow the shared database tab framework; missing %q: %s", expected, page)
		}
	}
	if !strings.Contains(page, "man-in-the-middle") {
		t.Fatalf("Redis connection form does not explain skip-verification risk: %s", page)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	instanceID := location.Query().Get("instance")
	if instanceID == "" {
		t.Fatalf("Redis connection redirect missing instance id: %q", response.Header.Get("Location"))
	}
	for _, expected := range []string{`data-redis-edit-instance`, `name="id" value="` + instanceID + `"`, `name="host" value="redis.internal"`, `value="insecure_skip_verify" selected`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("Redis edit drawer missing %q: %s", expected, page)
		}
	}
	response, err = client.PostForm(serverURL+"/resources/databases/redis/instances", url.Values{
		"csrf_token": {formToken(t, body)}, "id": {instanceID}, "name": {"Cache renamed"}, "environment": {"development"},
		"host": {"redis.internal"}, "port": {"6379"}, "username": {"scriptboard"}, "database": {"2"},
		"password": {""}, "tls_mode": {"insecure_skip_verify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit Redis connection status=%d", response.StatusCode)
	}
	edited := string(getBody(t, client, serverURL+response.Header.Get("Location"), http.StatusOK))
	if !strings.Contains(edited, "Cache renamed") || !strings.Contains(edited, "development") {
		t.Fatalf("edited Redis connection was not rendered: %s", edited)
	}
	keyspace := string(getBody(t, client, serverURL+"/resources/databases?engine=redis&instance="+url.QueryEscape(instanceID)+"&tab=keys&pattern=order::*&key=order::42", http.StatusOK))
	for _, expected := range []string{`data-redis-scan-form`, "Scan keyspace", "Match pattern", `data-redis-key-namespace="order"`, `data-redis-key-namespace="session"`, "Ungrouped keys", "cache:item", `data-redis-value-inspector`, "order::42", "status", "paid"} {
		if !strings.Contains(keyspace, expected) {
			t.Fatalf("Redis key browser missing %q: %s", expected, keyspace)
		}
	}
	if strings.Contains(keyspace, `data-redis-key-namespace="cache"`) {
		t.Fatalf("single-colon key was incorrectly split into a namespace: %s", keyspace)
	}
	if !strings.Contains(keyspace, `name="cursor" value="73"`) {
		t.Fatalf("Redis key browser does not expose the next SCAN cursor: %s", keyspace)
	}
	_ = getBody(t, client, serverURL+"/resources/databases?engine=redis&instance="+url.QueryEscape(instanceID)+"&tab=keys&cursor=73", http.StatusOK)
	if backend.lastScan.Cursor != 73 {
		t.Fatalf("continued Redis scan cursor=%d, want 73", backend.lastScan.Cursor)
	}
	paddedKey := "  padded Redis key  "
	_ = getBody(t, client, serverURL+"/resources/databases?engine=redis&instance="+url.QueryEscape(instanceID)+"&tab=keys&key="+url.QueryEscape(paddedKey), http.StatusOK)
	if backend.lastKey != paddedKey {
		t.Fatalf("Redis key preview received %q, want exact key %q", backend.lastKey, paddedKey)
	}
	for _, endpoint := range []string{"test", "delete"} {
		response, err = client.PostForm(serverURL+"/resources/databases/redis/instances/"+instanceID+"/"+endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("Redis %s without CSRF status=%d, want %d", endpoint, response.StatusCode, http.StatusForbidden)
		}
	}
}

func TestRedisOverviewTimeoutShowsActionableConnectionError(t *testing.T) {
	backend := &redisWebBackend{overviewErr: errors.Join(context.DeadlineExceeded, errors.New("bounded JSONL record is invalid"))}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), RedisBackend: backend})
	landing := getBody(t, client, serverURL+"/resources/databases?engine=redis", http.StatusOK)
	response, err := client.PostForm(serverURL+"/resources/databases/redis/instances", url.Values{
		"csrf_token": {formToken(t, landing)}, "name": {"Slow cache"}, "environment": {"production"},
		"host": {"redis.internal"}, "port": {"6379"}, "database": {"0"}, "tls_mode": {"disabled"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	page := string(getBody(t, client, serverURL+response.Header.Get("Location")+"&lang=en", http.StatusOK))
	if !strings.Contains(page, "Redis connection timed out") || !strings.Contains(page, "host, port, network access, and TLS mode") {
		t.Fatalf("Redis timeout is not actionable: %s", page)
	}
	if strings.Contains(page, "bounded JSONL record is invalid") {
		t.Fatalf("Redis timeout exposed Broker framing error: %s", page)
	}
}
