// Package quickrun owns validation and publication rules for ad-hoc and saved
// script execution independently from HTTP forms.
package quickrun

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxSourceBytes = 1 << 20

type Language struct {
	ID        string
	Label     string
	Extension string
}

func PlatformLanguages(goos string) []Language {
	if goos == "windows" {
		return []Language{
			{ID: "powershell", Label: "PowerShell", Extension: ".ps1"},
			{ID: "batch", Label: "Batch", Extension: ".cmd"},
			{ID: "python", Label: "Python", Extension: ".py"},
		}
	}
	return []Language{
		{ID: "shell", Label: "Shell", Extension: ".sh"},
		{ID: "python", Label: "Python", Extension: ".py"},
		{ID: "powershell", Label: "PowerShell", Extension: ".ps1"},
	}
}

func PlatformLanguage(goos, id string) (Language, error) {
	for _, language := range PlatformLanguages(goos) {
		if language.ID == id {
			return language, nil
		}
	}
	return Language{}, errors.New("script language is not supported on this host")
}

func ValidateSource(source string) error {
	if source == "" {
		return errors.New("source is required")
	}
	if len([]byte(source)) > MaxSourceBytes {
		return fmt.Errorf("source exceeds the %d-byte limit", MaxSourceBytes)
	}
	if !utf8.ValidString(source) || strings.ContainsRune(source, 0) {
		return errors.New("source must be valid UTF-8 without NUL bytes")
	}
	return nil
}

func FileName(name, extension string) string {
	if strings.HasSuffix(strings.ToLower(name), strings.ToLower(extension)) {
		return name
	}
	return name + extension
}

func FileStem(name, extension string) string {
	if strings.HasSuffix(strings.ToLower(name), strings.ToLower(extension)) {
		return name[:len(name)-len(extension)]
	}
	return name
}

func ParseTimeout(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 24*60*60 {
		return 0, errors.New("timeout must be from 0 to 86400 seconds")
	}
	return seconds, nil
}
