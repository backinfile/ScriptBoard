package app

import (
	"bytes"
	"errors"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"scriptboard/internal/pathsecurity"
)

var acceptedJSONImportMediaTypes = map[string]struct{}{
	"application/json": {},
	"text/json":        {},
	"text/plain":       {},
	// Browsers and generic multipart clients commonly use this when they do
	// not know that a selected .json file is textual.
	"application/octet-stream": {},
}

func validateJSONConfigurationImport(filename, contentType string, raw []byte, maximumBytes int64) error {
	filename = strings.TrimSpace(filename)
	if filename == "" || len(filename) > 255 || filepath.Base(filename) != filename || pathsecurity.UnsafeWindowsComponent(filename) || !strings.EqualFold(filepath.Ext(filename), ".json") {
		return errors.New("configuration import must use a safe .json filename")
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return errors.New("configuration import has an invalid media type")
	}
	if _, accepted := acceptedJSONImportMediaTypes[strings.ToLower(mediaType)]; !accepted {
		return errors.New("configuration import media type is not allowed")
	}
	if maximumBytes <= 0 || int64(len(raw)) > maximumBytes || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return errors.New("configuration import content is invalid or too large")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("configuration import must be a JSON object")
	}
	return nil
}
