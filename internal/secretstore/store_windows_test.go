//go:build windows

package secretstore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsMasterKeyFileIsDPAPIProtected(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(store.KeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, store.key[:]) {
		t.Fatal("Windows credential master file contains the raw AES key")
	}
}
