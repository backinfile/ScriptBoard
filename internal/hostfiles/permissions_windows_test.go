//go:build windows

package hostfiles

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPermissionsReadOwnerAndUpdateExplicitRule(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.ps1")
	if err := os.WriteFile(path, []byte("Write-Output ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(Options{ProtectedPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := manager.Permissions(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Platform != "windows" || before.Owner.ID == "" {
		t.Fatalf("permissions = %+v", before)
	}
	mask := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE)
	after, err := manager.SetPermissions(path, PermissionChange{Owner: before.Owner.ID, Principal: before.Owner.ID, AccessMask: &mask})
	if err != nil {
		t.Fatal(err)
	}
	if after.Owner.ID != before.Owner.ID {
		t.Fatalf("owner changed: before=%+v after=%+v", before.Owner, after.Owner)
	}
	found := false
	for _, rule := range after.Rules {
		if rule.Principal.ID == before.Owner.ID && !rule.Inherited && rule.Kind == "allow" && rule.Mask&mask == mask {
			found = true
		}
	}
	if !found {
		t.Fatalf("updated explicit rule missing: %+v", after.Rules)
	}
}

func TestWindowsPermissionChangeRejectsPOSIXMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(Options{ProtectedPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	mode := uint32(0o644)
	if _, err := manager.SetPermissions(path, PermissionChange{Mode: &mode}); err == nil {
		t.Fatal("Windows accepted a POSIX mode")
	}
}

func TestWindowsPermissionChangePreservesFileOnlyChildScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(Options{ProtectedPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := manager.Permissions(path)
	if err != nil {
		t.Fatal(err)
	}
	mask := uint32(windows.FILE_GENERIC_READ)
	after, err := manager.SetPermissions(path, PermissionChange{
		Principal: before.Owner.ID, AccessMask: &mask,
		ApplyRuleToChildren: true, RuleAppliesTo: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range after.Rules {
		if rule.Principal.ID == before.Owner.ID && !rule.Inherited && rule.Kind == "allow" && rule.AppliesTo == "files" {
			return
		}
	}
	t.Fatalf("file-only explicit rule missing: %+v", after.Rules)
}
