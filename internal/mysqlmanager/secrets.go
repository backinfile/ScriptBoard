package mysqlmanager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type credentialStore struct {
	directory string
}

func (s credentialStore) get(id string) (string, error) {
	values, _, err := s.load()
	if err != nil {
		return "", err
	}
	sealed, ok := values[id]
	if !ok {
		return "", errors.New("MySQL credential is unavailable")
	}
	key, err := os.ReadFile(filepath.Join(s.directory, "mysql-credentials.key"))
	if err != nil {
		return "", err
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
		return "", errors.New("encrypted MySQL credential is truncated")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(id))
	if err != nil {
		return "", fmt.Errorf("decrypt MySQL credential: %w", err)
	}
	return string(plain), nil
}

func (s credentialStore) set(id, password string) error {
	values, key, err := s.load()
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
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	values[id] = gcm.Seal(nonce, nonce, []byte(password), []byte(id))
	return s.write(values)
}

func (s credentialStore) delete(id string) error {
	values, _, err := s.load()
	if err != nil {
		return err
	}
	delete(values, id)
	return s.write(values)
}

func (s credentialStore) load() (map[string][]byte, []byte, error) {
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return nil, nil, err
	}
	_ = os.Chmod(s.directory, 0o700)
	keyPath := filepath.Join(s.directory, "mysql-credentials.key")
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, nil, err
		}
		if err := writePrivateFile(keyPath, key); err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	}
	if len(key) != 32 {
		return nil, nil, errors.New("invalid MySQL credential key")
	}
	values := make(map[string][]byte)
	body, err := os.ReadFile(filepath.Join(s.directory, "mysql-credentials.enc"))
	if err == nil {
		if err := json.Unmarshal(body, &values); err != nil {
			return nil, nil, fmt.Errorf("decode MySQL credentials: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	return values, key, nil
}

func (s credentialStore) write(values map[string][]byte) error {
	body, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(s.directory, "mysql-credentials.enc"), body)
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
	_ = os.Remove(path)
	return os.Rename(temporaryPath, path)
}
