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
	return redismanager.ScanPage{Keys: []redismanager.KeySummary{{Name: "order:42", Type: "hash", SizeBytes: 512}}}, nil
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
	for _, expected := range []string{"Cache production", "redis.internal:6379", "8.0.0", "4.0 MiB"} {
		if !strings.Contains(page, expected) {
			t.Fatalf("Redis workspace missing %q: %s", expected, page)
		}
	}
	if !strings.Contains(page, "man-in-the-middle") {
		t.Fatalf("Redis connection form does not explain skip-verification risk: %s", page)
	}
}
