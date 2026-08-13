package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scriptboard/internal/buildinfo"
)

func TestVersionJSONDoesNotExtractOrInstall(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"--version-json"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var info buildinfo.Info
	if err := json.Unmarshal(output.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != buildinfo.Current().Version {
		t.Fatalf("installer version=%q, want %q", info.Version, buildinfo.Current().Version)
	}
}

func TestDevelopmentInstallerCannotInstallManagedServices(t *testing.T) {
	err := run(nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "formal ScriptBoard release") {
		t.Fatalf("development installer error=%v", err)
	}
}

func TestExtractRequiresAbsoluteNonRootDirectory(t *testing.T) {
	for _, arguments := range [][]string{{"--extract-to"}, {"--extract-to", "relative"}, {"--extract-to", string(filepath.Separator)}} {
		err := run(arguments, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "absolute non-root") {
			t.Fatalf("extract arguments %v error=%v", arguments, err)
		}
	}
}

func TestPreparePayloadExecutablesRestoresExecutableModes(t *testing.T) {
	root := t.TempDir()
	names := []string{"scriptboard", "scriptboard-broker", "scriptboard-ai-host", "scriptboard-runner", "scriptboard-updater"}
	if runtime.GOOS == "windows" {
		names = []string{"scriptboard.exe", "scriptboard-broker.exe", "scriptboard-ai-host.exe", "scriptboard-runner.exe", "scriptboard-updater.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe"}
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := preparePayloadExecutables(root); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, name := range names {
			info, err := os.Stat(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("%s mode=%v, want 0700", name, info.Mode().Perm())
			}
		}
	}
}
