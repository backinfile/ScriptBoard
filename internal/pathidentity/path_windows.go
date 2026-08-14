//go:build windows

package pathidentity

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func platformCanonical(path string) string {
	if !strings.ContainsRune(path, '~') {
		return path
	}
	clean := filepath.Clean(path)
	current := clean
	unresolved := make([]string, 0, 2)
	for {
		if expanded, ok := longPath(current); ok {
			for index := len(unresolved) - 1; index >= 0; index-- {
				expanded = filepath.Join(expanded, unresolved[index])
			}
			return filepath.Clean(expanded)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return clean
		}
		unresolved = append(unresolved, filepath.Base(current))
		current = parent
	}
}

func longPath(path string) (string, bool) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	buffer := make([]uint16, windows.MAX_PATH)
	length, err := windows.GetLongPathName(pointer, &buffer[0], uint32(len(buffer)))
	if length >= uint32(len(buffer)) {
		buffer = make([]uint16, length+1)
		length, err = windows.GetLongPathName(pointer, &buffer[0], uint32(len(buffer)))
	}
	if err != nil || length == 0 || length >= uint32(len(buffer)) {
		return "", false
	}
	return windows.UTF16ToString(buffer[:length]), true
}
