//go:build windows

package hostfiles

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// canonicalComparisonPath expands Win32 8.3 aliases before applying a
// security boundary comparison. Display paths and persisted comparison keys
// retain their caller-provided spelling.
func canonicalComparisonPath(path string) string {
	clean := filepath.Clean(path)
	if !strings.ContainsRune(clean, '~') {
		return clean
	}
	current := clean
	unresolved := make([]string, 0, 2)
	for {
		if expanded, ok := windowsLongPath(current); ok {
			for index := len(unresolved) - 1; index >= 0; index-- {
				expanded = filepath.Join(expanded, unresolved[index])
			}
			return filepath.Clean(expanded)
		}

		// GetLongPathName cannot expand a destination that has not been created
		// yet. Walk back to the nearest existing ancestor so protection checks
		// and Rebase still compare that destination against the ancestor's long
		// spelling, then append the unresolved suffix without touching the disk.
		parent := filepath.Dir(current)
		if parent == current {
			return clean
		}
		unresolved = append(unresolved, filepath.Base(current))
		current = parent
	}
}

func windowsLongPath(path string) (string, bool) {
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
