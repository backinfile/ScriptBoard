//go:build !windows

package installation

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSetCurrentMetadataWriteFailureCanBeRepairedDeterministically(t *testing.T) {
	root := t.TempDir()
	installRoot := filepath.Join(root, "install")
	stateRoot := filepath.Join(root, "state")
	for _, directory := range []string{
		filepath.Join(installRoot, versionsDirName, "1.0.0"),
		filepath.Join(installRoot, versionsDirName, "1.1.0"),
		stateRoot,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	metadata := Metadata{
		Schema: MetadataSchema, InstallID: "fault-test", InstallRoot: installRoot, StateRoot: stateRoot,
		ServiceName: "ScriptBoard", ConfigPath: filepath.Join(root, "config.yaml"), OS: runtime.GOOS, Arch: runtime.GOARCH,
		ManagedLayout: true, Current: "1.0.0",
	}
	if err := writeJSONAtomic(filepath.Join(installRoot, metadataName), metadata, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := activateVersion(installRoot, VersionRoot(metadata, metadata.Current)); err != nil {
		t.Fatal(err)
	}
	// A directory at the staging filename injects the crash/error boundary after
	// the atomic symlink switch but before metadata replacement.
	blockedTemporary := filepath.Join(installRoot, metadataName+".tmp")
	if err := os.Mkdir(blockedTemporary, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := SetCurrent(metadata, "1.1.0"); err == nil {
		t.Fatal("metadata write failure was not surfaced")
	}
	stored, err := loadMetadata(filepath.Join(installRoot, metadataName))
	if err != nil || stored.Current != "1.0.0" {
		t.Fatalf("stored metadata=%#v err=%v", stored, err)
	}
	target, err := os.Readlink(filepath.Join(installRoot, "current"))
	if err != nil || target != filepath.Join(versionsDirName, "1.1.0") {
		t.Fatalf("injected partial switch target=%q err=%v", target, err)
	}
	if err := os.Remove(blockedTemporary); err != nil {
		t.Fatal(err)
	}
	if _, err := SetCurrent(stored, stored.Current); err != nil {
		t.Fatal(err)
	}
	target, err = os.Readlink(filepath.Join(installRoot, "current"))
	if err != nil || target != filepath.Join(versionsDirName, "1.0.0") {
		t.Fatalf("repaired target=%q err=%v", target, err)
	}
}
