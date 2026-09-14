package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"testing"

	"scriptboard/internal/mysqlmanager"
	app "scriptboard/internal/web"
)

type offlineMySQLBackend struct {
	mysqlmanager.Backend
	calls int
}

func (*offlineMySQLBackend) PrepareArtifactRoot(context.Context, string) error { return nil }
func (*offlineMySQLBackend) CleanupArtifacts(context.Context, string) error    { return nil }
func (*offlineMySQLBackend) Tools() mysqlmanager.ToolSettings                  { return mysqlmanager.ToolSettings{} }
func (*offlineMySQLBackend) StoreCredential(context.Context, mysqlmanager.Instance, string) error {
	return nil
}
func (b *offlineMySQLBackend) Status(context.Context, mysqlmanager.Instance) (mysqlmanager.Status, error) {
	b.calls++
	return mysqlmanager.Status{}, errors.New("offline fixture")
}
func (b *offlineMySQLBackend) Databases(context.Context, mysqlmanager.Instance) ([]mysqlmanager.Database, error) {
	b.calls++
	return nil, errors.New("offline fixture")
}

func TestMySQLLocalRecordsDoNotConnectToOfflineServer(t *testing.T) {
	backend := &offlineMySQLBackend{}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), MySQLBackend: backend})
	page := getBody(t, client, serverURL+"/resources/databases", http.StatusOK)
	response, err := client.PostForm(serverURL+"/resources/databases/instances", url.Values{
		"csrf_token": {formToken(t, page)}, "name": {"Offline"}, "host": {"127.0.0.1"}, "port": {"1"}, "username": {"qa"}, "password": {"qa"}, "tls_mode": {"disabled"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	page = getBody(t, client, serverURL+"/resources/databases", http.StatusOK)
	match := regexp.MustCompile("instance=([a-f0-9]+)").FindSubmatch(page)
	if len(match) != 2 {
		t.Fatal("missing instance")
	}
	id := string(match[1])
	for _, tab := range []string{"backups", "plans", "operations"} {
		getBody(t, client, serverURL+"/resources/databases?instance="+id+"&tab="+tab, http.StatusOK)
		if backend.calls != 0 {
			t.Fatalf("%s contacted offline server %d times", tab, backend.calls)
		}
	}
	getBody(t, client, serverURL+"/resources/databases?instance="+id+"&tab=overview", http.StatusOK)
	if backend.calls != 1 {
		t.Fatalf("live overview calls=%d", backend.calls)
	}
}
