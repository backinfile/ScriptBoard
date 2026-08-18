//go:build !windows

package secretstore

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

func TestOpenRejectsCredentialMasterWithUnsafeUnixPermissions(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	store, err := New(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.KeyPath(), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(stateRoot); err == nil {
		t.Fatal("group-readable credential master was accepted")
	}
}

func TestOpenRejectsCredentialMasterSymlink(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	store, err := New(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	realPath := store.KeyPath() + ".real"
	if err := os.Rename(store.KeyPath(), realPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, store.KeyPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(stateRoot); err == nil {
		t.Fatal("credential master symlink was accepted")
	}
}

func TestOpenForIdentityAcceptsKeyOwnedByThatIdentity(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	created, err := New(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenForIdentity(stateRoot, current.Username)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := created.Seal("shared-service-test", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opened.Unseal("shared-service-test", sealed)
	if err != nil || string(plain) != "secret" {
		t.Fatalf("unseal = %q, err = %v", plain, err)
	}
}

func TestCredentialMasterRejectsDifferentExpectedOwner(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	store, err := New(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	uid, err := strconv.Atoi(current.Uid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateKeyPathOwner(store.KeyPath(), uid+1); err == nil {
		t.Fatal("credential master was accepted for a different expected owner")
	}
}
