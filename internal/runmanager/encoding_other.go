//go:build !windows

package runmanager

import (
	"strings"
	"unicode/utf8"
)

func decodeOutput(raw []byte) (string, bool) {
	if utf8.Valid(raw) {
		return string(raw), false
	}
	return strings.ToValidUTF8(string(raw), "�"), true
}
