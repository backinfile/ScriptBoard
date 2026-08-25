package web_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/redismanager"
	app "scriptboard/internal/web"
)

type redisWebBackend struct{}

func (redisWebBackend) StoreCredential(context.Context, redismanager.Instance, string) error {
	return nil
}
func (redisWebBackend) DeleteCredential(context.Context, string) error { return nil }
func (redisWebBackend) Test(context.Context, redismanager.Instance) (redismanager.ConnectionTest, error) {
	return redismanager.ConnectionTest{OK: true, Version: "8.0.0", CanInfo: true, CanScan: true}, nil
}
func (redisWebBackend) Overview(context.Context, redismanager.Instance) (redismanager.Overview, error) {
	return redismanager.Overview{Version: "8.0.0", KeyCount: 42, UsedMemory: 4 << 20}, nil
}
func (redisWebBackend) Scan(context.Context, redismanager.Instance, redismanager.ScanRequest) (redismanager.ScanPage, error) {
	return redismanager.ScanPage{Keys: []redismanager.KeySummary{{Name: "order:42", Type: "hash", SizeBytes: 512}, {Name: "session:7", Type: "string", SizeBytes: 16}, {Name: "ungrouped", Type: "string", SizeBytes: 8}}}, nil
}
func (redisWebBackend) ReadKey(context.Context, redismanager.Instance, string) (redismanager.KeyValue, error) {
	return redismanager.KeyValue{Name: "order:42", Type: "hash", Items: []redismanager.KeyValueItem{{Field: "status", Value: "paid"}}}, nil
}

func TestAdministratorCanRegisterAndInspectRedisConnection(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot, RedisBackend: redisWebBackend{}})
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
	keyspace := string(getBody(t, client, serverURL+"/resources/databases?engine=redis&instance="+url.QueryEscape(instanceID)+"&tab=keys&pattern=order:*&key=order:42", http.StatusOK))
	for _, expected := range []string{`data-redis-scan-form`, "Scan keyspace", "Match pattern", `data-redis-key-namespace="order"`, `data-redis-key-namespace="session"`, "Ungrouped keys", `data-redis-value-inspector`, "order:42", "status", "paid"} {
		if !strings.Contains(keyspace, expected) {
			t.Fatalf("Redis key browser missing %q: %s", expected, keyspace)
		}
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
