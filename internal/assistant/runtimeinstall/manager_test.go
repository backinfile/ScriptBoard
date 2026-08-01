package runtimeinstall

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManagerInstallsActivatesAndRollsBackSignedRuntime(t *testing.T) {
	stateRoot := t.TempDir()
	manager := newFixtureManager(t, stateRoot)
	installFixtureRuntime(t, manager, "0.82.0")
	installFixtureRuntime(t, manager, "0.83.0")

	snapshot := manager.Snapshot()
	if snapshot.ActiveVersion != "0.83.0" || snapshot.RollbackVersion != "0.82.0" {
		t.Fatalf("snapshot after update = %#v", snapshot)
	}
	if err := manager.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	snapshot = manager.Snapshot()
	if snapshot.ActiveVersion != "0.82.0" || snapshot.RollbackVersion != "0.83.0" {
		t.Fatalf("snapshot after rollback = %#v", snapshot)
	}
}

func TestManagerRejectsChangedArchiveWithoutPublishingVersion(t *testing.T) {
	stateRoot := t.TempDir()
	manager := newFixtureManager(t, stateRoot)
	raw, signature, archive := fixturePackage(t, "0.83.0")
	archive[len(archive)-1] ^= 0xff
	if err := manager.InstallOffline(context.Background(), raw, signature, bytes.NewReader(archive)); err == nil {
		t.Fatal("changed archive was accepted")
	}
	entries, err := os.ReadDir(filepath.Join(stateRoot, "assistant", "runtime", "versions"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("changed archive published %d versions", len(entries))
	}
}

func TestManagerRefusesSwitchWhileGuardIsBusy(t *testing.T) {
	stateRoot := t.TempDir()
	manager := newFixtureManager(t, stateRoot)
	manager.switchGuard = func(context.Context) error { return ErrRuntimeBusy }
	raw, signature, archive := fixturePackage(t, "0.83.0")
	if err := manager.InstallOffline(context.Background(), raw, signature, bytes.NewReader(archive)); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("install error = %v, want ErrRuntimeBusy", err)
	}
}

func newFixtureManager(t *testing.T, stateRoot string) *Manager {
	t.Helper()
	return NewManager(Config{
		StateRoot:     stateRoot,
		Compatibility: Compatibility{ScriptBoardVersion: "1.2.3", ScriptBoardTag: "v1.2.3"},
		Verify: func(raw, _ []byte, compatibility Compatibility) (Manifest, error) {
			return DecodeManifest(raw, compatibility)
		},
		HealthCheck: func(context.Context, Candidate) error { return nil },
		DiskCheck:   func(string, uint64) error { return nil },
	})
}

func installFixtureRuntime(t *testing.T, manager *Manager, version string) {
	t.Helper()
	raw, signature, archive := fixturePackage(t, version)
	if err := manager.InstallOffline(context.Background(), raw, signature, bytes.NewReader(archive)); err != nil {
		t.Fatalf("install %s: %v", version, err)
	}
}

func fixturePackage(t *testing.T, version string) ([]byte, []byte, []byte) {
	t.Helper()
	metadata := RuntimeMetadata{
		Schema: 1, Product: Product, PiVersion: version, RPCContract: 1, BrokerContract: 1,
		Executable: runtimeExecutableName(), Extension: "scriptboard-extension.ts",
		Upstream:       "https://github.com/earendil-works/pi/releases/tag/v" + version,
		UpstreamCommit: "845d6ff1f6643aba440341cce877ce1c43ebbc39",
	}
	metadataRaw, _ := json.Marshal(metadata)
	files := map[string][]byte{
		runtimeExecutableName():    []byte("fixture pi " + version),
		"scriptboard-extension.ts": []byte("export default function () {}\n"),
		"LICENSE":                  []byte("fixture license\n"),
		"runtime.json":             append(metadataRaw, '\n'),
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	var unpacked int64
	for name, body := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		if name == runtimeExecutableName() {
			header.SetMode(0o700)
		}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
		unpacked += int64(len(body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive.Bytes())
	manifest := validManifestForTest()
	manifest.PiVersion = version
	for index := range manifest.Assets {
		asset := &manifest.Assets[index]
		extension := "tar.gz"
		if asset.OS == "windows" {
			extension = "zip"
		}
		asset.Name = "scriptboard-pi-runtime-" + version + "-" + asset.OS + "-" + asset.Arch + "." + extension
		if asset.OS == runtime.GOOS && asset.Arch == runtime.GOARCH {
			asset.SHA256 = hex.EncodeToString(digest[:])
			asset.Size = int64(archive.Len())
			asset.UnpackedSize = unpacked
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw, []byte("fixture-signature"), archive.Bytes()
}
