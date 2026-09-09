//go:build windows

package config_test

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"scriptboard/internal/config"
	"testing"
)

func TestMemorySettingsPreservesWindowsConfigurationACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("run_memory_limit: 512MiB\n"), 0600); err != nil {
		t.Fatal(err)
	}
	descriptor := func() string {
		t.Helper()
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		return sd.String()
	}
	before := descriptor()
	snapshot, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Memory
	next.PerRun = "1GiB"
	if err := config.SaveMemorySettings(path, snapshot.Revision, next); err != nil {
		t.Fatal(err)
	}
	if after := descriptor(); after != before {
		t.Fatalf("security descriptor changed: %s -> %s", before, after)
	}
}
