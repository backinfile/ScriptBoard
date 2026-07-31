//go:build windows

package hostfiles

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type systemTopology struct{}

func (systemTopology) Roots() ([]Entry, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, fmt.Errorf("list host volumes: %w", err)
	}
	entries := make([]Entry, 0, 8)
	for index := 0; index < 26; index++ {
		if mask&(1<<index) == 0 {
			continue
		}
		name := string(rune('A'+index)) + ":"
		path := name + string(filepath.Separator)
		pointer, pointerErr := windows.UTF16PtrFromString(path)
		if pointerErr != nil {
			continue
		}
		var volumeType string
		switch windows.GetDriveType(pointer) {
		case windows.DRIVE_FIXED:
			volumeType = "local"
		case windows.DRIVE_REMOVABLE:
			volumeType = "removable"
		case windows.DRIVE_REMOTE:
			volumeType = "network"
		case windows.DRIVE_RAMDISK:
			volumeType = "memory"
		default:
			continue
		}
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			continue
		}
		entries = append(entries, Entry{Name: name, Path: path, Kind: Directory, VolumeType: volumeType})
	}
	return entries, nil
}

func (systemTopology) FilesystemRoot(path string) (string, error) {
	return filesystemRoot(path)
}

func (systemTopology) Restricted(string) bool { return false }
