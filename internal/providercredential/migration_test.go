package providercredential

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"scriptboard/internal/secretstore"
)

func TestMigrateLegacyBindsConfiguredModelBeforeRemovingOldStore(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "app.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE assistant_models (
		id TEXT PRIMARY KEY, owner_user_id TEXT, provider TEXT, model TEXT, endpoint TEXT,
		credential_configured INTEGER, is_shared INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO assistant_models VALUES ('model-1','owner-1','openai-compatible','fixture','http://127.0.0.1:11434/v1',1,1)`); err != nil {
		t.Fatal(err)
	}
	vault, err := secretstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(map[string]string{"model-1": "legacy-secret"})
	sealed, err := vault.Seal(legacyStorePurpose, plain)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "secrets", "assistant-provider.enc")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{StateRoot: root, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MigrateLegacy(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy store still exists: %v", err)
	}
	values, err := service.load()
	if err != nil {
		t.Fatal(err)
	}
	entry := values["model-1"]
	if entry.OwnerUserID != "owner-1" || !entry.Shared || entry.Endpoint != "http://127.0.0.1:11434/v1" || entry.Credential != "legacy-secret" {
		t.Fatalf("migrated binding=%+v", entry)
	}
}

func TestMigrateLegacyFailsClosedWhenConfiguredCredentialIsMissing(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "app.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, _ = db.Exec(`CREATE TABLE assistant_models (id TEXT PRIMARY KEY, owner_user_id TEXT, provider TEXT, model TEXT, endpoint TEXT, credential_configured INTEGER, is_shared INTEGER)`)
	_, _ = db.Exec(`INSERT INTO assistant_models VALUES ('missing','owner','openai','gpt','https://api.openai.com/v1',1,0)`)
	service, err := New(Options{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MigrateLegacy(context.Background(), db); err == nil {
		t.Fatal("migration accepted configured model without a credential")
	}
}
