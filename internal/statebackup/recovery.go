package statebackup

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/instancelock"
	"scriptboard/internal/secretstore"
)

const (
	recoveryMagic       = "SBHR\x00\x01"
	recoveryFormat      = 1
	maximumRecoveryFile = 1 << 20
	maximumSealedKey    = 64 << 10
)

type ExportRecoveryMaterialRequest struct {
	StateRoot   string
	Destination string
	Passphrase  []byte
	Now         time.Time
	Random      io.Reader
}

type RecoveryMaterialArtifact struct {
	Path        string    `json:"path"`
	StateRootID string    `json:"state_root_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type HostRecoveryRequest struct {
	StateRoot                  string
	ArchivePath                string
	ArchivePassphrase          []byte
	RecoveryMaterialPath       string
	RecoveryMaterialPassphrase []byte
	ConfirmBackupID            string
	MinimumSchemaVersion       int
	MaximumSchemaVersion       int
	Now                        time.Time
}

type recoveryPayload struct {
	FormatVersion   int    `json:"format_version"`
	StateRootID     string `json:"state_root_id"`
	CreatedAt       string `json:"created_at"`
	CredentialKey   string `json:"credential_key"`
	AuditSigningKey string `json:"audit_signing_key"`
}

type recoveryEncryptionHeader struct {
	Format      int    `json:"format"`
	KDF         string `json:"kdf"`
	Salt        string `json:"salt"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	Nonce       string `json:"nonce"`
}

func ExportRecoveryMaterial(ctx context.Context, request ExportRecoveryMaterialRequest) (artifact RecoveryMaterialArtifact, resultErr error) {
	if err := ctx.Err(); err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	root, err := absoluteStateRoot(request.StateRoot)
	if err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	destination, err := validateRecoveryDestination(root, request.Destination)
	if err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	if len(request.Passphrase) < 16 {
		return RecoveryMaterialArtifact{}, errors.New("host recovery passphrase must contain at least 16 bytes")
	}
	vault, err := secretstore.Open(root)
	if err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	credentialKey := vault.RecoveryKey()
	defer clearSensitiveBytes(credentialKey)
	stateRootID, signingKeyPath, _, err := auditcheckpoint.PathsForStateRoot(root)
	if err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	signingKey, err := readBoundedRecoveryFile(signingKeyPath, maximumSealedKey)
	if err != nil {
		return RecoveryMaterialArtifact{}, fmt.Errorf("read external audit signing key: %w", err)
	}
	defer clearSensitiveBytes(signingKey)
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload := recoveryPayload{
		FormatVersion: recoveryFormat, StateRootID: stateRootID, CreatedAt: now.Format(time.RFC3339Nano),
		CredentialKey:   base64.RawStdEncoding.EncodeToString(credentialKey),
		AuditSigningKey: base64.RawStdEncoding.EncodeToString(signingKey),
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	defer clearSensitiveBytes(plaintext)
	randomSource := request.Random
	if randomSource == nil {
		randomSource = rand.Reader
	}
	body, err := encryptRecoveryPayload(plaintext, request.Passphrase, randomSource)
	if err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return RecoveryMaterialArtifact{}, fmt.Errorf("create host recovery material without overwrite: %w", err)
	}
	committed := false
	closed := false
	defer func() {
		if !closed {
			if closeErr := file.Close(); resultErr == nil && closeErr != nil {
				resultErr = closeErr
			}
		}
		if !committed {
			_ = os.Remove(destination)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	if err := file.Sync(); err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	if err := file.Close(); err != nil {
		return RecoveryMaterialArtifact{}, err
	}
	closed = true
	committed = true
	return RecoveryMaterialArtifact{Path: destination, StateRootID: stateRootID, CreatedAt: now}, nil
}

func RecoverHost(ctx context.Context, request HostRecoveryRequest) (RestoreResult, error) {
	root, err := absoluteStateRoot(request.StateRoot)
	if err != nil {
		return RestoreResult{}, err
	}
	lock, err := instancelock.Acquire(root)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("host recovery requires all ScriptBoard components to be stopped: %w", err)
	}
	defer lock.Close()
	if err := requireEmptyRecoveryRoot(root); err != nil {
		return RestoreResult{}, err
	}
	stateRootID, signingKeyPath, checkpointPath, err := auditcheckpoint.PathsForStateRoot(root)
	if err != nil {
		return RestoreResult{}, err
	}
	if request.RecoveryMaterialPath == "" || !filepath.IsAbs(request.RecoveryMaterialPath) {
		return RestoreResult{}, errors.New("host recovery material path must be absolute")
	}
	recoveryMaterialPath, err := canonicalRegularFile(request.RecoveryMaterialPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if withinPath(root, recoveryMaterialPath) || withinPath(stageRootFor(root), recoveryMaterialPath) || withinPath(filepath.Dir(signingKeyPath), recoveryMaterialPath) {
		return RestoreResult{}, errors.New("host recovery material must remain outside State Root, restore staging, and the live external secrets directory")
	}
	payload, credentialKey, signingKey, err := readRecoveryMaterial(recoveryMaterialPath, request.RecoveryMaterialPassphrase)
	if err != nil {
		return RestoreResult{}, err
	}
	defer clearSensitiveBytes(credentialKey)
	defer clearSensitiveBytes(signingKey)
	if payload.StateRootID != stateRootID {
		return RestoreResult{}, errors.New("host recovery material belongs to a different canonical State Root path")
	}
	manifest, err := Inspect(ctx, request.ArchivePath, request.ArchivePassphrase)
	if err != nil {
		return RestoreResult{}, err
	}
	if request.ConfirmBackupID == "" || request.ConfirmBackupID != manifest.ID {
		return RestoreResult{}, errors.New("host recovery confirmation must exactly match the backup ID")
	}
	for _, path := range []string{signingKeyPath, checkpointPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return RestoreResult{}, errors.New("host recovery refuses to replace existing external trust material")
		}
	}
	credentialKeyPath, err := secretstore.InstallRecoveryKey(root, credentialKey)
	if err != nil {
		return RestoreResult{}, err
	}
	installed := []string{credentialKeyPath}
	committed := false
	defer func() {
		if !committed {
			for index := len(installed) - 1; index >= 0; index-- {
				_ = os.Remove(installed[index])
			}
		}
	}()
	if err := writeRecoveryFileExclusive(signingKeyPath, signingKey); err != nil {
		return RestoreResult{}, err
	}
	installed = append(installed, signingKeyPath)
	if err := writeRecoveryFileExclusive(checkpointPath, manifest.AuditCheckpoint); err != nil {
		return RestoreResult{}, err
	}
	installed = append(installed, checkpointPath)
	vault, err := secretstore.Open(root)
	if err != nil {
		return RestoreResult{}, err
	}
	checkpoint, err := auditcheckpoint.New(auditcheckpoint.Options{StateRoot: root, SecretStore: vault, ReadOnly: false})
	if err != nil {
		return RestoreResult{}, err
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := restore(ctx, RestoreRequest{
		StateRoot: root, ArchivePath: request.ArchivePath, Passphrase: request.ArchivePassphrase,
		ConfirmBackupID: request.ConfirmBackupID, MinimumSchemaVersion: request.MinimumSchemaVersion,
		MaximumSchemaVersion: request.MaximumSchemaVersion,
		ValidateStaged: func(ctx context.Context, databasePath string, staged Manifest) error {
			database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro")
			if err != nil {
				return err
			}
			defer database.Close()
			_, err = checkpoint.VerifyDetached(ctx, auditlog.New(database), staged.AuditCheckpoint)
			return err
		},
		Finalize: func(ctx context.Context, restored RestoreResult) error {
			database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "app.db"))+"?mode=rw&_pragma=busy_timeout(5000)")
			if err != nil {
				return err
			}
			defer database.Close()
			audit := auditlog.New(database)
			if err := checkpoint.VerifyOrBootstrap(ctx, audit, now); err != nil {
				return err
			}
			event := auditlog.Event{OccurredAt: strconv.FormatInt(now.Unix(), 10), Action: "state_backup.recover_host", Target: restored.Manifest.ID, Result: "succeeded", SourceAddress: "local-emergency-cli", ActorRole: "local-administrator", AuthenticationAssurance: "local-os-access"}
			if _, err := audit.Append(ctx, event); err != nil {
				return err
			}
			return checkpoint.Write(ctx, audit, now)
		},
	}, false)
	if err != nil {
		return RestoreResult{}, err
	}
	committed = true
	_ = os.Remove(result.PreservedStatePath)
	return result, nil
}

func validateRecoveryDestination(root, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || !filepath.IsAbs(raw) {
		return "", errors.New("host recovery material destination must be absolute")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(raw)))
	if err != nil {
		return "", errors.New("host recovery material destination parent must exist")
	}
	destination := filepath.Join(parent, filepath.Base(filepath.Clean(raw)))
	credentialKeyPath, err := secretstore.KeyPathForStateRoot(root)
	if err != nil {
		return "", err
	}
	if withinPath(root, destination) || withinPath(stageRootFor(root), destination) || withinPath(filepath.Dir(credentialKeyPath), destination) {
		return "", errors.New("host recovery material must be stored outside State Root, restore staging, and the live external secrets directory")
	}
	return destination, nil
}

func requireEmptyRecoveryRoot(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "instance.lock" {
			return errors.New("host recovery requires an empty, never-initialized State Root")
		}
	}
	return nil
}

func readRecoveryMaterial(rawPath string, passphrase []byte) (recoveryPayload, []byte, []byte, error) {
	if len(passphrase) < 16 {
		return recoveryPayload{}, nil, nil, errors.New("host recovery passphrase must contain at least 16 bytes")
	}
	path, err := canonicalRegularFile(rawPath)
	if err != nil {
		return recoveryPayload{}, nil, nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) > maximumRecoveryFile {
		return recoveryPayload{}, nil, nil, errors.New("read bounded host recovery material")
	}
	plaintext, err := decryptRecoveryPayload(body, passphrase)
	if err != nil {
		return recoveryPayload{}, nil, nil, err
	}
	defer clearSensitiveBytes(plaintext)
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var payload recoveryPayload
	if err := decoder.Decode(&payload); err != nil {
		return recoveryPayload{}, nil, nil, errors.New("host recovery material payload is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return recoveryPayload{}, nil, nil, errors.New("host recovery material contains trailing data")
	}
	createdAt, timeErr := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	stateID, idErr := hex.DecodeString(payload.StateRootID)
	credentialKey, keyErr := base64.RawStdEncoding.DecodeString(payload.CredentialKey)
	signingKey, signingErr := base64.RawStdEncoding.DecodeString(payload.AuditSigningKey)
	if payload.FormatVersion != recoveryFormat || timeErr != nil || createdAt.IsZero() || idErr != nil || len(stateID) != 16 || keyErr != nil || len(credentialKey) != 32 || signingErr != nil || len(signingKey) == 0 || len(signingKey) > maximumSealedKey {
		clearSensitiveBytes(credentialKey)
		clearSensitiveBytes(signingKey)
		return recoveryPayload{}, nil, nil, errors.New("host recovery material fields are invalid")
	}
	return payload, credentialKey, signingKey, nil
}

func encryptRecoveryPayload(plaintext, passphrase []byte, randomSource io.Reader) ([]byte, error) {
	salt := make([]byte, 16)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := io.ReadFull(randomSource, salt); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(randomSource, nonce); err != nil {
		return nil, err
	}
	header := recoveryEncryptionHeader{Format: recoveryFormat, KDF: "argon2id", Salt: base64.RawStdEncoding.EncodeToString(salt), MemoryKiB: kdfMemoryKiB, Iterations: kdfIterations, Parallelism: kdfParallelism, Nonce: base64.RawStdEncoding.EncodeToString(nonce)}
	headerBody, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	key := argon2.IDKey(passphrase, salt, header.Iterations, header.MemoryKiB, header.Parallelism, chacha20poly1305.KeySize)
	defer clearSensitiveBytes(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, append([]byte(recoveryMagic), headerBody...))
	output := bytes.NewBuffer(make([]byte, 0, len(recoveryMagic)+8+len(headerBody)+len(ciphertext)))
	output.WriteString(recoveryMagic)
	_ = binary.Write(output, binary.BigEndian, uint32(len(headerBody)))
	output.Write(headerBody)
	_ = binary.Write(output, binary.BigEndian, uint32(len(ciphertext)))
	output.Write(ciphertext)
	return output.Bytes(), nil
}

func decryptRecoveryPayload(body, passphrase []byte) ([]byte, error) {
	reader := bytes.NewReader(body)
	magic := make([]byte, len(recoveryMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != recoveryMagic {
		return nil, errors.New("host recovery material format is invalid")
	}
	var headerLength uint32
	if err := binary.Read(reader, binary.BigEndian, &headerLength); err != nil || headerLength == 0 || headerLength > maximumHeaderSize {
		return nil, errors.New("host recovery material encryption header is invalid")
	}
	headerBody := make([]byte, headerLength)
	if _, err := io.ReadFull(reader, headerBody); err != nil {
		return nil, errors.New("host recovery material encryption header is truncated")
	}
	var header recoveryEncryptionHeader
	decoder := json.NewDecoder(bytes.NewReader(headerBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil || header.Format != recoveryFormat || header.KDF != "argon2id" || header.MemoryKiB != kdfMemoryKiB || header.Iterations != kdfIterations || header.Parallelism != kdfParallelism {
		return nil, errors.New("host recovery material encryption parameters are unsupported")
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(header.Salt)
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(header.Nonce)
	if saltErr != nil || len(salt) != 16 || nonceErr != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return nil, errors.New("host recovery material encryption parameters are invalid")
	}
	var ciphertextLength uint32
	if err := binary.Read(reader, binary.BigEndian, &ciphertextLength); err != nil || ciphertextLength < uint32(chacha20poly1305.Overhead) || ciphertextLength > maximumRecoveryFile || int64(ciphertextLength) != int64(reader.Len()) {
		return nil, errors.New("host recovery material ciphertext is invalid")
	}
	ciphertext := make([]byte, ciphertextLength)
	if _, err := io.ReadFull(reader, ciphertext); err != nil {
		return nil, errors.New("host recovery material ciphertext is truncated")
	}
	key := argon2.IDKey(passphrase, salt, header.Iterations, header.MemoryKiB, header.Parallelism, chacha20poly1305.KeySize)
	defer clearSensitiveBytes(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, append([]byte(recoveryMagic), headerBody...))
	if err != nil {
		return nil, errors.New("host recovery material authentication failed")
	}
	return plaintext, nil
}

func readBoundedRecoveryFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("recovery dependency must be a bounded regular file")
	}
	return os.ReadFile(path)
}

func writeRecoveryFileExclusive(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("install external host recovery material without overwrite: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

func clearSensitiveBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
