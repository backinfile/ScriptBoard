package ai

import (
	"context"
	"errors"
	"testing"
)

func TestSecretVaultRoundTripAndDelete(t *testing.T) {
	vault, err := OpenSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reference, err := vault.Write(context.Background(), "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	value, err := vault.Read(reference)
	if err != nil || value != "secret-value" {
		t.Fatalf("Read = %q, %v", value, err)
	}
	if err := vault.Delete(reference); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Read(reference); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Read deleted secret: %v", err)
	}
}
