package pathsecurity

import (
	"strings"
	"unicode/utf8"
)

// UnsafeWindowsComponent reports names that Win32 can reinterpret as a
// device, alternate data stream, or a different normalized filename.
// Applying the rule on every platform keeps names created through the host-file UI portable.
func UnsafeWindowsComponent(component string) bool {
	if component == "" || !utf8.ValidString(component) ||
		strings.ContainsAny(component, `<>:"/\|?*`) ||
		strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
		return true
	}
	for _, character := range component {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	base := component
	if extension := strings.IndexByte(base, '.'); extension >= 0 {
		base = base[:extension]
	}
	base = strings.TrimRight(base, " .")
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
		"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³":
		return true
	default:
		return false
	}
}
