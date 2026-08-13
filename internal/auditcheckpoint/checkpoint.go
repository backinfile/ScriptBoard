package auditcheckpoint

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/secretstore"
)

const (
	checkpointVersion = "scriptboard-audit-checkpoint-v1"
	keyPurpose        = "audit-checkpoint-signing-key-v1"
	maxCheckpointSize = 64 << 10
	lockStaleAfter    = time.Minute
	lockRetryInterval = 25 * time.Millisecond
)

type Options struct {
	StateRoot   string
	SecretStore *secretstore.Store
	ReadOnly    bool
}

type Store struct {
	vault          *secretstore.Store
	stateRootID    string
	keyPath        string
	checkpointPath string
	privateKey     ed25519.PrivateKey
	newKey         bool
	readOnly       bool
	trusted        *checkpointDocument
	mu             sync.Mutex
}

type unsignedCheckpoint struct {
	Version     string `json:"version"`
	StateRootID string `json:"state_root_id"`
	EventID     int64  `json:"event_id"`
	EventSHA256 string `json:"event_sha256"`
	SignedAt    int64  `json:"signed_at"`
	PublicKey   string `json:"public_key"`
}

type checkpointDocument struct {
	unsignedCheckpoint
	Signature string `json:"signature"`
}

type DocumentInfo struct {
	EventID     int64
	EventSHA256 string
	SignedAt    time.Time
}

func New(options Options) (*Store, error) {
	absolute, err := filepath.Abs(options.StateRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve audit checkpoint State Root: %w", err)
	}
	vault := options.SecretStore
	if vault == nil {
		if options.ReadOnly {
			vault, err = secretstore.Open(absolute)
		} else {
			vault, err = secretstore.New(absolute)
		}
		if err != nil {
			return nil, err
		}
	}
	stateRootID, keyPath, checkpointPath, err := PathsForStateRoot(absolute)
	if err != nil {
		return nil, err
	}
	store := &Store{
		vault: vault, stateRootID: stateRootID, keyPath: keyPath, checkpointPath: checkpointPath, readOnly: options.ReadOnly,
	}
	sealedKey, keyErr := os.ReadFile(store.keyPath)
	_, checkpointErr := os.Stat(store.checkpointPath)
	keyExists := keyErr == nil
	checkpointExists := checkpointErr == nil
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read audit checkpoint signing key: %w", keyErr)
	}
	if checkpointErr != nil && !errors.Is(checkpointErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect external audit checkpoint: %w", checkpointErr)
	}
	if checkpointExists && !keyExists {
		return nil, errors.New("external audit checkpoint signing key is missing")
	}
	if options.ReadOnly && !keyExists {
		return nil, errors.New("external audit checkpoint signing key is missing")
	}
	if keyExists {
		plain, err := vault.Unseal(keyPurpose, sealedKey)
		if err != nil {
			return nil, fmt.Errorf("unseal audit checkpoint signing key: %w", err)
		}
		if len(plain) != ed25519.PrivateKeySize {
			return nil, errors.New("audit checkpoint signing key has an invalid length")
		}
		store.privateKey = append(ed25519.PrivateKey(nil), plain...)
		return store, nil
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate audit checkpoint signing key: %w", err)
	}
	store.privateKey = privateKey
	store.newKey = true
	return store, nil
}

func PathsForStateRoot(stateRoot string) (stateRootID, keyPath, checkpointPath string, err error) {
	absolute, err := filepath.Abs(stateRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve audit checkpoint State Root: %w", err)
	}
	identity := filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	digest := sha256.Sum256([]byte(identity))
	stateRootID = hex.EncodeToString(digest[:16])
	masterKeyPath, err := secretstore.KeyPathForStateRoot(absolute)
	if err != nil {
		return "", "", "", err
	}
	directory := filepath.Dir(masterKeyPath)
	return stateRootID,
		filepath.Join(directory, "audit-checkpoint-signing-"+stateRootID[:16]+".enc"),
		filepath.Join(directory, "audit-checkpoint-"+stateRootID[:16]+".json"), nil
}

func (store *Store) KeyPath() string        { return store.keyPath }
func (store *Store) CheckpointPath() string { return store.checkpointPath }

func (store *Store) VerifyOrBootstrap(ctx context.Context, audit *auditlog.Store, now time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	body, err := os.ReadFile(store.checkpointPath)
	if errors.Is(err, os.ErrNotExist) {
		if store.readOnly || !store.newKey {
			return errors.New("external audit checkpoint is missing")
		}
		release, lockErr := store.acquireFileLock(ctx)
		if lockErr != nil {
			return lockErr
		}
		defer release()
		if _, statErr := os.Stat(store.checkpointPath); statErr == nil {
			return errors.New("external audit checkpoint appeared during initialization; retry startup")
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		verification, err := audit.Verify(ctx)
		if err != nil {
			return err
		}
		return store.bootstrap(verification, now)
	}
	if err != nil {
		return fmt.Errorf("read external audit checkpoint: %w", err)
	}
	document, err := store.verifyDocument(body)
	if err != nil {
		return err
	}
	if _, err := audit.VerifyWithCheckpoint(ctx, document.EventID, document.EventSHA256); err != nil {
		return fmt.Errorf("verify external audit checkpoint against local chain: %w", err)
	}
	store.trusted = &document
	return nil
}

func (store *Store) Write(ctx context.Context, audit *auditlog.Store, now time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.readOnly {
		return errors.New("read-only audit checkpoint store cannot write")
	}
	release, err := store.acquireFileLock(ctx)
	if err != nil {
		return err
	}
	defer release()
	if store.newKey {
		verification, err := audit.Verify(ctx)
		if err != nil {
			return err
		}
		return store.bootstrap(verification, now)
	}
	verification, err := store.verifyTrustedCheckpoint(ctx, audit)
	if err != nil {
		return err
	}
	return store.writeCheckpoint(verification, now)
}

func (store *Store) CheckpointEventID() int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.trusted == nil {
		return 0
	}
	return store.trusted.EventID
}

// TrustedDocument returns the exact signed document most recently verified by
// VerifyOrBootstrap. Callers can embed it in an authenticated recovery artifact
// without exporting the protected signing key.
func (store *Store) TrustedDocument() ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.trusted == nil {
		return nil, errors.New("external audit checkpoint has not been trusted")
	}
	body, err := os.ReadFile(store.checkpointPath)
	if err != nil {
		return nil, fmt.Errorf("read trusted external audit checkpoint: %w", err)
	}
	document, err := store.verifyDocument(body)
	if err != nil {
		return nil, err
	}
	if document != *store.trusted {
		return nil, errors.New("external audit checkpoint changed after verification")
	}
	return append([]byte(nil), body...), nil
}

// VerifyDetached validates a signed checkpoint embedded in a recovery artifact
// against the supplied audit database. It does not change the currently trusted
// external checkpoint.
func (store *Store) VerifyDetached(ctx context.Context, audit *auditlog.Store, body []byte) (DocumentInfo, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	document, err := store.verifyDocument(body)
	if err != nil {
		return DocumentInfo{}, fmt.Errorf("verify detached audit checkpoint document: %w", err)
	}
	if _, err := audit.VerifyWithCheckpoint(ctx, document.EventID, document.EventSHA256); err != nil {
		return DocumentInfo{}, fmt.Errorf("verify detached audit checkpoint against restored chain: %w", err)
	}
	return documentInfo(document), nil
}

// ReanchorRestoredState is the only supported backward checkpoint transition.
// It requires the exact currently trusted document and a separately signed
// checkpoint that belongs to the restored chain, records both identities in a
// new audit event, and only then replaces the external checkpoint.
func (store *Store) ReanchorRestoredState(ctx context.Context, audit *auditlog.Store, previousBody, backupBody []byte, event auditlog.Event, now time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.readOnly {
		return errors.New("read-only audit checkpoint store cannot reanchor restored state")
	}
	if store.trusted == nil {
		return errors.New("external audit checkpoint has not been trusted")
	}
	if event.Action != "state_backup.restore" {
		return errors.New("restored-state reanchor requires the state_backup.restore audit action")
	}
	release, err := store.acquireFileLock(ctx)
	if err != nil {
		return err
	}
	defer release()
	currentBody, err := os.ReadFile(store.checkpointPath)
	if err != nil {
		return fmt.Errorf("read external audit checkpoint before restored-state reanchor: %w", err)
	}
	if !bytes.Equal(currentBody, previousBody) {
		return errors.New("external audit checkpoint changed before restored-state reanchor")
	}
	previous, err := store.verifyDocument(previousBody)
	if err != nil {
		return err
	}
	if previous != *store.trusted {
		return errors.New("restored-state reanchor does not reference the trusted previous checkpoint")
	}
	backup, err := store.verifyDocument(backupBody)
	if err != nil {
		return fmt.Errorf("verify backup audit checkpoint document: %w", err)
	}
	if _, err := audit.VerifyWithCheckpoint(ctx, backup.EventID, backup.EventSHA256); err != nil {
		return fmt.Errorf("verify backup audit checkpoint against restored chain: %w", err)
	}
	previousDigest := sha256.Sum256(previousBody)
	backupDigest := sha256.Sum256(backupBody)
	event.Result = fmt.Sprintf("succeeded; previous_checkpoint_event_id=%d; backup_checkpoint_event_id=%d; backup_checkpoint_sha256=%s", previous.EventID, backup.EventID, hex.EncodeToString(backupDigest[:]))
	event.ResourceRevision = strconv.FormatInt(previous.EventID, 10)
	event.ResourceDigestSHA256 = hex.EncodeToString(previousDigest[:])
	if _, err := audit.Append(ctx, event); err != nil {
		return fmt.Errorf("append restored-state audit continuity event: %w", err)
	}
	verification, err := audit.Verify(ctx)
	if err != nil {
		return fmt.Errorf("verify restored audit chain after continuity event: %w", err)
	}
	return store.writeCheckpoint(verification, now)
}

func documentInfo(document checkpointDocument) DocumentInfo {
	return DocumentInfo{EventID: document.EventID, EventSHA256: document.EventSHA256, SignedAt: time.Unix(document.SignedAt, 0).UTC()}
}

func (store *Store) verifyTrustedCheckpoint(ctx context.Context, audit *auditlog.Store) (auditlog.Verification, error) {
	if store.trusted == nil {
		return auditlog.Verification{}, errors.New("external audit checkpoint has not been trusted")
	}
	body, err := os.ReadFile(store.checkpointPath)
	if err != nil {
		return auditlog.Verification{}, fmt.Errorf("read external audit checkpoint before refresh: %w", err)
	}
	document, err := store.verifyDocument(body)
	if err != nil {
		return auditlog.Verification{}, err
	}
	if document != *store.trusted {
		if document.EventID < store.trusted.EventID ||
			document.EventID == store.trusted.EventID && document.EventSHA256 != store.trusted.EventSHA256 {
			return auditlog.Verification{}, errors.New("external audit checkpoint moved backward or changed identity after verification")
		}
		if document.EventID == 0 && store.trusted.EventID > 0 {
			return auditlog.Verification{}, errors.New("external audit checkpoint moved back to the retained anchor")
		}
	}
	verification, err := audit.VerifyWithCheckpoint(ctx, document.EventID, document.EventSHA256)
	if err != nil {
		return auditlog.Verification{}, fmt.Errorf("refuse to refresh from an untrusted audit chain: %w", err)
	}
	store.trusted = &document
	return verification, nil
}

func (store *Store) acquireFileLock(ctx context.Context) (func(), error) {
	lockPath := store.checkpointPath + ".lock"
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, writeErr := fmt.Fprintf(file, "%d\n", os.Getpid())
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = os.Remove(lockPath)
				if writeErr != nil {
					return nil, writeErr
				}
				return nil, closeErr
			}
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("acquire external audit checkpoint lock: %w", err)
		}
		if info, statErr := os.Lstat(lockPath); statErr == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > lockStaleAfter {
			if removeErr := os.Remove(lockPath); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
				continue
			}
		}
		timer := time.NewTimer(lockRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for external audit checkpoint lock: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (store *Store) bootstrap(verification auditlog.Verification, now time.Time) error {
	created, err := store.writeKeyExclusive()
	if err != nil {
		return err
	}
	if err := store.writeCheckpoint(verification, now); err != nil {
		if created {
			_ = os.Remove(store.keyPath)
		}
		return err
	}
	store.newKey = false
	return nil
}

func (store *Store) writeKeyExclusive() (bool, error) {
	sealed, err := store.vault.Seal(keyPurpose, store.privateKey)
	if err != nil {
		return false, fmt.Errorf("seal audit checkpoint signing key: %w", err)
	}
	file, err := os.OpenFile(store.keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, errors.New("audit checkpoint signing key appeared concurrently")
	}
	if err != nil {
		return false, fmt.Errorf("create audit checkpoint signing key: %w", err)
	}
	if _, err := file.Write(sealed); err != nil {
		_ = file.Close()
		_ = os.Remove(store.keyPath)
		return false, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(store.keyPath)
		return false, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(store.keyPath)
		return false, err
	}
	return true, nil
}

func (store *Store) writeCheckpoint(verification auditlog.Verification, now time.Time) error {
	publicKey := store.privateKey.Public().(ed25519.PublicKey)
	unsigned := unsignedCheckpoint{
		Version: checkpointVersion, StateRootID: store.stateRootID,
		EventID: verification.LastID, EventSHA256: verification.LastHash,
		SignedAt: now.UTC().Unix(), PublicKey: base64.RawStdEncoding.EncodeToString(publicKey),
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return err
	}
	document := checkpointDocument{unsignedCheckpoint: unsigned, Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(store.privateKey, payload))}
	body, err := json.Marshal(document)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.checkpointPath), ".audit-checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("create audit checkpoint staging file: %w", err)
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
	if err := os.Rename(temporaryPath, store.checkpointPath); err != nil {
		return fmt.Errorf("replace external audit checkpoint: %w", err)
	}
	store.trusted = &document
	return nil
}

func (store *Store) verifyDocument(body []byte) (checkpointDocument, error) {
	if len(body) == 0 || len(body) > maxCheckpointSize {
		return checkpointDocument{}, errors.New("external audit checkpoint has an invalid size")
	}
	var document checkpointDocument
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return checkpointDocument{}, fmt.Errorf("decode external audit checkpoint: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return checkpointDocument{}, errors.New("external audit checkpoint has trailing data")
	}
	if document.Version != checkpointVersion || document.StateRootID != store.stateRootID || document.EventID < 0 || document.SignedAt <= 0 {
		return checkpointDocument{}, errors.New("external audit checkpoint identity is invalid")
	}
	if document.EventSHA256 != "" {
		decoded, err := hex.DecodeString(document.EventSHA256)
		if err != nil || len(decoded) != sha256.Size || document.EventSHA256 != strings.ToLower(document.EventSHA256) {
			return checkpointDocument{}, errors.New("external audit checkpoint digest is invalid")
		}
	} else if document.EventID != 0 {
		return checkpointDocument{}, errors.New("external audit checkpoint digest is missing")
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(document.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return checkpointDocument{}, errors.New("external audit checkpoint public key is invalid")
	}
	expectedPublic := store.privateKey.Public().(ed25519.PublicKey)
	if !bytes.Equal(publicKey, expectedPublic) {
		return checkpointDocument{}, errors.New("external audit checkpoint public key does not match the protected signing key")
	}
	signature, err := base64.RawStdEncoding.DecodeString(document.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return checkpointDocument{}, errors.New("external audit checkpoint signature is invalid")
	}
	payload, err := json.Marshal(document.unsignedCheckpoint)
	if err != nil {
		return checkpointDocument{}, err
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return checkpointDocument{}, errors.New("external audit checkpoint signature verification failed")
	}
	return document, nil
}
