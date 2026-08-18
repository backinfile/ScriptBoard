// Package variables owns Variable value types and their storage format.
package variables

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"unicode/utf8"
)

const maxValueBytes = 4 << 10

type Kind string

const (
	KindText    Kind = "text"
	KindBool    Kind = "bool"
	KindInteger Kind = "integer"
	KindFloat   Kind = "float"
	KindVersion Kind = "version"
)

var (
	ErrInvalidKind  = errors.New("invalid variable kind")
	ErrInvalidValue = errors.New("invalid variable value")
)
var integerPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)
var floatPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
var versionPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$`)

func Parse(kind Kind, raw any) (string, error) {
	switch kind {
	case KindText, KindBool, KindInteger, KindFloat, KindVersion:
	default:
		return "", ErrInvalidKind
	}
	if value, ok := raw.(bool); ok && kind == KindBool {
		return strconv.FormatBool(value), nil
	}
	var value string
	switch typed := raw.(type) {
	case string:
		value = typed
	case json.Number:
		value = string(typed)
	default:
		return "", ErrInvalidValue
	}
	if !utf8.ValidString(value) || len([]byte(value)) > maxValueBytes {
		return "", ErrInvalidValue
	}
	switch kind {
	case KindText:
		return value, nil
	case KindBool:
		if value == "true" || value == "false" {
			return value, nil
		}
	case KindInteger:
		if integerPattern.MatchString(value) {
			return value, nil
		}
	case KindFloat:
		if floatPattern.MatchString(value) {
			return value, nil
		}
	case KindVersion:
		if versionPattern.MatchString(value) {
			return value, nil
		}
	}
	return "", ErrInvalidValue
}
