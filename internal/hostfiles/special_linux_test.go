//go:build linux

package hostfiles_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"scriptboard/internal/hostfiles"
)

func TestManagerListsButRefusesToMoveOrDeleteSpecialFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fifo := filepath.Join(root, "events.pipe")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "special-file-test", Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := manager.List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != hostfiles.Restricted {
		t.Fatalf("special file listing = %#v", entries)
	}
	if _, err := manager.MoveToTrash(fifo, "special-delete"); err == nil {
		t.Fatal("special file was moved to trash")
	}
	if err := manager.Move(fifo, filepath.Join(root, "moved.pipe")); err == nil {
		t.Fatal("special file was moved")
	}
	if _, err := os.Lstat(fifo); err != nil {
		t.Fatalf("special file changed after refused mutations: %v", err)
	}
}
