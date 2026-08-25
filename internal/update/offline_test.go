package update

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"scriptboard/internal/buildinfo"
)

func TestVerifyOfflinePackageChecksSignatureArchiveAndReleaseMetadata(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	originalID, originalKey := buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64
	buildinfo.UpdatePublicKeyID = "offline-test-key"
	buildinfo.UpdatePublicKeyBase64 = base64.StdEncoding.EncodeToString(publicKey)
	t.Cleanup(func() {
		buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64 = originalID, originalKey
	})

	manifest := validManifest()
	assetName := offlineAssetName(manifest.Version)
	archivePath, unpackedSize := writeOfflineReleaseArchive(t, assetName, targetBuild(Operation{Manifest: manifest}))
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archiveHash := sha256.Sum256(archiveBytes)
	verifiedAsset := Asset{
		OS: runtime.GOOS, Arch: runtime.GOARCH, Name: assetName,
		SHA256: hex.EncodeToString(archiveHash[:]), Size: int64(len(archiveBytes)), UnpackedSize: unpackedSize,
	}
	for index := range manifest.Assets {
		if manifest.Assets[index].OS == runtime.GOOS && manifest.Assets[index].Arch == runtime.GOARCH {
			manifest.Assets[index] = verifiedAsset
		}
	}
	manifestRaw, _ := json.Marshal(manifest)
	signatureRaw, err := SignManifest(manifestRaw, "offline-test-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), ManifestFilename)
	signaturePath := filepath.Join(filepath.Dir(manifestPath), SignatureFilename)
	if err := os.WriteFile(manifestPath, manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signaturePath, signatureRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	verified, err := VerifyOfflinePackage(archivePath, manifestPath, signaturePath)
	if err != nil {
		t.Fatalf("verify offline package: %v", err)
	}
	if verified.Version != manifest.Version || verified.KeyID != "offline-test-key" || verified.ArchiveSHA256 != verifiedAsset.SHA256 {
		t.Fatalf("verified package = %#v", verified)
	}

	archiveBytes[len(archiveBytes)-1] ^= 1
	if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyOfflinePackage(archivePath, manifestPath, signaturePath); err == nil {
		t.Fatal("modified offline archive was accepted")
	}
}

func offlineAssetName(version string) string {
	if runtime.GOOS == "windows" {
		return "scriptboard-v" + version + "-windows-" + runtime.GOARCH + "-setup.exe"
	}
	return "scriptboard-v" + version + "-linux-" + runtime.GOARCH + ".run"
}

func writeOfflineReleaseArchive(t *testing.T, name string, release buildinfo.Info) (string, int64) {
	t.Helper()
	releaseRaw, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{buildinfo.ReleaseInfoFilename: releaseRaw}
	if runtime.GOOS == "windows" {
		for _, required := range []string{"scriptboard.exe", "scriptboard-broker.exe", "scriptboard-runner.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe", "scriptboard-updater.exe"} {
			files[required] = []byte("fixture-" + required)
		}
	} else {
		files["scriptboard"] = []byte("fixture-scriptboard")
		files["scriptboard-broker"] = []byte("fixture-broker")
		files["scriptboard-runner"] = []byte("fixture-runner")
		files["scriptboard-updater"] = []byte("fixture-updater")
	}
	var unpacked int64
	for _, content := range files {
		unpacked += int64(len(content))
	}
	path := filepath.Join(t.TempDir(), name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("fixture-native-installer")); err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path, unpacked
}
