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

func RequestSignature(secret string, timestamp int64, nonce, method, requestURI, contentType string, body []byte) string {
	digest := BodySHA256(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signaturePayloadV2(timestamp, nonce, method, requestURI, contentType, int64(len(body)), digest)))
	return "v2=" + hex.EncodeToString(mac.Sum(nil))
}

func BodySHA256(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func signaturePayloadV2(timestamp int64, nonce, method, requestURI, contentType string, contentLength int64, bodySHA256 string) string {
	return fmt.Sprintf("v2\n%d\n%s\n%s\n%s\n%s\n%d\n%s", timestamp, nonce, strings.ToUpper(method), requestURI, strings.TrimSpace(contentType), contentLength, bodySHA256)
}

func (manager *Manager) VerifyAndConsumeSignature(ctx context.Context, keyID, secret string, timestamp int64, nonce, method, requestURI, contentType string, contentLength int64, bodySHA256, signature string) error {
	if strings.TrimSpace(method) == "" || requestURI == "" || contentLength < 0 || len(bodySHA256) != sha256.Size*2 || bodySHA256 != strings.ToLower(bodySHA256) {
		return ErrSignatureInvalid
	}
	if _, err := hex.DecodeString(bodySHA256); err != nil {
		return ErrSignatureInvalid
	}
	return manager.verifyAndConsumeSignature(ctx, keyID, secret, timestamp, nonce, signature, "v2", signaturePayloadV2(timestamp, nonce, method, requestURI, contentType, contentLength, bodySHA256))
}

func (manager *Manager) verifyAndConsumeSignature(ctx context.Context, keyID, secret string, timestamp int64, nonce, signature, version, payload string) error {
	now := manager.now().UTC()
	if timestamp < now.Add(-SignatureWindow).Unix() || timestamp > now.Add(SignatureWindow).Unix() ||
		!signatureNonce.MatchString(nonce) || keyID == "" || secret == "" {
		return ErrSignatureInvalid
	}
	prefix := version + "="
	if len(signature) != len(prefix)+sha256.Size*2 || !strings.HasPrefix(signature, prefix) {
		return ErrSignatureInvalid
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, prefix))
	if err != nil || len(provided) != sha256.Size {
		return ErrSignatureInvalid
	}
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(payload))
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
