package mfa

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scriptboard/internal/secretstore"
)

func TestEnrollmentVerificationReplayAndRecoveryCodes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	vault, err := secretstore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	store, err := New(Options{StateRoot: root, SecretStore: vault, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.Begin("user-1", "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.Secret == "" || enrollment.URI == "" {
		t.Fatalf("incomplete enrollment: %#v", enrollment)
	}
	code, err := TOTPCode(enrollment.Secret, now)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := store.Confirm("user-1", code)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery) != recoveryCodeCount {
		t.Fatalf("recovery count=%d", len(recovery))
	}
	status, err := store.Status("user-1")
	if err != nil || !status.Enabled || status.RecoveryCodes != recoveryCodeCount {
		t.Fatalf("status=%#v err=%v", status, err)
	}

	now = now.Add(30 * time.Second)
	nextCode, _ := TOTPCode(enrollment.Secret, now)
	if ok, err := store.Verify("user-1", nextCode); err != nil || !ok {
		t.Fatalf("verify next TOTP ok=%v err=%v", ok, err)
	}
	if ok, err := store.Verify("user-1", nextCode); err != nil || ok {
		t.Fatalf("replayed TOTP ok=%v err=%v", ok, err)
	}
	if ok, err := store.Verify("user-1", recovery[0]); err != nil || !ok {
		t.Fatalf("verify recovery ok=%v err=%v", ok, err)
	}
	if ok, err := store.Verify("user-1", recovery[0]); err != nil || ok {
		t.Fatalf("replayed recovery ok=%v err=%v", ok, err)
	}
	status, _ = store.Status("user-1")
	if status.RecoveryCodes != recoveryCodeCount-1 {
		t.Fatalf("remaining recovery=%d", status.RecoveryCodes)
	}
}

func TestEnrollmentCannotReplaceEnabledFactorWithoutReset(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	vault, _ := secretstore.New(root)
	now := time.Unix(1_800_000_000, 0).UTC()
	store, _ := New(Options{StateRoot: root, SecretStore: vault, Now: func() time.Time { return now }})
	enrollment, _ := store.Begin("user-1", "admin")
	code, _ := TOTPCode(enrollment.Secret, now)
	if _, err := store.Confirm("user-1", code); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Begin("user-1", "admin"); !errors.Is(err, ErrAlreadyEnabled) {
		t.Fatalf("begin enabled factor err=%v", err)
	}
	if err := store.Reset("user-1"); err != nil {
		t.Fatal(err)
	}
	if status, err := store.Status("user-1"); err != nil || status.Enabled {
		t.Fatalf("status after reset=%#v err=%v", status, err)
	}
}

func TestMFAStateIsSealedAndBoundToExternalHostKey(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	vault, _ := secretstore.New(root)
	store, _ := New(Options{StateRoot: root, SecretStore: vault})
	enrollment, err := store.Begin("user-1", "admin")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(enrollment.Secret)) {
		t.Fatal("sealed MFA state contains the TOTP secret")
	}
	otherRoot := filepath.Join(t.TempDir(), "state")
	otherVault, _ := secretstore.New(otherRoot)
	other := &Store{path: store.path, vault: otherVault, now: time.Now}
	if _, err := other.Status("user-1"); err == nil {
		t.Fatal("MFA state opened with another State Root host key")
	}
}
