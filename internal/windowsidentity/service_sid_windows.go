//go:build windows

package windowsidentity

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// ResolveSID derives service SIDs locally because account lookup can stall
// during SCM startup before the virtual NT SERVICE account is resolvable.
func ResolveSID(identity string) (*windows.SID, error) {
	trimmed := strings.TrimSpace(identity)
	const servicePrefix = `NT SERVICE\`
	if !strings.HasPrefix(strings.ToUpper(trimmed), servicePrefix) {
		sid, _, _, err := windows.LookupSID("", trimmed)
		return sid, err
	}
	name := strings.TrimSpace(trimmed[len(servicePrefix):])
	if name == "" {
		return nil, errors.New("Windows service identity has no service name")
	}
	units := utf16.Encode([]rune(strings.ToUpper(name)))
	encoded := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	digest := sha1.Sum(encoded)
	sidText := "S-1-5-80"
	for offset := 0; offset < len(digest); offset += 4 {
		sidText += fmt.Sprintf("-%d", binary.LittleEndian.Uint32(digest[offset:offset+4]))
	}
	return windows.StringToSid(sidText)
}
