package installation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"scriptboard/internal/buildinfo"
)

func TestPrepareCreatesVersionedInstallation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "release")
	state := filepath.Join(root, "state")
	install := filepath.Join(root, "install")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	info := buildinfo.Info{
		Version: "1.2.3", Tag: "v1.2.3",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		BuiltAt: "2026-07-29T00:00:00Z", ReleaseBuild: true,
		DatabaseSchemaVersion:  buildinfo.DatabaseSchemaVersion,
		UpdaterProtocolVersion: buildinfo.UpdaterProtocolVersion,
		Repository:             buildinfo.Repository,
	}
	raw, _ := json.Marshal(info)
	files := map[string][]byte{
		"scriptboard": []byte("service"), "scriptboard-broker": []byte("broker"), "scriptboard-ai-host": []byte("ai-host"), "scriptboard-runner": []byte("runner"), "scriptboard-updater": []byte("updater"), "install.sh": []byte("installer"),
		buildinfo.ReleaseInfoFilename: raw,
	}
	if runtime.GOOS == "windows" {
		files = map[string][]byte{
			"scriptboard.exe": []byte("service"), "scriptboard-broker.exe": []byte("broker"), "scriptboard-ai-host.exe": []byte("ai-host"), "scriptboard-runner.exe": []byte("runner"), "scriptboard-tray.exe": []byte("tray"),
			"scriptboard-tray-launcher.exe": []byte("launcher"), "scriptboard-updater.exe": []byte("updater"), "install.cmd": []byte("installer"),
			buildinfo.ReleaseInfoFilename: raw,
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(source, name), content, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := Prepare(PrepareOptions{
		SourceRoot: source, InstallRoot: install, StateRoot: state,
		ConfigPath: filepath.Join(root, "config.yaml"), Build: info,
	})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Current != "1.2.3" || metadata.InstallID == "" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if runtime.GOOS != "windows" {
		target, err := os.Readlink(filepath.Join(install, "current"))
		if err != nil || target != filepath.Join("versions", "1.2.3") {
			t.Fatalf("current target=%q err=%v", target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "updates", installRefName)); err != nil {
		t.Fatal(err)
	}
	wantUpdater := filepath.Join(install, "scriptboard-updater")
	if runtime.GOOS == "windows" {
		wantUpdater = filepath.Join(install, "versions", "1.2.3", "scriptboard-updater.exe")
	}
	assertSameFile(t, "service updater executable", ServiceUpdaterExecutable(metadata), wantUpdater)
	wantService := filepath.Join(install, "current", "scriptboard")
	if runtime.GOOS == "windows" {
		wantService = filepath.Join(install, "versions", "1.2.3", "scriptboard.exe")
	}
	assertSameFile(t, "service entry executable", ServiceEntryExecutable(metadata), wantService)
	wantBroker := filepath.Join(install, "current", "scriptboard-broker")
	if runtime.GOOS == "windows" {
		wantBroker = filepath.Join(install, "versions", "1.2.3", "scriptboard-broker.exe")
	}
	assertSameFile(t, "privileged Broker executable", ServiceBrokerExecutable(metadata), wantBroker)
	wantAIHost := filepath.Join(install, "current", "scriptboard-ai-host")
	if runtime.GOOS == "windows" {
		wantAIHost = filepath.Join(install, "versions", "1.2.3", "scriptboard-ai-host.exe")
	}
	assertSameFile(t, "AI Runtime Host executable", ServiceAIHostExecutable(metadata), wantAIHost)
	wantRunner := filepath.Join(install, "current", "scriptboard-runner")
	if runtime.GOOS == "windows" {
		wantRunner = filepath.Join(install, "versions", "1.2.3", "scriptboard-runner.exe")
	}
	assertSameFile(t, "Runner Host executable", ServiceRunnerExecutable(metadata), wantRunner)
	assertSameFile(t, "state root", metadata.StateRoot, state)
	installer := "install.sh"
	if runtime.GOOS == "windows" {
		installer = "install.cmd"
	}
	if err := os.Remove(filepath.Join(VersionRoot(metadata, metadata.Current), installer)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateVersion(metadata, metadata.Current, info); err == nil {
		t.Fatalf("Installed Release without %s passed validation", installer)
	}
}

func assertSameFile(t *testing.T, label, got, want string) {
	t.Helper()
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("%s %q: %v", label, got, err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("%s expected path %q: %v", label, want, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("%s = %q, want same file as %q", label, got, want)
	}
}
