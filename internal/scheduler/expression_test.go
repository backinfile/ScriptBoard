package scheduler

import (
	"errors"
	"testing"
	"time"
)

func TestPreviewExpressionNormalizesAndReturnsFiveTimesInInstanceLocation(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 7, 26, 8, 30, 0, 0, location)

	preview, err := PreviewExpression("  0  9  * * mon  ", now)
	if err != nil {
		t.Fatalf("preview expression: %v", err)
	}
	if preview.Expression != "0 9 * * MON" {
		t.Fatalf("normalized expression = %q, want %q", preview.Expression, "0 9 * * MON")
	}
	if preview.Location != "CST" {
		t.Fatalf("location = %q, want CST", preview.Location)
	}
	if len(preview.NextFive) != 5 {
		t.Fatalf("next times = %d, want 5", len(preview.NextFive))
	}
	wantFirst := time.Date(2026, 7, 27, 9, 0, 0, 0, location)
	if !preview.NextFive[0].Equal(wantFirst) || preview.NextFive[0].Location() != location {
		t.Fatalf("first next time = %v (%v), want %v (%v)", preview.NextFive[0], preview.NextFive[0].Location(), wantFirst, location)
	}
}

func TestPreviewExpressionRejectsUnsupportedAndInvalidSyntaxWithFieldErrors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 8, 30, 0, 0, time.UTC)
	tests := []struct {
		name       string
		expression string
		kind       ExpressionErrorKind
		field      ExpressionField
	}{
		{name: "empty", expression: "", kind: ExpressionEmpty},
		{name: "four fields", expression: "0 0 * *", kind: ExpressionFieldCount},
		{name: "six fields", expression: "0 0 0 * * *", kind: ExpressionFieldCount},
		{name: "question mark", expression: "0 0 ? * *", kind: ExpressionUnsupportedSyntax},
		{name: "descriptor", expression: "@daily", kind: ExpressionUnsupportedSyntax},
		{name: "cron timezone", expression: "CRON_TZ=Asia/Shanghai 0 0 * * *", kind: ExpressionTimezone},
		{name: "timezone", expression: "TZ=Asia/Shanghai 0 0 * * *", kind: ExpressionTimezone},
		{name: "minute", expression: "60 0 * * *", kind: ExpressionInvalidField, field: ExpressionMinute},
		{name: "hour", expression: "0 24 * * *", kind: ExpressionInvalidField, field: ExpressionHour},
		{name: "day of month", expression: "0 0 0 * *", kind: ExpressionInvalidField, field: ExpressionDayOfMonth},
		{name: "month", expression: "0 0 * 13 *", kind: ExpressionInvalidField, field: ExpressionMonth},
		{name: "day of week", expression: "0 0 * * 7", kind: ExpressionInvalidField, field: ExpressionDayOfWeek},
		{name: "no future time", expression: "0 0 31 2 *", kind: ExpressionNoFuture},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := PreviewExpression(test.expression, now)
			if err == nil {
				t.Fatal("preview succeeded, want error")
			}
			var expressionErr *ExpressionError
			if !errors.As(err, &expressionErr) {
				t.Fatalf("error type = %T, want *ExpressionError", err)
			}
			if expressionErr.Kind != test.kind || expressionErr.Field != test.field {
				t.Fatalf("error = kind %q field %q, want kind %q field %q", expressionErr.Kind, expressionErr.Field, test.kind, test.field)
			}
		})
	}
}

func TestPreviewExpressionUsesCronOrSemanticsForRestrictedDayFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 8, 30, 0, 0, time.UTC)
	preview, err := PreviewExpression("0 9 1 * MON", now)
	if err != nil {
		t.Fatalf("preview expression: %v", err)
	}
	if !preview.UsesDayOr {
		t.Fatal("UsesDayOr = false, want true")
	}
	want := []time.Time{
		time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
	}
	for index := range want {
		if !preview.NextFive[index].Equal(want[index]) {
			t.Fatalf("next time %d = %v, want %v", index, preview.NextFive[index], want[index])
		}
	}
}
