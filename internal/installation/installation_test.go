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
		"scriptboard": []byte("service"), "scriptboard-broker": []byte("broker"), "scriptboard-runner": []byte("runner"), "scriptboard-updater": []byte("updater"),
		buildinfo.ReleaseInfoFilename: raw,
	}
	if runtime.GOOS == "windows" {
		files = map[string][]byte{
			"scriptboard.exe": []byte("service"), "scriptboard-broker.exe": []byte("broker"), "scriptboard-runner.exe": []byte("runner"), "scriptboard-tray.exe": []byte("tray"),
			"scriptboard-tray-launcher.exe": []byte("launcher"), "scriptboard-updater.exe": []byte("updater"),
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
	wantRunner := filepath.Join(install, "current", "scriptboard-runner")
	if runtime.GOOS == "windows" {
		wantRunner = filepath.Join(install, "versions", "1.2.3", "scriptboard-runner.exe")
	}
	assertSameFile(t, "Runner Host executable", ServiceRunnerExecutable(metadata), wantRunner)
	assertSameFile(t, "state root", metadata.StateRoot, state)
}

func TestDetectOptionalOnlyTreatsMissingReferenceAsPortable(t *testing.T) {
	stateRoot := t.TempDir()
	if _, managed, err := DetectOptional(stateRoot); err != nil || managed {
		t.Fatalf("empty portable state managed=%v err=%v", managed, err)
	}
	updates := filepath.Join(stateRoot, updatesDirName)
	if err := os.MkdirAll(updates, 0o700); err != nil {
		t.Fatal(err)
	}
	reference := Reference{Schema: MetadataSchema, InstallID: "missing-install", InstallRoot: filepath.Join(t.TempDir(), "missing")}
	raw, _ := json.Marshal(reference)
	if err := os.WriteFile(filepath.Join(updates, installRefName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DetectOptional(stateRoot); err == nil {
		t.Fatal("existing managed reference with missing metadata was treated as portable")
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
