package secretstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRootCopyCannotDecryptSealedSecretWithoutExternalKey(t *testing.T) {
	firstRoot := filepath.Join(t.TempDir(), "state")
	first, err := New(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := first.Seal("registry-credential", []byte("registry-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("registry-secret")) {
		t.Fatal("sealed value contains plaintext")
	}
	plain, err := first.Unseal("registry-credential", sealed)
	if err != nil || string(plain) != "registry-secret" {
		t.Fatalf("unseal=%q err=%v", plain, err)
	}
	if relative, err := filepath.Rel(firstRoot, first.KeyPath()); err != nil || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..") {
		t.Fatalf("master key is not outside State Root: state=%s key=%s relative=%s err=%v", firstRoot, first.KeyPath(), relative, err)
	}

	secondRoot := filepath.Join(t.TempDir(), "state")
	second, err := New(secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Unseal("registry-credential", sealed); err == nil {
		t.Fatal("a copied State Root decrypted without the original external key")
	}
}

func TestPurposeIsAuthenticated(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := store.Seal("mysql-credential", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Unseal("registry-credential", sealed); err == nil {
		t.Fatal("sealed secret was accepted for another purpose")
	}
	if _, err := os.Stat(store.KeyPath()); err != nil {
		t.Fatalf("external key missing: %v", err)
	}
}

func TestOpenDoesNotCreateMissingHostKey(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	keyPath, err := KeyPathForStateRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open missing host key error=%v", err)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only open created key: %v", err)
	}
}
