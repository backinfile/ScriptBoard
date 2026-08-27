//go:build windows

package statebackup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRenamePrivateStateWaitsForTransientSharingViolation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "app.db")
	destination := filepath.Join(root, "app.db.preserved")
	if err := os.WriteFile(source, []byte("sqlite fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(source)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = windows.CloseHandle(handle)
		close(released)
	}()

	renameErr := renamePrivateState(source, destination)
	<-released
	if renameErr != nil {
		t.Fatalf("rename did not outlive a transient sharing violation: %v", renameErr)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("renamed private state is missing: %v", err)
	}
}
