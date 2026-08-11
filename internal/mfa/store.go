package mfa

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/secretstore"
)

const (
	storePurpose      = "account-mfa-state-v1"
	recoveryCodeCount = 10
	maxStoreBytes     = 4 << 20
)

var (
	ErrAlreadyEnabled   = errors.New("MFA is already enabled")
	ErrEnrollmentAbsent = errors.New("MFA enrollment is unavailable")
	ErrInvalidCode      = errors.New("MFA code is invalid")
)

type Options struct {
	StateRoot   string
	SecretStore *secretstore.Store
	Now         func() time.Time
}

type Store struct {
	path  string
	vault *secretstore.Store
	now   func() time.Time
	mu    sync.Mutex
}

type Status struct {
	Enabled       bool
	RecoveryCodes int
}

type Enrollment struct {
	Secret string
	URI    string
}

type entry struct {
	Secret         string   `json:"secret,omitempty"`
	PendingSecret  string   `json:"pending_secret,omitempty"`
	LastTOTPStep   int64    `json:"last_totp_step,omitempty"`
	RecoveryHashes []string `json:"recovery_hashes,omitempty"`
	Enabled        bool     `json:"enabled"`
}

func New(options Options) (*Store, error) {
	stateRoot, err := filepath.Abs(options.StateRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve MFA State Root: %w", err)
	}
	vault := options.SecretStore
	if vault == nil {
		vault, err = secretstore.New(stateRoot)
		if err != nil {
			return nil, fmt.Errorf("initialize MFA secret store: %w", err)
		}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	directory := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create MFA state directory: %w", err)
	}
	_ = os.Chmod(directory, 0o700)
	return &Store{path: filepath.Join(directory, "account-mfa.enc"), vault: vault, now: now}, nil
}

func (store *Store) Status(userID string) (Status, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return Status{}, err
	}
	value := entries[userID]
	return Status{Enabled: value.Enabled, RecoveryCodes: len(value.RecoveryHashes)}, nil
}

func (store *Store) Begin(userID, account string) (Enrollment, error) {
	if strings.TrimSpace(userID) == "" {
		return Enrollment{}, errors.New("MFA user ID is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return Enrollment{}, err
	}
	value := entries[userID]
	if value.Enabled {
		return Enrollment{}, ErrAlreadyEnabled
	}
	if value.PendingSecret != "" {
		return enrollmentFor(value.PendingSecret, account), nil
	}
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return Enrollment{}, fmt.Errorf("generate TOTP secret: %w", err)
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	value.PendingSecret = secret
	entries[userID] = value
	if err := store.write(entries); err != nil {
		return Enrollment{}, err
	}
	return enrollmentFor(secret, account), nil
}

func (store *Store) Confirm(userID, code string) ([]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return nil, err
	}
	value, ok := entries[userID]
	if !ok || value.PendingSecret == "" || value.Enabled {
		return nil, ErrEnrollmentAbsent
	}
	step, valid := validTOTP(value.PendingSecret, strings.TrimSpace(code), store.now().UTC(), -1)
	if !valid {
		return nil, ErrInvalidCode
	}
	codes := make([]string, 0, recoveryCodeCount)
	hashes := make([]string, 0, recoveryCodeCount)
	for range recoveryCodeCount {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
		hashes = append(hashes, recoveryHash(code))
	}
	value.Secret = value.PendingSecret
	value.PendingSecret = ""
	value.Enabled = true
	value.LastTOTPStep = step
	value.RecoveryHashes = hashes
	entries[userID] = value
	if err := store.write(entries); err != nil {
		return nil, err
	}
	return codes, nil
}

func (store *Store) Verify(userID, candidate string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := store.load()
	if err != nil {
		return false, err
	}
	value, ok := entries[userID]
	if !ok || !value.Enabled || value.Secret == "" {
		return false, nil
	}
	candidate = strings.TrimSpace(candidate)
	if step, valid := validTOTP(value.Secret, candidate, store.now().UTC(), value.LastTOTPStep); valid {
		value.LastTOTPStep = step
		entries[userID] = value
		if err := store.write(entries); err != nil {
			return false, err
		}
		return true, nil
	}
	hash := recoveryHash(candidate)
	for index, existing := range value.RecoveryHashes {
		if subtle.ConstantTimeCompare([]byte(existing), []byte(hash)) != 1 {
			continue
		}
		value.RecoveryHashes = append(value.RecoveryHashes[:index], value.RecoveryHashes[index+1:]...)
		entries[userID] = value
		if err := store.write(entries); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
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

func (store *Store) load() (map[string]entry, error) {
	body, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]entry), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read MFA state: %w", err)
	}
	if len(body) > maxStoreBytes {
		return nil, errors.New("MFA state is too large")
	}
	plain, err := store.vault.Unseal(storePurpose, body)
	if err != nil {
		return nil, fmt.Errorf("unseal MFA state: %w", err)
	}
	entries := make(map[string]entry)
	if err := json.Unmarshal(plain, &entries); err != nil {
		return nil, fmt.Errorf("decode MFA state: %w", err)
	}
	return entries, nil
}

func (store *Store) write(entries map[string]entry) error {
	plain, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("encode MFA state: %w", err)
	}
	body, err := store.vault.Seal(storePurpose, plain)
	if err != nil {
		return fmt.Errorf("seal MFA state: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".account-mfa-*.tmp")
	if err != nil {
		return fmt.Errorf("create MFA state staging file: %w", err)
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
		return fmt.Errorf("replace MFA state: %w", err)
	}
	return nil
}

func newRecoveryCode() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate MFA recovery code: %w", err)
	}
	compact := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	parts := make([]string, 0, 6)
	for len(compact) > 5 {
		parts = append(parts, compact[:5])
		compact = compact[5:]
	}
	if compact != "" {
		parts = append(parts, compact)
	}
	return strings.Join(parts, "-"), nil
}

func recoveryHash(code string) string {
	normalized := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	digest := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(digest[:])
}

func enrollmentFor(secret, account string) Enrollment {
	label := "ScriptBoard:" + strings.TrimSpace(account)
	query := url.Values{"secret": {secret}, "issuer": {"ScriptBoard"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return Enrollment{Secret: secret, URI: (&url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + label, RawQuery: query.Encode()}).String()}
}
