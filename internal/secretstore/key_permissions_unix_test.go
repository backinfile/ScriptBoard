//go:build !windows

package secretstore

import (
	"os"
	"path/filepath"
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
