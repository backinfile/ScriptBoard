package redismanager

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestManagerSavesPlaintextAndTLSConnectionsThroughBackend(t *testing.T) {
	database := openTestDatabase(t)
	backend := &recordingBackend{}
	manager, err := New(Options{DB: database, StateRoot: t.TempDir(), Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []TLSMode{TLSDisabled, TLSVerifyIdentity, TLSInsecureSkipVerify} {
		instance, saveErr := manager.SaveInstance(context.Background(), InstanceInput{
			Name: "cache-" + string(mode), Host: "redis.internal", Port: 6379,
			Username: "operator", Password: "secret", TLSMode: mode,
		})
		if saveErr != nil {
			t.Fatalf("save %s connection: %v", mode, saveErr)
		}
		if instance.TLSMode != mode || !instance.CredentialConfigured {
			t.Fatalf("unexpected saved instance: %+v", instance)
		}
	}
	if len(backend.stored) != 3 {
		t.Fatalf("stored credentials = %d, want 3", len(backend.stored))
	}
}

func TestLocalBackendEncryptsPasswordAndBuildsEveryTransportMode(t *testing.T) {
	database := openTestDatabase(t)
	stateRoot := t.TempDir()
	manager, err := New(Options{DB: database, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	backend := manager.ExecutionBackend().(*localBackend)
	for _, test := range []struct {
		mode       TLSMode
		tls        bool
		skipVerify bool
	}{
		{mode: TLSDisabled},
		{mode: TLSVerifyIdentity, tls: true},
		{mode: TLSInsecureSkipVerify, tls: true, skipVerify: true},
	} {
		instance, saveErr := manager.SaveInstance(context.Background(), InstanceInput{Name: "local-" + string(test.mode), Host: "redis.internal", Port: 6379, Password: "local-secret-value", TLSMode: test.mode})
		if saveErr != nil {
			t.Fatalf("save %s: %v", test.mode, saveErr)
		}
		client, clientErr := backend.client(instance, 5)
		if clientErr != nil {
			t.Fatalf("build %s client: %v", test.mode, clientErr)
		}
		options := client.Options()
		if (options.TLSConfig != nil) != test.tls {
			t.Fatalf("mode %s TLS configured = %v, want %v", test.mode, options.TLSConfig != nil, test.tls)
		}
		if options.DB != 5 {
			t.Fatalf("mode %s database = %d, want operation-selected database 5", test.mode, options.DB)
		}
		if options.TLSConfig != nil && options.TLSConfig.InsecureSkipVerify != test.skipVerify {
			t.Fatalf("mode %s skip verify = %v, want %v", test.mode, options.TLSConfig.InsecureSkipVerify, test.skipVerify)
		}
		_ = client.Close()
	}
	sealed, err := os.ReadFile(filepath.Join(stateRoot, "secrets", "redis-credentials.v1.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("local-secret-value")) {
		t.Fatal("Redis credential file contains the plaintext password")
	}
}

func TestManagerAllowsPasswordlessRedisInstance(t *testing.T) {
	database := openTestDatabase(t)
	backend := &recordingBackend{}
	manager, err := New(Options{DB: database, StateRoot: t.TempDir(), Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "passwordless", Host: "127.0.0.1", Port: 6379, TLSMode: TLSDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if !instance.CredentialConfigured || len(backend.stored) != 1 {
		t.Fatalf("passwordless credential record was not configured: instance=%+v stored=%d", instance, len(backend.stored))
	}
}

func TestManagerRejectsCredentialBindingChangeWithoutPassword(t *testing.T) {
	database := openTestDatabase(t)
	backend := &recordingBackend{}
	manager, err := New(Options{DB: database, StateRoot: t.TempDir(), Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "cache", Host: "redis.internal", Port: 6379, Password: "secret", TLSMode: TLSVerifyIdentity})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.SaveInstance(context.Background(), InstanceInput{ID: instance.ID, Name: "cache", Host: "other.internal", Port: 6379, TLSMode: TLSVerifyIdentity})
	if err == nil {
		t.Fatal("connection binding changed without a replacement password")
	}
	stored, err := manager.Instance(context.Background(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Host != "redis.internal" {
		t.Fatalf("stored host changed after rejected update: %q", stored.Host)
	}
}

func TestManagerBoundsScanCount(t *testing.T) {
	database := openTestDatabase(t)
	backend := &recordingBackend{}
	manager, err := New(Options{DB: database, StateRoot: t.TempDir(), Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "cache", Host: "127.0.0.1", Port: 6379, Password: "secret", TLSMode: TLSDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Scan(context.Background(), instance.ID, 7, ScanRequest{Pattern: "order:*", Count: 5000}); err != nil {
		t.Fatal(err)
	}
	if backend.lastScan.Count != 200 {
		t.Fatalf("scan count = %d, want 200", backend.lastScan.Count)
	}
	if backend.lastDatabase != 7 {
		t.Fatalf("scan database = %d, want 7", backend.lastDatabase)
	}
}

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, statement := range SchemaStatements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

type recordingBackend struct {
	stored       []Instance
	lastScan     ScanRequest
	lastDatabase int
}

func (backend *recordingBackend) StoreCredential(_ context.Context, instance Instance, _ string) error {
	backend.stored = append(backend.stored, instance)
	return nil
}
func (*recordingBackend) DeleteCredential(context.Context, string) error { return nil }
func (*recordingBackend) Test(context.Context, Instance) (ConnectionTest, error) {
	return ConnectionTest{OK: true}, nil
}
func (backend *recordingBackend) Overview(_ context.Context, _ Instance, database int) (Overview, error) {
	backend.lastDatabase = database
	return Overview{}, nil
}
func (backend *recordingBackend) Scan(_ context.Context, _ Instance, database int, request ScanRequest) (ScanPage, error) {
	backend.lastDatabase = database
	backend.lastScan = request
	return ScanPage{}, nil
}
func (backend *recordingBackend) ReadKey(_ context.Context, _ Instance, database int, key string) (KeyValue, error) {
	backend.lastDatabase = database
	return KeyValue{Name: key, Type: "string", Value: "preview"}, nil
}

func TestManagerReadKeyValidatesNameAndReturnsPreview(t *testing.T) {
	backend := &recordingBackend{}
	manager, err := New(Options{DB: openTestDatabase(t), StateRoot: t.TempDir(), Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.SaveInstance(context.Background(), InstanceInput{Name: "cache", Host: "127.0.0.1", Port: 6379, TLSMode: TLSDisabled})
	if err != nil {
		t.Fatal(err)
	}
	value, err := manager.ReadKey(context.Background(), instance.ID, 9, "qa:string")
	if err != nil || value.Value != "preview" || value.Name != "qa:string" {
		t.Fatalf("read preview = %#v, %v", value, err)
	}
	if backend.lastDatabase != 9 {
		t.Fatalf("read key database = %d, want 9", backend.lastDatabase)
	}
	if _, err := manager.ReadKey(context.Background(), instance.ID, 9, "bad\nkey"); err == nil {
		t.Fatal("control characters in Redis key must be rejected")
	}
}
