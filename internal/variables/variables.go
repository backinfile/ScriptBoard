// Package variables owns Variable value types and their storage format.
package variables

import (
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxValueBytes = 4 << 10

type Kind string

type VersionPart string

const (
	KindText    Kind = "text"
	KindBool    Kind = "bool"
	KindInteger Kind = "integer"
	KindFloat   Kind = "float"
	KindVersion Kind = "version"

	VersionPartMajor VersionPart = "major"
	VersionPartMinor VersionPart = "minor"
	VersionPartPatch VersionPart = "patch"
)

var (
	ErrInvalidKind        = errors.New("invalid variable kind")
	ErrInvalidValue       = errors.New("invalid variable value")
	ErrInvalidVersionPart = errors.New("invalid version part")
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

// IncrementVersion derives the next canonical version without narrowing its
// numeric parts to machine-sized integers.
func IncrementVersion(value string, part VersionPart) (string, error) {
	if _, err := Parse(KindVersion, value); err != nil {
		return "", err
	}
	if part != VersionPartMajor && part != VersionPartMinor && part != VersionPartPatch {
		return "", ErrInvalidVersionPart
	}

	parts := strings.Split(value, ".")
	index := map[VersionPart]int{
		VersionPartMajor: 0,
		VersionPartMinor: 1,
		VersionPartPatch: 2,
	}[part]
	number, ok := new(big.Int).SetString(parts[index], 10)
	if !ok {
		return "", ErrInvalidValue
	}
	parts[index] = number.Add(number, big.NewInt(1)).String()
	for lower := index + 1; lower < len(parts); lower++ {
		parts[lower] = "0"
	}
	next := strings.Join(parts, ".")
	if _, err := Parse(KindVersion, next); err != nil {
		return "", err
	}
	return next, nil
}
