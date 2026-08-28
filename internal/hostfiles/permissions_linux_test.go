//go:build linux

package hostfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxPermissionsReadAndSetModeWithoutChangingOwnership(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o640); err != nil {
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
	if before.Platform != "linux" || before.Mode != 0o640 || before.Owner.ID == "" || before.Group.ID == "" {
		t.Fatalf("permissions = %+v", before)
	}
	mode := uint32(0o750)
	after, err := manager.SetPermissions(path, PermissionChange{Mode: &mode})
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode != mode || after.Owner.ID != before.Owner.ID || after.Group.ID != before.Group.ID {
		t.Fatalf("permissions after update = %+v", after)
	}
}

func TestLinuxRecursivePermissionsRejectRestrictedEntriesBeforeMutation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "tree")
	if err := os.Mkdir(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(Options{ProtectedPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	mode := uint32(0o700)
	if _, err := manager.SetPermissions(directory, PermissionChange{Mode: &mode, Recursive: true}); err == nil {
		t.Fatal("recursive permission change accepted a symlink")
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("directory mode changed before validation: %o", info.Mode().Perm())
	}
}
