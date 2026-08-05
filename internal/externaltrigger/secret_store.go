package externaltrigger

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var ErrSecretUnavailable = errors.New("external trigger key secret is unavailable")

type encryptedSecretStore struct {
	directory string
	mu        sync.Mutex
}

func (store *encryptedSecretStore) get(id string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	values, key, err := store.load()
	if err != nil {
		return "", err
	}
	encoded, ok := values[id]
	if !ok {
		return "", ErrSecretUnavailable
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode external trigger key secret: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", ErrSecretUnavailable
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(id))
	if err != nil {
		return "", ErrSecretUnavailable
	}
	return string(plain), nil
}

func (store *encryptedSecretStore) set(id, secret string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	values, key, err := store.load()
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate external trigger key nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(secret), []byte(id))
	values[id] = base64.RawURLEncoding.EncodeToString(sealed)
	return store.write(values)
}

func (store *encryptedSecretStore) delete(id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	values, _, err := store.load()
	if err != nil {
		return err
	}
	if _, ok := values[id]; !ok {
		return nil
	}
	delete(values, id)
	return store.write(values)
}

func (store *encryptedSecretStore) load() (map[string]string, []byte, error) {
	if store.directory == "" {
		return nil, nil, ErrSecretUnavailable
	}
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create external trigger secrets directory: %w", err)
	}
	_ = os.Chmod(store.directory, 0o700)
	keyPath := filepath.Join(store.directory, "external-interface.master-key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(keyPath, key, 0o600); err != nil {
			return nil, nil, fmt.Errorf("write external trigger master key: %w", err)
		}
		_ = os.Chmod(keyPath, 0o600)
	} else if err != nil {
		return nil, nil, fmt.Errorf("read external trigger master key: %w", err)
	}
	if len(key) != 32 {
		return nil, nil, errors.New("external trigger master key has an invalid length")
	}
	values := map[string]string{}
	content, err := os.ReadFile(filepath.Join(store.directory, "external-interface-keys.json"))
	if errors.Is(err, os.ErrNotExist) {
		return values, key, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read external trigger key store: %w", err)
	}
	if err := json.Unmarshal(content, &values); err != nil {
		return nil, nil, fmt.Errorf("decode external trigger key store: %w", err)
	}
	return values, key, nil
}

func (store *encryptedSecretStore) write(values map[string]string) error {
	content, err := json.Marshal(values)
	if err != nil {
		return err
	}
	path := filepath.Join(store.directory, "external-interface-keys.json")
	temporary, err := os.CreateTemp(store.directory, ".external-interface-keys-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace external trigger key store: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}
