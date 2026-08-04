package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
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

func TestPackRuntimeTarGZDeclaresPortableRuntimePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "payload")
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"pi":               []byte("fixture executable"),
		"runtime.json":     []byte("{}\n"),
		"assets/theme.txt": []byte("fixture asset"),
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	archivePath := filepath.Join(t.TempDir(), "runtime.tar.gz")
	if err := packRuntimeTarGZ(root, archivePath); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	want := map[string]int64{"assets/": 0o700, "assets/theme.txt": 0o600, "pi": 0o700, "runtime.json": 0o600}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		expected, ok := want[header.Name]
		if !ok {
			t.Fatalf("unexpected archive entry %q", header.Name)
		}
		if got := header.Mode & 0o777; got != expected {
			t.Fatalf("archive entry %q mode = %04o, want %04o", header.Name, got, expected)
		}
		delete(want, header.Name)
	}
	if len(want) != 0 {
		t.Fatalf("archive is missing entries: %v", want)
	}
}
