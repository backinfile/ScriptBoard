package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedRuntimeLockIsComplete(t *testing.T) {
	lock, err := readRuntimeLock(filepath.Join("..", "..", "runtime", "pi-runtime-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if lock.Version != "0.83.0" || len(lock.Assets) != 4 {
		t.Fatalf("runtime lock = %#v", lock)
	}
	for _, platform := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		if _, ok := lock.assetFor(platform[0], platform[1]); !ok {
			t.Fatalf("missing lock for %s/%s", platform[0], platform[1])
		}
	}
}

func TestRuntimePayloadRootUsesLinuxWrapper(t *testing.T) {
	extracted := t.TempDir()
	wrapper := filepath.Join(extracted, "pi")
	if err := os.Mkdir(wrapper, 0o700); err != nil {
		t.Fatal(err)
	}

	payload, err := runtimePayloadRoot(extracted, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if payload != wrapper {
		t.Fatalf("payload = %q, want %q", payload, wrapper)
	}
}

func TestRuntimePayloadRootRejectsUnexpectedLinuxLayout(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, extracted string)
	}{
		{name: "missing wrapper"},
		{name: "wrapper is a file", setup: func(t *testing.T, extracted string) {
			if err := os.WriteFile(filepath.Join(extracted, "pi"), []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "extra sibling", setup: func(t *testing.T, extracted string) {
			if err := os.Mkdir(filepath.Join(extracted, "pi"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(extracted, "unexpected"), []byte("unexpected"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			extracted := t.TempDir()
			if test.setup != nil {
				test.setup(t, extracted)
			}
			if _, err := runtimePayloadRoot(extracted, "linux"); err == nil {
				t.Fatal("runtimePayloadRoot accepted an unexpected Linux archive layout")
			}
		})
	}
}

func TestRuntimePayloadRootKeepsWindowsRoot(t *testing.T) {
	extracted := t.TempDir()
	payload, err := runtimePayloadRoot(extracted, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if payload != extracted {
		t.Fatalf("payload = %q, want %q", payload, extracted)
	}
}
