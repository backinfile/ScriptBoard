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
	pointer, err := windows.UTF16PtrFromString(clean)
	if err != nil {
		return clean
	}
	buffer := make([]uint16, windows.MAX_PATH)
	length, err := windows.GetLongPathName(pointer, &buffer[0], uint32(len(buffer)))
	if length >= uint32(len(buffer)) {
		buffer = make([]uint16, length+1)
		length, err = windows.GetLongPathName(pointer, &buffer[0], uint32(len(buffer)))
	}
	if err != nil || length == 0 || length >= uint32(len(buffer)) {
		return clean
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:length]))
}
