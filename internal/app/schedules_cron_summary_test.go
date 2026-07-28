package app

import "testing"

func TestScheduleCronSummaryDescribesGuidedRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		locale webLocale
		fields [5]string
		want   string
	}{
		{name: "daily Chinese", locale: localeSimplifiedChinese, fields: [5]string{"0", "2", "*", "*", "*"}, want: "每天 02:00 执行。"},
		{name: "weekly English", locale: localeEnglishUS, fields: [5]string{"30", "8", "*", "*", "1,3,5"}, want: "Mon, Wed, Fri at 08:30."},
		{name: "monthly Chinese", locale: localeSimplifiedChinese, fields: [5]string{"15", "6", "20", "*", "*"}, want: "每月 20 日 06:15 执行。"},
		{name: "minute interval English", locale: localeEnglishUS, fields: [5]string{"*/20", "*", "*", "*", "*"}, want: "Every 20 minutes."},
		{name: "hour interval Chinese", locale: localeSimplifiedChinese, fields: [5]string{"0", "*/3", "*", "*", "*"}, want: "每 3 小时执行一次。"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := scheduleCronSummary(test.locale, test.fields); got != test.want {
				t.Fatalf("summary = %q, want %q", got, test.want)
			}
		})
	}
}
