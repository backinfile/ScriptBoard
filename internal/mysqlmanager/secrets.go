package mysqlmanager

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"scriptboard/internal/secretstore"
)

const mysqlCredentialPurpose = "mysql-credentials-v2"

const mysqlCredentialBindingPurpose = "mysql-credential-bindings-v1"

type credentialBinding struct {
	Host, Username, CAPath string
	Port                   int
	TLSMode                TLSMode
}

type credentialStore struct {
	directory string
	vault     *secretstore.Store
}

func (store credentialStore) get(id string) (string, error) {
	values, err := store.load()
	if err != nil {
		return "", err
	}
	password, ok := values[id]
	if !ok {
		return "", errors.New("MySQL credential is unavailable")
	}
	return password, nil
}

func (store credentialStore) getForInstance(instance Instance) (string, error) {
	password, err := store.get(instance.ID)
	if err != nil {
		return "", err
	}
	bindings, err := store.loadBindings()
	if err != nil {
		return "", err
	}
	binding, ok := bindings[instance.ID]
	if !ok || binding != bindingForInstance(instance) {
		return "", errors.New("MySQL credential binding does not match the requested instance")
	}
	return password, nil
}

func (store credentialStore) set(instance Instance, password string) error {
	values, err := store.load()
	if err != nil {
		return err
	}
	values[instance.ID] = password
	if err := store.write(values); err != nil {
		return err
	}
	bindings, err := store.loadBindings()
	if err != nil {
		return err
	}
	bindings[instance.ID] = bindingForInstance(instance)
	return store.writeBindings(bindings)
}

func (store credentialStore) delete(id string) error {
	values, err := store.load()
	if err != nil {
		return err
	}
	delete(values, id)
	if err := store.write(values); err != nil {
		return err
	}
	bindings, err := store.loadBindings()
	if err != nil {
		return err
	}
	delete(bindings, id)
	return store.writeBindings(bindings)
}

func bindingForInstance(instance Instance) credentialBinding {
	return credentialBinding{Host: instance.Host, Port: instance.Port, Username: instance.Username, TLSMode: instance.TLSMode, CAPath: instance.CAPath}
}

func (store credentialStore) ensureBindings(database *sql.DB) error {
	values, err := store.load()
	if err != nil {
		return err
	}
	bindings, err := store.loadBindings()
	if err != nil {
		return err
	}
	changed := false
	for id := range values {
		if _, ok := bindings[id]; ok {
			continue
		}
		var instance Instance
		if err := database.QueryRowContext(context.Background(), `SELECT host,port,username,tls_mode,ca_path FROM mysql_instances WHERE id=?`, id).
			Scan(&instance.Host, &instance.Port, &instance.Username, &instance.TLSMode, &instance.CAPath); err != nil {
			return fmt.Errorf("bind legacy MySQL credential %s: %w", id, err)
		}
		bindings[id] = bindingForInstance(instance)
		changed = true
	}
	for id := range bindings {
		if _, ok := values[id]; !ok {
			delete(bindings, id)
			changed = true
		}
	}
	if changed {
		return store.writeBindings(bindings)
	}
	return nil
}

func (store credentialStore) loadBindings() (map[string]credentialBinding, error) {
	body, err := os.ReadFile(store.bindingPath())
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]credentialBinding), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sealed MySQL credential bindings: %w", err)
	}
	if len(body) > 4<<20 {
		return nil, errors.New("sealed MySQL credential binding file is too large")
	}
	plain, err := store.vault.Unseal(mysqlCredentialBindingPurpose, body)
	if err != nil {
		return nil, fmt.Errorf("unseal MySQL credential bindings: %w", err)
	}
	values := make(map[string]credentialBinding)
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, fmt.Errorf("decode MySQL credential bindings: %w", err)
	}
	return values, nil
}

func (store credentialStore) writeBindings(values map[string]credentialBinding) error {
	plain, err := json.Marshal(values)
	if err != nil {
		return err
	}
	body, err := store.vault.Seal(mysqlCredentialBindingPurpose, plain)
	if err != nil {
		return fmt.Errorf("seal MySQL credential bindings: %w", err)
	}
	return writePrivateFile(store.bindingPath(), body)
}

func (store credentialStore) load() (map[string]string, error) {
	if err := store.ensureMigrated(); err != nil {
		return nil, err
	}
	path := store.sealedPath()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sealed MySQL credentials: %w", err)
	}
	if len(body) > 4<<20 {
		return nil, errors.New("sealed MySQL credential file is too large")
	}
	plain, err := store.vault.Unseal(mysqlCredentialPurpose, body)
	if err != nil {
		return nil, fmt.Errorf("unseal MySQL credentials: %w", err)
	}
	values := make(map[string]string)
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, fmt.Errorf("decode MySQL credentials: %w", err)
	}
	return values, nil
}

func (store credentialStore) write(values map[string]string) error {
	plain, err := json.Marshal(values)
	if err != nil {
		return err
	}
	body, err := store.vault.Seal(mysqlCredentialPurpose, plain)
	if err != nil {
		return fmt.Errorf("seal MySQL credentials: %w", err)
	}
	return writePrivateFile(store.sealedPath(), body)
}

func (store credentialStore) ensureMigrated() error {
	if store.vault == nil {
		return errors.New("MySQL credential store is unavailable")
	}
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(store.directory, 0o700)
	legacyKeyPath := filepath.Join(store.directory, "mysql-credentials.key")
	legacyDataPath := filepath.Join(store.directory, "mysql-credentials.enc")
	key, keyErr := os.ReadFile(legacyKeyPath)
	data, dataErr := os.ReadFile(legacyDataPath)
	hasKey := keyErr == nil
	hasData := dataErr == nil
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return fmt.Errorf("read legacy MySQL credential key: %w", keyErr)
	}
	if dataErr != nil && !errors.Is(dataErr, os.ErrNotExist) {
		return fmt.Errorf("read legacy MySQL credentials: %w", dataErr)
	}
	if !hasKey && !hasData {
		return nil
	}
	if hasKey != hasData {
		if hasKey {
			if err := os.Remove(legacyKeyPath); err != nil {
				return fmt.Errorf("remove orphaned legacy MySQL key: %w", err)
			}
		} else if err := os.Remove(legacyDataPath); err != nil {
			return fmt.Errorf("remove undecryptable legacy MySQL credentials: %w", err)
		}
		return nil
	}
	if _, err := os.Stat(store.sealedPath()); errors.Is(err, os.ErrNotExist) {
		values, decryptErr := decryptLegacyMySQLCredentials(key, data)
		if decryptErr != nil {
			return decryptErr
		}
		if err := store.write(values); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("inspect sealed MySQL credentials: %w", err)
	}
	// Delete the raw legacy key first. If removing the old ciphertext then
	// fails, a State Root copy still cannot decrypt it.
	if err := os.Remove(legacyKeyPath); err != nil {
		return fmt.Errorf("remove legacy MySQL credential key: %w", err)
	}
	if err := os.Remove(legacyDataPath); err != nil {
		return fmt.Errorf("remove legacy MySQL credentials: %w", err)
	}
	return nil
}

func decryptLegacyMySQLCredentials(key, body []byte) (map[string]string, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid legacy MySQL credential key")
	}
	sealed := make(map[string][]byte)
	if err := json.Unmarshal(body, &sealed); err != nil {
		return nil, fmt.Errorf("decode legacy MySQL credentials: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(sealed))
	for id, ciphertext := range sealed {
		if len(ciphertext) < gcm.NonceSize() {
			return nil, errors.New("legacy encrypted MySQL credential is truncated")
		}
		plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], []byte(id))
		if err != nil {
			return nil, fmt.Errorf("decrypt legacy MySQL credential: %w", err)
		}
		values[id] = string(plain)
	}
	return values, nil
}

func (store credentialStore) sealedPath() string {
	return filepath.Join(store.directory, "mysql-credentials.v2.enc")
}

func (store credentialStore) bindingPath() string {
	return filepath.Join(store.directory, "mysql-credential-bindings.v1.enc")
}

func writePrivateFile(path string, body []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".mysql-secret-*")
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
