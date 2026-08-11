package secretredaction

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

const Marker = "[REDACTED]"

const sensitiveName = `(?:[A-Za-z0-9_.-]+[_-])?(?:password|passwd|pwd|secret|client[_-]?secret|api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|token|authorization|proxy[_-]?authorization|cookie|set[_-]?cookie)`

var redactions = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{
		regexp.MustCompile(`(?is)-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`),
		"-----BEGIN PRIVATE KEY-----\n" + Marker + "\n-----END PRIVATE KEY-----",
	},
	{regexp.MustCompile(`(?i)(\b(?:authorization|proxy[_-]?authorization)\b\s*[:=]\s*)(?:Bearer|Basic)\s+[^\s,;]+`), `${1}` + Marker},
	{regexp.MustCompile(`(?i)(\b(?:Bearer|Basic)\s+)[A-Za-z0-9._~+/=-]+`), `${1}` + Marker},
	{regexp.MustCompile(`(?i)((?:\\")` + sensitiveName + `(?:\\")\s*:\s*(?:\\"))[^"\\]*(\\")`), `${1}` + Marker + `${2}`},
	{regexp.MustCompile(`(?i)("` + sensitiveName + `"\s*:\s*")[^"\r\n]*(")`), `${1}` + Marker + `${2}`},
	{regexp.MustCompile(`(?i)(\b` + sensitiveName + `\b\s*[:=]\s*")[^"\r\n]*(")`), `${1}` + Marker + `${2}`},
	{regexp.MustCompile(`(?i)(\b` + sensitiveName + `\b\s*[:=]\s*')[^'\r\n]*(')`), `${1}` + Marker + `${2}`},
	{regexp.MustCompile(`(?i)(\b` + sensitiveName + `\b\s*[:=]\s*)[^\s,;&#"'\\<>]+`), `${1}` + Marker},
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^/@:\s]+:)[^/@\s]+@`), `${1}` + Marker + `@`},
	{regexp.MustCompile(`\bsbk_[A-Za-z0-9_-]{16}\.[A-Za-z0-9_-]{40,}\b`), Marker},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), Marker},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`), Marker},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), Marker},
	{regexp.MustCompile(`\b(?:sk|pk)_(?:live|test)_[A-Za-z0-9]{12,}\b`), Marker},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`), Marker},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), Marker},
	{regexp.MustCompile(`\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), Marker},
}

var sensitiveFieldName = regexp.MustCompile(`(?i)^` + sensitiveName + `$`)

// String removes common credential forms from text before it crosses an
// observability or export boundary. It intentionally keeps field names and
// authentication schemes when possible so the remaining output is useful.
func String(value string) string {
	for _, redaction := range redactions {
		value = redaction.pattern.ReplaceAllString(value, redaction.replacement)
	}
	return value
}

func Bytes(value []byte) []byte {
	return []byte(String(string(value)))
}

// MarshalJSON produces a valid JSON document whose textual secret forms have
// been removed. It is intended for support/configuration exports, not backups
// that promise lossless credential recovery.
func MarshalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	return json.Marshal(redactJSONValue(document))
}

func redactJSONValue(value any) any {
	switch current := value.(type) {
	case string:
		return String(current)
	case []any:
		for index := range current {
			current[index] = redactJSONValue(current[index])
		}
		return current
	case map[string]any:
		if label, ok := current["name"].(string); ok && sensitiveFieldName.MatchString(strings.TrimSpace(label)) {
			if _, hasValue := current["value"]; hasValue {
				current["value"] = Marker
			}
		}
		for key, item := range current {
			if sensitiveFieldName.MatchString(strings.TrimSpace(key)) {
				current[key] = Marker
				continue
			}
			current[key] = redactJSONValue(item)
		}
		return current
	default:
		return current
	}
}
