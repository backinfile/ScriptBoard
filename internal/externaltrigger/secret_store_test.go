package externaltrigger

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedSecretStoreMigratesStateRootMasterKey(t *testing.T) {
	stateRoot := t.TempDir()
	directory := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	const id = "remote-website:branch"
	sealed := gcm.Seal(nonce, nonce, []byte("legacy-remote-secret"), []byte(id))
	legacy, _ := json.Marshal(map[string]string{id: base64.RawURLEncoding.EncodeToString(sealed)})
	keyPath := filepath.Join(directory, "external-interface.master-key")
	dataPath := filepath.Join(directory, "external-interface-keys.json")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &encryptedSecretStore{directory: directory}
	secret, err := store.get(id)
	if err != nil || secret != "legacy-remote-secret" {
		t.Fatalf("migrated secret=%q err=%v", secret, err)
	}
	for _, path := range []string{keyPath, dataPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy secret material remained at %s: %v", path, err)
		}
	}
	newBody, err := os.ReadFile(filepath.Join(directory, "external-interface-secrets.v2.enc"))
	if err != nil || bytes.Contains(newBody, []byte("legacy-remote-secret")) {
		t.Fatalf("sealed secret migration invalid: err=%v body=%s", err, newBody)
	}
}
