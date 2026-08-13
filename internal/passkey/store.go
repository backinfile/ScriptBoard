package passkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/go-webauthn/webauthn/webauthn"

	"scriptboard/internal/secretstore"
)

const (
	storePurpose       = "account-passkey-state-v1"
	maxStoreBytes      = 4 << 20
	maxPerUser         = 10
	maxCredentialBytes = 64 << 10
)

var (
	ErrDuplicateCredential        = errors.New("passkey credential is already registered")
	ErrCredentialLimit            = errors.New("passkey credential limit reached")
	ErrCredentialNotFound         = errors.New("passkey credential was not found")
	ErrCredentialIdentityMismatch = errors.New("passkey credential identity fields changed")
	ErrCredentialTooLarge         = errors.New("passkey credential is too large")
)

type Options struct {
	StateRoot   string
	SecretStore *secretstore.Store
}

type Store struct {
	path  string
	vault *secretstore.Store
	mu    sync.Mutex
}

type User struct {
	ID          string
	Name        string
	Credentials []webauthn.Credential
}

func (user User) WebAuthnID() []byte                         { return []byte(user.ID) }
func (user User) WebAuthnName() string                       { return user.Name }
func (user User) WebAuthnDisplayName() string                { return user.Name }
func (user User) WebAuthnCredentials() []webauthn.Credential { return user.Credentials }

type CredentialView struct {
	ID   string
	Name string
}

type credentialRecord struct {
	Name       string              `json:"name"`
	Credential webauthn.Credential `json:"credential"`
}

func New(options Options) (*Store, error) {
	root, err := filepath.Abs(options.StateRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve passkey State Root: %w", err)
	}
	vault := options.SecretStore
	if vault == nil {
		vault, err = secretstore.New(root)
		if err != nil {
			return nil, err
		}
	}
	directory := filepath.Join(root, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create passkey state directory: %w", err)
	}
	_ = os.Chmod(directory, 0o700)
	return &Store{path: filepath.Join(directory, "account-passkeys.enc"), vault: vault}, nil
}

func (store *Store) User(userID, username string) (User, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return User{}, err
	}
	records := entries[userID]
	credentials := make([]webauthn.Credential, 0, len(records))
	for _, record := range records {
		credentials = append(credentials, record.Credential)
	}
	return User{ID: userID, Name: username, Credentials: credentials}, nil
}

func (store *Store) List(userID string) ([]CredentialView, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return nil, err
	}
	views := make([]CredentialView, 0, len(entries[userID]))
	for _, record := range entries[userID] {
		views = append(views, CredentialView{ID: fmt.Sprintf("%x", record.Credential.ID), Name: record.Name})
	}
	return views, nil
}

func (store *Store) Add(userID, name string, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil || len(encoded) > maxCredentialBytes {
		return ErrCredentialTooLarge
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return err
	}
	for _, records := range entries {
		for _, existing := range records {
			if bytes.Equal(existing.Credential.ID, credential.ID) {
				return ErrDuplicateCredential
			}
		}
	}
	if len(entries[userID]) >= maxPerUser {
		return ErrCredentialLimit
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	nameRunes := []rune(name)
	if len(nameRunes) > 64 {
		name = string(nameRunes[:64])
	}
	entries[userID] = append(entries[userID], credentialRecord{Name: name, Credential: credential})
	return store.write(entries)
}

// Update persists the authenticator counter and flags returned by a successful
// assertion. The entire verified credential is replaced atomically.
func (store *Store) Update(userID string, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil || len(encoded) > maxCredentialBytes {
		return ErrCredentialTooLarge
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return err
	}
	for index := range entries[userID] {
		if bytes.Equal(entries[userID][index].Credential.ID, credential.ID) {
			existing := entries[userID][index].Credential
			candidate := credential
			candidate.Flags = existing.Flags
			candidate.Authenticator.SignCount = existing.Authenticator.SignCount
			candidate.Authenticator.CloneWarning = existing.Authenticator.CloneWarning
			if credential.Flags.BackupEligible != existing.Flags.BackupEligible || !reflect.DeepEqual(candidate, existing) {
				return ErrCredentialIdentityMismatch
			}
			updated := existing
			updated.Flags.BackupState = credential.Flags.BackupState
			updated.Authenticator.SignCount = credential.Authenticator.SignCount
			updated.Authenticator.CloneWarning = credential.Authenticator.CloneWarning
			entries[userID][index].Credential = updated
			return store.write(entries)
		}
	}
	return ErrCredentialNotFound
}

func (store *Store) Delete(userID, credentialID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return err
	}
	for index, record := range entries[userID] {
		if fmt.Sprintf("%x", record.Credential.ID) == credentialID {
			entries[userID] = append(entries[userID][:index], entries[userID][index+1:]...)
			return store.write(entries)
		}
	}
	return ErrCredentialNotFound
}

func (store *Store) Reset(userID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return err
	}
	delete(entries, userID)
	return store.write(entries)
}

func (store *Store) load() (map[string][]credentialRecord, error) {
	body, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string][]credentialRecord), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read passkey state: %w", err)
	}
	if len(body) > maxStoreBytes {
		return nil, errors.New("passkey state is too large")
	}
	plain, err := store.vault.Unseal(storePurpose, body)
	if err != nil {
		return nil, fmt.Errorf("unseal passkey state: %w", err)
	}
	entries := make(map[string][]credentialRecord)
	if err := json.Unmarshal(plain, &entries); err != nil {
		return nil, fmt.Errorf("decode passkey state: %w", err)
	}
	return entries, nil
}

func (store *Store) write(entries map[string][]credentialRecord) error {
	plain, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("encode passkey state: %w", err)
	}
	body, err := store.vault.Seal(storePurpose, plain)
	if err != nil {
		return fmt.Errorf("seal passkey state: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".account-passkeys-*.tmp")
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
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace passkey state: %w", err)
	}
	return nil
}
