package externaltrigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const SignatureWindow = 5 * time.Minute

var (
	ErrSignatureInvalid = errors.New("external trigger signature is invalid")
	ErrSignatureReplay  = errors.New("external trigger signature nonce was already used")
	signatureNonce      = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

func RequestSignature(secret string, timestamp int64, nonce, method, requestURI string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signaturePayload(timestamp, nonce, method, requestURI)))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func signaturePayload(timestamp int64, nonce, method, requestURI string) string {
	return fmt.Sprintf("v1\n%d\n%s\n%s\n%s", timestamp, nonce, strings.ToUpper(method), requestURI)
}

func (manager *Manager) VerifyAndConsumeSignature(ctx context.Context, keyID, secret string, timestamp int64, nonce, method, requestURI, signature string) error {
	now := manager.now().UTC()
	if timestamp < now.Add(-SignatureWindow).Unix() || timestamp > now.Add(SignatureWindow).Unix() ||
		!signatureNonce.MatchString(nonce) || keyID == "" || secret == "" || requestURI == "" {
		return ErrSignatureInvalid
	}
	if len(signature) != len("v1=")+sha256.Size*2 || !strings.HasPrefix(signature, "v1=") {
		return ErrSignatureInvalid
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "v1="))
	if err != nil || len(provided) != sha256.Size {
		return ErrSignatureInvalid
	}
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(signaturePayload(timestamp, nonce, method, requestURI)))
	if !hmac.Equal(provided, expected.Sum(nil)) {
		return ErrSignatureInvalid
	}

	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrSignatureInvalid
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `DELETE FROM external_trigger_nonces WHERE expires_at <= ?`, now.Unix()); err != nil {
		return ErrSignatureInvalid
	}
	nonceExpiresAt := now.Add(SignatureWindow).Unix()
	if signatureExpiresAt := time.Unix(timestamp, 0).Add(SignatureWindow).Unix(); signatureExpiresAt > nonceExpiresAt {
		nonceExpiresAt = signatureExpiresAt
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO external_trigger_nonces (key_id, nonce, expires_at) VALUES (?, ?, ?)`, keyID, nonce, nonceExpiresAt); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrSignatureReplay
		}
		return ErrSignatureInvalid
	}
	if err := transaction.Commit(); err != nil {
		return ErrSignatureInvalid
	}
	return nil
}
