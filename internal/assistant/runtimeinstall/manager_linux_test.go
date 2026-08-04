//go:build linux

package runtimeinstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagerInstallsTrustedLinuxRuntimeWhoseArchiveOmitsExecuteBit(t *testing.T) {
	stateRoot := t.TempDir()
	manager := NewManager(Config{
		StateRoot:     stateRoot,
		Compatibility: Compatibility{ScriptBoardVersion: "1.2.3", ScriptBoardTag: "v1.2.3"},
		Verify: func(raw, _ []byte, compatibility Compatibility) (Manifest, error) {
			return DecodeManifest(raw, compatibility)
		},
		HealthCheck: func(ctx context.Context, candidate Candidate) error {
			info, err := os.Stat(candidate.Executable)
			if err != nil {
				return err
			}
			if got := info.Mode().Perm(); got != 0o700 {
				return fmt.Errorf("Pi executable mode = %04o, want 0700", got)
			}
			output, err := exec.CommandContext(ctx, candidate.Executable, "--version").Output()
			if err != nil {
				return err
			}
			if got := strings.TrimSpace(string(output)); got != candidate.Version {
				return fmt.Errorf("Pi version = %q, want %q", got, candidate.Version)
			}
			return nil
		},
		DiskCheck: func(string, uint64) error { return nil },
		GOOS:      "linux",
		GOARCH:    runtime.GOARCH,
	})
	manifestRaw, signatureRaw, archiveRaw := linuxRuntimePackageWithoutExecuteBit(t)

	if err := manager.InstallOffline(context.Background(), manifestRaw, signatureRaw, bytes.NewReader(archiveRaw)); err != nil {
		t.Fatalf("install trusted Linux runtime: %v", err)
	}

	executable := filepath.Join(stateRoot, "assistant", "runtime", "versions", "0.83.0", "pi")
	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("published Pi executable mode = %04o, want 0700", got)
	}
}

func linuxRuntimePackageWithoutExecuteBit(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	metadata := RuntimeMetadata{
		Schema: 1, Product: Product, PiVersion: "0.83.0", RPCContract: 1, BrokerContract: 1,
		Executable: "pi", Extension: "scriptboard-extension.ts",
		Upstream:       "https://github.com/earendil-works/pi/releases/tag/v0.83.0",
		UpstreamCommit: "845d6ff1f6643aba440341cce877ce1c43ebbc39",
	}
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	files := []struct {
		name string
		body []byte
	}{
		{name: "pi", body: []byte("#!/bin/sh\nprintf '0.83.0\\n'\n")},
		{name: "scriptboard-extension.ts", body: []byte("export default function () {}\n")},
		{name: "LICENSE", body: []byte("fixture license\n")},
		{name: "runtime.json", body: append(metadataRaw, '\n')},
	}
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	writer := tar.NewWriter(compressed)
	var unpacked int64
	for _, file := range files {
		header := &tar.Header{Name: file.name, Mode: 0o666, Size: int64(len(file.body)), Typeflag: tar.TypeReg}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(file.body); err != nil {
			t.Fatal(err)
		}
		unpacked += int64(len(file.body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(archive.Bytes())
	manifest := validManifestForTest()
	asset, ok := manifest.AssetFor("linux", runtime.GOARCH)
	if !ok {
		t.Fatalf("test manifest has no linux/%s asset", runtime.GOARCH)
	}
	for index := range manifest.Assets {
		if manifest.Assets[index].OS == asset.OS && manifest.Assets[index].Arch == asset.Arch {
			manifest.Assets[index].SHA256 = hex.EncodeToString(digest[:])
			manifest.Assets[index].Size = int64(archive.Len())
			manifest.Assets[index].UnpackedSize = unpacked
		}
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifestRaw, []byte("fixture-signature"), archive.Bytes()
}
