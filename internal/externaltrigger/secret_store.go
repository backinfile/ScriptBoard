package externaltrigger

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"scriptboard/internal/secretstore"
)

var ErrSecretUnavailable = errors.New("external trigger key secret is unavailable")

const externalSecretPurpose = "external-interface-secrets-v2"

type encryptedSecretStore struct {
	directory string
	vault     *secretstore.Store
	mu        sync.Mutex
}

func (store *encryptedSecretStore) get(id string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	values, err := store.load()
	if err != nil {
		return "", err
	}
	secret, ok := values[id]
	if !ok {
		return "", ErrSecretUnavailable
	}
	return secret, nil
}

func (store *encryptedSecretStore) set(id, secret string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	values, err := store.load()
	if err != nil {
		return err
	}
	values[id] = secret
	return store.write(values)
}

func (store *encryptedSecretStore) delete(id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	values, err := store.load()
	if err != nil {
		return err
	}
	if _, ok := values[id]; !ok {
		return nil
	}
	delete(values, id)
	return store.write(values)
}

func (store *encryptedSecretStore) load() (map[string]string, error) {
	if err := store.ensureMigrated(); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(store.sealedPath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read external interface secret store: %w", err)
	}
	if len(body) > 4<<20 {
		return nil, errors.New("external interface secret store is too large")
	}
	plain, err := store.vault.Unseal(externalSecretPurpose, body)
	if err != nil {
		return nil, fmt.Errorf("unseal external interface secrets: %w", err)
	}
	values := map[string]string{}
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, fmt.Errorf("decode external interface secrets: %w", err)
	}
	return values, nil
}

func (store *encryptedSecretStore) write(values map[string]string) error {
	plain, err := json.Marshal(values)
	if err != nil {
		return err
	}
	body, err := store.vault.Seal(externalSecretPurpose, plain)
	if err != nil {
		return fmt.Errorf("seal external interface secrets: %w", err)
	}
	path := store.sealedPath()
	temporary, err := os.CreateTemp(store.directory, ".external-interface-secrets-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
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
	return os.Rename(temporaryPath, path)
}

func (store *encryptedSecretStore) ensureMigrated() error {
	if store.directory == "" {
		return ErrSecretUnavailable
	}
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return fmt.Errorf("create external interface secrets directory: %w", err)
	}
	_ = os.Chmod(store.directory, 0o700)
	if store.vault == nil {
		vault, err := secretstore.New(filepath.Dir(store.directory))
		if err != nil {
			return fmt.Errorf("initialize external interface credential store: %w", err)
		}
		store.vault = vault
	}
	legacyKeyPath := filepath.Join(store.directory, "external-interface.master-key")
	legacyDataPath := filepath.Join(store.directory, "external-interface-keys.json")
	key, keyErr := os.ReadFile(legacyKeyPath)
	data, dataErr := os.ReadFile(legacyDataPath)
	hasKey, hasData := keyErr == nil, dataErr == nil
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return fmt.Errorf("read legacy external interface key: %w", keyErr)
	}
	if dataErr != nil && !errors.Is(dataErr, os.ErrNotExist) {
		return fmt.Errorf("read legacy external interface secrets: %w", dataErr)
	}
	if !hasKey && !hasData {
		return nil
	}
	if hasKey != hasData {
		if hasKey {
			if err := os.Remove(legacyKeyPath); err != nil {
				return fmt.Errorf("remove orphaned external interface key: %w", err)
			}
		} else if err := os.Remove(legacyDataPath); err != nil {
			return fmt.Errorf("remove undecryptable external interface secrets: %w", err)
		}
		return nil
	}
	if _, err := os.Stat(store.sealedPath()); errors.Is(err, os.ErrNotExist) {
		values, decryptErr := decryptLegacyExternalSecrets(key, data)
		if decryptErr != nil {
			return decryptErr
		}
		if err := store.write(values); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("inspect sealed external interface secrets: %w", err)
	}
	if err := os.Remove(legacyKeyPath); err != nil {
		return fmt.Errorf("remove legacy external interface key: %w", err)
	}
	if err := os.Remove(legacyDataPath); err != nil {
		return fmt.Errorf("remove legacy external interface secrets: %w", err)
	}
	return nil
}

func decryptLegacyExternalSecrets(key, body []byte) (map[string]string, error) {
	if len(key) != 32 {
		return nil, errors.New("external interface master key has an invalid length")
	}
	encoded := map[string]string{}
	if err := json.Unmarshal(body, &encoded); err != nil {
		return nil, fmt.Errorf("decode legacy external interface secrets: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(encoded))
	for id, value := range encoded {
		sealed, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(sealed) < gcm.NonceSize() {
			return nil, ErrSecretUnavailable
		}
		plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(id))
		if err != nil {
			return nil, ErrSecretUnavailable
		}
		values[id] = string(plain)
	}
	return values, nil
}

func (store *encryptedSecretStore) sealedPath() string {
	return filepath.Join(store.directory, "external-interface-secrets.v2.enc")
}
