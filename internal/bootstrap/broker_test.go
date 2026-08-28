package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareBrokerHostFilesStagingRootRemovesOnlyRetiredUploads(t *testing.T) {
	stateRoot := t.TempDir()
	retired := filepath.Join(stateRoot, "inbox", "uploads", "legacy-payload")
	exchangeSentinel := filepath.Join(stateRoot, "inbox", "host-files-broker", "active-exchange")
	if err := os.MkdirAll(filepath.Dir(retired), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retired, []byte("retired"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(exchangeSentinel), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exchangeSentinel, []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := prepareBrokerHostFilesStagingRoot(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Dir(exchangeSentinel) {
		t.Fatalf("staging root=%q, want %q", root, filepath.Dir(exchangeSentinel))
	}
	if _, err := os.Stat(retired); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired upload remains: %v", err)
	}
	if content, err := os.ReadFile(exchangeSentinel); err != nil || string(content) != "active" {
		t.Fatalf("Broker exchange changed content=%q error=%v", content, err)
	}
}
