package externaltrigger

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestSignedRequestAcceptsOneFreshNonce(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Signed automation", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "signed-log", Label: "Signed log", Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "signed.log", MaxMessageBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := now.Unix()
	nonce := "nonce_1234567890abcdef"
	requestURI := "/trigger?name=signed-log"
	signature := RequestSignature(secret, timestamp, nonce, http.MethodPost, requestURI)
	if err := manager.VerifyAndConsumeSignature(context.Background(), key.ID, secret, timestamp, nonce, http.MethodPost, requestURI, signature); err != nil {
		t.Fatalf("verify fresh signature: %v", err)
	}
	if err := manager.VerifyAndConsumeSignature(context.Background(), key.ID, secret, timestamp, nonce, http.MethodPost, requestURI, signature); !errors.Is(err, ErrSignatureReplay) {
		t.Fatalf("replayed signature error=%v", err)
	}
}

func TestSignedRequestRejectsInvalidOrExpiredProof(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Signed automation", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "signed-log", Label: "Signed log", Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "signed.log", MaxMessageBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, nonce, method, requestURI, signature string
		timestamp                                  int64
	}{
		{name: "expired", timestamp: now.Add(-6 * time.Minute).Unix(), nonce: "nonce_expired_123456", method: http.MethodPost, requestURI: "/trigger?name=signed-log"},
		{name: "future", timestamp: now.Add(6 * time.Minute).Unix(), nonce: "nonce_future_12345678", method: http.MethodPost, requestURI: "/trigger?name=signed-log"},
		{name: "short nonce", timestamp: now.Unix(), nonce: "short", method: http.MethodPost, requestURI: "/trigger?name=signed-log"},
		{name: "bad signature", timestamp: now.Unix(), nonce: "nonce_bad_sig_123456", method: http.MethodPost, requestURI: "/trigger?name=signed-log", signature: "v1=0000000000000000000000000000000000000000000000000000000000000000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			signature := test.signature
			if signature == "" {
				signature = RequestSignature(secret, test.timestamp, test.nonce, test.method, test.requestURI)
			}
			if err := manager.VerifyAndConsumeSignature(context.Background(), key.ID, secret, test.timestamp, test.nonce, test.method, test.requestURI, signature); !errors.Is(err, ErrSignatureInvalid) {
				t.Fatalf("signature error=%v", err)
			}
		})
	}
	nonce := "nonce_wrong_uri_123456"
	signature := RequestSignature(secret, now.Unix(), nonce, http.MethodPost, "/trigger?name=other")
	if err := manager.VerifyAndConsumeSignature(context.Background(), key.ID, secret, now.Unix(), nonce, http.MethodPost, "/trigger?name=signed-log", signature); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("request URI binding error=%v", err)
	}
}

func TestFutureDatedSignatureNonceLivesThroughFullAcceptanceWindow(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	manager, db := testManager(t, now)
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Future signature", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "signed-log", Label: "Signed log", Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "signed.log", MaxMessageBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := now.Add(4 * time.Minute).Unix()
	nonce := "nonce_future_valid_1234"
	requestURI := "/trigger?name=signed-log"
	if err := manager.VerifyAndConsumeSignature(context.Background(), key.ID, secret, timestamp, nonce, http.MethodPost, requestURI, RequestSignature(secret, timestamp, nonce, http.MethodPost, requestURI)); err != nil {
		t.Fatal(err)
	}
	var expiresAt int64
	if err := db.QueryRow(`SELECT expires_at FROM external_trigger_nonces WHERE key_id = ? AND nonce = ?`, key.ID, nonce).Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	if expected := time.Unix(timestamp, 0).Add(SignatureWindow).Unix(); expiresAt != expected {
		t.Fatalf("nonce expires_at=%d expected=%d", expiresAt, expected)
	}
}

func TestConcurrentSignedRequestConsumesNonceOnce(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	key, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Concurrent signature", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "signed-log", Label: "Signed log", Type: ActionLog, Enabled: true,
		Config: LogConfig{File: "signed.log", MaxMessageBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := now.Unix()
	nonce := "nonce_concurrent_12345"
	requestURI := "/trigger?name=signed-log"
	signature := RequestSignature(secret, timestamp, nonce, http.MethodPost, requestURI)
	const attempts = 12
	results := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- manager.VerifyAndConsumeSignature(context.Background(), key.ID, secret, timestamp, nonce, http.MethodPost, requestURI, signature)
		}()
	}
	wait.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent signature successes=%d expected=1", succeeded)
	}
}
