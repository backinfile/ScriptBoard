// Package recordnote defines the shared contract for private operator notes.
package recordnote

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxRunes = 500

// Normalize trims an optional note and enforces the common storage limit.
func Normalize(value string) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > MaxRunes {
		return "", errors.New("note must be 500 characters or fewer")
	}
	return value, nil
}
