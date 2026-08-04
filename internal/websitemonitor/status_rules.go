package websitemonitor

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func ParseHTTPStatusRanges(input string) ([]HTTPStatusRange, error) {
	parts := strings.FieldsFunc(input, func(value rune) bool {
		return value == ';' || value == '；' || value == ',' || value == '，' || value == ' ' || value == '\t' || value == '\r' || value == '\n'
	})
	if len(parts) == 0 {
		return nil, errors.New("at least one HTTP status code or range is required")
	}

	ranges := make([]HTTPStatusRange, 0, len(parts))
	for _, part := range parts {
		part = strings.NewReplacer("–", "-", "—", "-").Replace(strings.TrimSpace(part))
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 || bounds[0] == "" || (len(bounds) == 2 && bounds[1] == "") {
			return nil, fmt.Errorf("invalid HTTP status range %q", part)
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil || start < 100 || start > 599 {
			return nil, fmt.Errorf("invalid HTTP status code %q", bounds[0])
		}
		end := start
		if len(bounds) == 2 {
			end, err = strconv.Atoi(bounds[1])
			if err != nil || end < 100 || end > 599 || end < start {
				return nil, fmt.Errorf("invalid HTTP status range %q", part)
			}
		}
		ranges = append(ranges, HTTPStatusRange{Start: start, End: end})
	}
	return ranges, nil
}

func FormatHTTPStatusRanges(ranges []HTTPStatusRange) string {
	parts := make([]string, 0, len(ranges))
	for _, statusRange := range ranges {
		if statusRange.Start == statusRange.End {
			parts = append(parts, strconv.Itoa(statusRange.Start))
			continue
		}
		parts = append(parts, fmt.Sprintf("%d-%d", statusRange.Start, statusRange.End))
	}
	return strings.Join(parts, ";")
}

func ExpectedHTTPStatusRanges(config Config) []HTTPStatusRange {
	if len(config.ExpectedStatusRanges) > 0 {
		return append([]HTTPStatusRange(nil), config.ExpectedStatusRanges...)
	}
	ranges := make([]HTTPStatusRange, 0, len(config.ExpectedStatuses))
	for _, status := range config.ExpectedStatuses {
		ranges = append(ranges, HTTPStatusRange{Start: status, End: status})
	}
	return ranges
}
