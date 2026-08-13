package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildManifestUsesSingleFileInstallerAssets(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"scriptboard-v1.2.3-windows-amd64-setup.exe",
		"scriptboard-v1.2.3-windows-arm64-setup.exe",
		"scriptboard-v1.2.3-linux-amd64.run",
		"scriptboard-v1.2.3-linux-arm64.run",
	} {
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("native-launcher")); err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(file)
		entry, err := writer.Create("RELEASE.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("{}")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := buildManifest(
		"1.2.3", "v1.2.3", "0123456789abcdef0123456789abcdef01234567",
		"2026-08-13T00:00:00Z", root,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Assets) != 4 || manifest.MinimumUpdaterProtocol != 2 {
		t.Fatalf("manifest=%#v", manifest)
	}
	for _, asset := range manifest.Assets {
		if asset.OS == "windows" && filepath.Ext(asset.Name) != ".exe" {
			t.Fatalf("Windows asset=%q", asset.Name)
		}
		if asset.OS == "linux" && filepath.Ext(asset.Name) != ".run" {
			t.Fatalf("Linux asset=%q", asset.Name)
		}
	}
}
