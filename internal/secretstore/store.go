package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"scriptboard/internal/privatepath"
)

const sealedMagic = "SBSEAL1"

type Store struct {
	key     [32]byte
	keyPath string
}

func New(stateRoot string) (*Store, error) {
	keyPath, err := KeyPathForStateRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(keyPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create external credential key directory: %w", err)
	}
	if err := privatepath.ProtectDirectory(directory); err != nil {
		return nil, fmt.Errorf("protect external credential key directory: %w", err)
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	store := &Store{keyPath: keyPath}
	copy(store.key[:], key)
	return store, nil
}

func KeyPathForStateRoot(stateRoot string) (string, error) {
	absolute, err := filepath.Abs(stateRoot)
	if err != nil {
		return "", fmt.Errorf("resolve State Root for credential key: %w", err)
	}
	identity := filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	digest := sha256.Sum256([]byte(identity))
	return filepath.Join(filepath.Dir(absolute), "secrets", "credential-master-"+hex.EncodeToString(digest[:8])+".key"), nil
}

func (store *Store) KeyPath() string { return store.keyPath }

func (store *Store) Seal(purpose string, plaintext []byte) ([]byte, error) {
	if err := validatePurpose(purpose); err != nil {
		return nil, err
	}
	gcm, err := store.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate credential nonce: %w", err)
	}
	output := make([]byte, 0, len(sealedMagic)+len(nonce)+len(plaintext)+gcm.Overhead())
	output = append(output, sealedMagic...)
	output = append(output, nonce...)
	return gcm.Seal(output, nonce, plaintext, []byte(purpose)), nil
}

func (store *Store) Unseal(purpose string, sealed []byte) ([]byte, error) {
	if err := validatePurpose(purpose); err != nil {
		return nil, err
	}
	gcm, err := store.gcm()
	if err != nil {
		return nil, err
	}
	if len(sealed) < len(sealedMagic)+gcm.NonceSize()+gcm.Overhead() || string(sealed[:len(sealedMagic)]) != sealedMagic {
		return nil, errors.New("sealed credential has an invalid format")
	}
	nonce := sealed[len(sealedMagic) : len(sealedMagic)+gcm.NonceSize()]
	ciphertext := sealed[len(sealedMagic)+gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(purpose))
	if err != nil {
		return nil, errors.New("sealed credential cannot be decrypted with this host key")
	}
	return plain, nil
}

func (store *Store) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(store.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func loadOrCreateKey(path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if err == nil {
		return decodeKey(body)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read external credential key: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, fmt.Errorf("generate external credential key: %w", err)
	}
	wrapped, err := wrapKey(raw)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read concurrently created credential key: %w", readErr)
		}
		return decodeKey(body)
	}
	if err != nil {
		return nil, fmt.Errorf("create external credential key: %w", err)
	}
	if _, writeErr := file.Write(wrapped); writeErr != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write external credential key: %w", writeErr)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync external credential key: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close external credential key: %w", err)
	}
	return raw, nil
}

func decodeKey(body []byte) ([]byte, error) {
	key, err := unwrapKey(body)
	if err != nil {
		return nil, fmt.Errorf("unwrap external credential key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("external credential key has an invalid length")
	}
	return key, nil
}

func validatePurpose(purpose string) error {
	if strings.TrimSpace(purpose) != purpose || purpose == "" || len(purpose) > 128 || strings.IndexFunc(purpose, unicode.IsControl) >= 0 {
		return errors.New("credential purpose is invalid")
	}
	return nil
}
