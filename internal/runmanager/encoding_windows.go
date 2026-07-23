//go:build windows

package runmanager

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

func decodeOutput(raw []byte) (string, bool) {
	if utf8.Valid(raw) {
		return string(raw), false
	}
	if decoded, ok := decodeWindowsCodePage(raw, windows.GetACP()); ok {
		return decoded, false
	}
	return strings.ToValidUTF8(string(raw), "�"), true
}

func decodeWindowsCodePage(raw []byte, codePage uint32) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	required, err := windows.MultiByteToWideChar(codePage, 0, &raw[0], int32(len(raw)), nil, 0)
	if err != nil || required == 0 {
		return "", false
	}
	wide := make([]uint16, required)
	written, err := windows.MultiByteToWideChar(codePage, 0, &raw[0], int32(len(raw)), &wide[0], required)
	if err != nil || written != required {
		return "", false
	}
	return string(utf16.Decode(wide)), true
}
