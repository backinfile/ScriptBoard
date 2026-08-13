package registryconnection

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateLegacyActivatesBoundConnectionBeforeDeletingOldKey(t *testing.T) {
	root := t.TempDir()
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "app.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE custom_dashboard_cards(id TEXT PRIMARY KEY,type TEXT,config_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO custom_dashboard_cards VALUES('card-1','registry','{"endpoint":"https://registry.example","images":["team/api"],"authMode":"basic","username":"robot"}')`); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, "secrets")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	sealed := gcm.Seal(nonce, nonce, []byte("legacy-secret"), []byte("card-1"))
	body, _ := json.Marshal(map[string]string{"card-1": base64.RawURLEncoding.EncodeToString(sealed)})
	if err := os.WriteFile(filepath.Join(legacy, "custom-dashboard-registry.master-key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "custom-dashboard-registry.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MigrateLegacy(context.Background(), database, legacy); err != nil {
		t.Fatal(err)
	}
	state, err := service.load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active["card-1"].Password != "legacy-secret" {
		t.Fatalf("migrated record=%#v", state.Active["card-1"])
	}
	for _, name := range []string{"custom-dashboard-registry.master-key", "custom-dashboard-registry.json"} {
		if _, err := os.Stat(filepath.Join(legacy, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy file %s survived: %v", name, err)
		}
	}
	if err := service.MigrateLegacy(context.Background(), database, legacy); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
