package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var fiveFieldParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type ExpressionErrorKind string

const (
	ExpressionEmpty             ExpressionErrorKind = "empty"
	ExpressionFieldCount        ExpressionErrorKind = "field_count"
	ExpressionUnsupportedSyntax ExpressionErrorKind = "unsupported_syntax"
	ExpressionTimezone          ExpressionErrorKind = "timezone"
	ExpressionInvalidField      ExpressionErrorKind = "invalid_field"
	ExpressionNoFuture          ExpressionErrorKind = "no_future"
)

type ExpressionField string

const (
	ExpressionMinute     ExpressionField = "minute"
	ExpressionHour       ExpressionField = "hour"
	ExpressionDayOfMonth ExpressionField = "day_of_month"
	ExpressionMonth      ExpressionField = "month"
	ExpressionDayOfWeek  ExpressionField = "day_of_week"
)

var expressionFields = [...]ExpressionField{
	ExpressionMinute,
	ExpressionHour,
	ExpressionDayOfMonth,
	ExpressionMonth,
	ExpressionDayOfWeek,
}

type ExpressionError struct {
	Kind  ExpressionErrorKind
	Field ExpressionField
	cause error
}

func (e *ExpressionError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("invalid five-field Cron %s: %v", e.Field, e.cause)
	}
	if e.cause != nil {
		return fmt.Sprintf("invalid five-field Cron: %v", e.cause)
	}
	return "invalid five-field Cron"
}

func (e *ExpressionError) Unwrap() error {
	return e.cause
}

// ExpressionPreview is the scheduler-owned interpretation of a five-field Cron
// expression. Callers can present it without reimplementing scheduling rules.
type ExpressionPreview struct {
	Expression string
	Fields     [5]string
	NextFive   []time.Time
	Location   string
	UsesDayOr  bool
}

// PreviewExpression validates an expression and calculates its next five
// triggers using the location attached to now.
func PreviewExpression(expression string, now time.Time) (ExpressionPreview, error) {
	_, preview, err := parseExpression(expression, now)
	return preview, err
}

func parseExpression(expression string, now time.Time) (cron.Schedule, ExpressionPreview, error) {
	trimmed := strings.TrimSpace(expression)
	if trimmed == "" {
		return nil, ExpressionPreview{}, &ExpressionError{Kind: ExpressionEmpty}
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "TZ=") || strings.HasPrefix(upper, "CRON_TZ=") {
		return nil, ExpressionPreview{}, &ExpressionError{Kind: ExpressionTimezone}
	}
	if strings.ContainsAny(upper, "?@") {
		return nil, ExpressionPreview{}, &ExpressionError{Kind: ExpressionUnsupportedSyntax}
	}
	fields := strings.Fields(upper)
	if len(fields) != len(expressionFields) {
		return nil, ExpressionPreview{}, &ExpressionError{
			Kind:  ExpressionFieldCount,
			cause: fmt.Errorf("got %d fields, want 5", len(fields)),
		}
	}
	normalized := strings.Join(fields, " ")
	spec, err := fiveFieldParser.Parse(normalized)
	if err != nil {
		for index, field := range fields {
			candidate := []string{"*", "*", "*", "*", "*"}
			candidate[index] = field
			if _, fieldErr := fiveFieldParser.Parse(strings.Join(candidate, " ")); fieldErr != nil {
				return nil, ExpressionPreview{}, &ExpressionError{
					Kind: ExpressionInvalidField, Field: expressionFields[index], cause: fieldErr,
				}
			}
		}
		return nil, ExpressionPreview{}, &ExpressionError{Kind: ExpressionInvalidField, cause: err}
	}
	preview := ExpressionPreview{
		Expression: normalized,
		Fields:     [5]string(fields),
		Location:   now.Location().String(),
		UsesDayOr:  !expressionFieldUsesWildcard(fields[2]) && !expressionFieldUsesWildcard(fields[4]),
	}
	cursor := now
	for range 5 {
		cursor = spec.Next(cursor)
		if cursor.IsZero() {
			return nil, ExpressionPreview{}, &ExpressionError{Kind: ExpressionNoFuture}
		}
		preview.NextFive = append(preview.NextFive, cursor)
	}
	return spec, preview, nil
}

func expressionFieldUsesWildcard(field string) bool {
	for _, item := range strings.Split(field, ",") {
		if strings.HasPrefix(item, "*") {
			return true
		}
	}
	return false
}
