package update

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/buildinfo"
)

type fakeReleaseSource struct {
	release RemoteRelease
	err     error
}

func (source fakeReleaseSource) Check(_ context.Context, _ string) (RemoteRelease, error) {
	return source.release, source.err
}

func (fakeReleaseSource) Download(context.Context, string, string, Asset) error {
	return nil
}

func validManifest() Manifest {
	return Manifest{
		Schema: ManifestSchema, Product: "scriptboard", Repository: buildinfo.Repository,
		Version: "1.2.3", Tag: "v1.2.3",
		Commit:      "0123456789abcdef0123456789abcdef01234567",
		PublishedAt: "2026-07-29T00:00:00Z", DatabaseSchema: 14,
		UpdaterProtocol: 1, MinimumUpdaterProtocol: 1,
		Assets: []Asset{
			{OS: "windows", Arch: "amd64", Name: "scriptboard-v1.2.3-windows-amd64.zip", SHA256: strings.Repeat("0", 64), Size: 100, UnpackedSize: 200},
			{OS: "windows", Arch: "arm64", Name: "scriptboard-v1.2.3-windows-arm64.zip", SHA256: strings.Repeat("1", 64), Size: 101, UnpackedSize: 201},
			{OS: "linux", Arch: "amd64", Name: "scriptboard-v1.2.3-linux-amd64.tar.gz", SHA256: strings.Repeat("2", 64), Size: 102, UnpackedSize: 202},
			{OS: "linux", Arch: "arm64", Name: "scriptboard-v1.2.3-linux-arm64.tar.gz", SHA256: strings.Repeat("3", 64), Size: 103, UnpackedSize: 203},
		},
	}
}

func TestManifestRejectsUnknownAndDuplicateFields(t *testing.T) {
	raw, _ := json.Marshal(validManifest())
	unknown := append(raw[:len(raw)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodeManifest(unknown); err == nil {
		t.Fatal("manifest with unknown field accepted")
	}
	duplicate := []byte(`{"schema":1,"schema":1}`)
	if _, err := DecodeManifest(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate field error = %v", err)
	}
}

func TestManifestSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(validManifest())
	signature, err := SignManifest(raw, "test-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifestSignature(raw, signature, "test-key", publicKey); err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 1
	if err := VerifyManifestSignature(raw, signature, "test-key", publicKey); err == nil {
		t.Fatal("modified manifest signature accepted")
	}
	_ = base64.StdEncoding
}

func TestTrustedManifestAcceptsNextRotationKey(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(validManifest())
	signature, err := SignManifest(raw, "next-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	originalID, originalKey := buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64
	originalNextID, originalNextKey := buildinfo.UpdateNextKeyID, buildinfo.UpdateNextKeyBase64
	buildinfo.UpdatePublicKeyID = "current-key"
	buildinfo.UpdatePublicKeyBase64 = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	buildinfo.UpdateNextKeyID = "next-key"
	buildinfo.UpdateNextKeyBase64 = base64.StdEncoding.EncodeToString(publicKey)
	t.Cleanup(func() {
		buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64 = originalID, originalKey
		buildinfo.UpdateNextKeyID, buildinfo.UpdateNextKeyBase64 = originalNextID, originalNextKey
	})
	if _, err := VerifyTrustedManifest(raw, signature); err != nil {
		t.Fatalf("verify manifest with next rotation key: %v", err)
	}
}

func TestManagerChecksAndPersistsSignedRelease(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	originalID, originalKey := buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64
	buildinfo.UpdatePublicKeyID = "test-key"
	buildinfo.UpdatePublicKeyBase64 = base64.StdEncoding.EncodeToString(publicKey)
	t.Cleanup(func() {
		buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64 = originalID, originalKey
	})
	manifest := validManifest()
	raw, _ := json.Marshal(manifest)
	signature, _ := SignManifest(raw, "test-key", privateKey)
	current := buildinfo.Info{
		Version: "1.0.0", Tag: "v1.0.0",
		Commit:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BuiltAt: "2026-07-28T00:00:00Z", ReleaseBuild: true,
		DatabaseSchemaVersion:  buildinfo.DatabaseSchemaVersion,
		UpdaterProtocolVersion: buildinfo.UpdaterProtocolVersion, Repository: buildinfo.Repository,
	}
	manager := NewManager(ManagerConfig{
		StateRoot: t.TempDir(), Build: &current, CheckEnabled: true,
		Now: func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) },
		Source: fakeReleaseSource{release: RemoteRelease{
			ETag: "test", ReleaseURL: "https://github.com/backinfile/ScriptBoard/releases/tag/v1.2.3",
			ManifestRaw: raw, SignatureRaw: signature, Manifest: manifest,
			AssetURLs: releaseAssetURLs(manifest),
		}},
	})
	snapshot, err := manager.Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.UpdateAvailable || snapshot.Latest == nil || snapshot.Latest.Version != "1.2.3" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	cache, err := loadCache(manager.stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	cache.ReleaseURL = "javascript:alert(1)"
	if err := saveCache(manager.stateRoot, cache); err == nil {
		t.Fatal("cache with an untrusted release URL was accepted")
	}
	if _, err := manager.Check(context.Background(), true); err == nil || !strings.Contains(err.Error(), "before checking again") {
		t.Fatalf("second forced check error = %v", err)
	}
}

func releaseAssetURLs(manifest Manifest) map[string]string {
	result := make(map[string]string, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		result[asset.Name] = "https://github.com/backinfile/ScriptBoard/releases/download/" + manifest.Tag + "/" + asset.Name
	}
	return result
}

func TestManagerPersistsFirstCheckFailure(t *testing.T) {
	stateRoot := t.TempDir()
	current := buildinfo.Info{
		Version: "1.0.0", Tag: "v1.0.0", Commit: strings.Repeat("a", 40),
		BuiltAt: "2026-07-28T00:00:00Z", ReleaseBuild: true,
		DatabaseSchemaVersion: buildinfo.DatabaseSchemaVersion, UpdaterProtocolVersion: 1, Repository: buildinfo.Repository,
	}
	checkedAt := time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC)
	manager := NewManager(ManagerConfig{
		StateRoot: stateRoot, Build: &current, Source: fakeReleaseSource{err: errors.New("offline")},
		Now: func() time.Time { return checkedAt },
	})
	if _, err := manager.Check(context.Background(), true); err == nil {
		t.Fatal("offline check succeeded")
	}
	restarted := NewManager(ManagerConfig{StateRoot: stateRoot, Build: &current})
	snapshot := restarted.Snapshot()
	if snapshot.LastError != "offline" || !snapshot.CheckedAt.Equal(checkedAt) {
		t.Fatalf("persisted check snapshot = %#v", snapshot)
	}
}

func TestCompareVersionsDoesNotOverflow(t *testing.T) {
	if compareVersions("184467440737095516160.0.0", "9.9.9") <= 0 {
		t.Fatal("large stable version component was not compared numerically")
	}
}

func TestExtractZIPRejectsTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, _ := writer.Create("../outside")
	_, _ = entry.Write([]byte("bad"))
	_ = writer.Close()
	_ = file.Close()
	if err := ExtractArchive(archive, filepath.Join(t.TempDir(), "out"), 3); err == nil {
		t.Fatal("path traversal archive accepted")
	}
}

func TestExtractZIPChecksDeclaredSize(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.zip")
	file, _ := os.Create(archive)
	writer := zip.NewWriter(file)
	entry, _ := writer.Create("scriptboard.exe")
	_, _ = entry.Write([]byte("binary"))
	_ = writer.Close()
	_ = file.Close()
	destination := filepath.Join(t.TempDir(), "out")
	if err := ExtractArchive(archive, destination, 6); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(destination, "scriptboard.exe")); err != nil || string(content) != "binary" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestMeasureArchiveCountsDirectoryEntries(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "many-directories.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for index := 0; index <= MaxArchiveFileCount; index++ {
		header := &zip.FileHeader{Name: fmt.Sprintf("directory-%03d/", index)}
		header.SetMode(os.ModeDir | 0o755)
		if _, err := writer.CreateHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	_ = writer.Close()
	_ = file.Close()
	if _, _, err := MeasureArchive(archive); err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("directory-heavy archive error = %v", err)
	}
}

func TestAtomicJSONCanReplaceExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeAtomicJSON(path, map[string]int{"value": 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicJSON(path, map[string]int{"value": 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	var state map[string]int
	if err := readStrictJSON(path, &state, 1024); err != nil {
		t.Fatal(err)
	}
	if state["value"] != 2 {
		t.Fatalf("state = %#v", state)
	}
}

func TestPendingValidationAppliesToUnexpectedBinary(t *testing.T) {
	stateRoot := t.TempDir()
	id, _ := NewOperationID()
	nonce, _ := NewOperationID()
	root, _ := OperationDirectory(stateRoot, id)
	operation := Operation{
		Schema: OperationSchema, ID: id, Nonce: nonce, Phase: PhaseValidating,
		PreviousVersion: "1.0.0", TargetVersion: "1.2.3",
		PreviousCommit: strings.Repeat("a", 40), TargetCommit: validManifest().Commit,
		InstallRoot: filepath.Join(stateRoot, "install"), StateRoot: stateRoot,
		ConfigPath: filepath.Join(stateRoot, "config.yaml"), DatabasePath: filepath.Join(stateRoot, "app.db"),
		ArchivePath: filepath.Join(root, "release.zip"), ExtractedPath: filepath.Join(root, "extracted"),
		SnapshotPath: filepath.Join(root, "database-before-update.db"), Manifest: validManifest(),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := SaveOperation(operation); err != nil {
		t.Fatal(err)
	}
	unexpected := buildinfo.Info{Version: "9.9.9", Commit: strings.Repeat("b", 40)}
	gotID, validating := PendingValidation(stateRoot, unexpected)
	if !validating || gotID != id {
		t.Fatalf("pending validation id=%q validating=%v", gotID, validating)
	}
}

func TestOperationLockRejectsConcurrentHelper(t *testing.T) {
	stateRoot := t.TempDir()
	first, err := acquireOperationLock(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireOperationLock(stateRoot); err == nil {
		_ = second.Close()
		t.Fatal("concurrent update operation lock succeeded")
	}
}
