//go:build windows

package hostfiles

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func filesystemRoot(path string) (string, error) {
	pointer, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(pointer, &buffer[0], uint32(len(buffer))); err != nil {
		return "", err
	}
	return filepath.Clean(windows.UTF16ToString(buffer)), nil
}

func entryHidden(path string, _ os.FileInfo) bool {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return strings.HasPrefix(filepath.Base(path), ".")
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return strings.HasPrefix(filepath.Base(path), ".")
	}
	return attributes&(windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_ATTRIBUTE_SYSTEM) != 0 || strings.HasPrefix(filepath.Base(path), ".")
}

func restrictedEntry(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(pointer)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
