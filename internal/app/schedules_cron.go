package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/scheduler"
)

type scheduleCronPreviewTime struct {
	Datetime string `json:"datetime"`
	Label    string `json:"label"`
}

type scheduleCronPreviewPayload struct {
	Valid                bool                      `json:"valid"`
	NormalizedExpression string                    `json:"normalized_expression,omitempty"`
	Summary              string                    `json:"summary,omitempty"`
	Timezone             string                    `json:"timezone,omitempty"`
	Next                 []scheduleCronPreviewTime `json:"next,omitempty"`
	DayOrWarning         string                    `json:"day_or_warning,omitempty"`
	Error                string                    `json:"error,omitempty"`
}

func (a *App) previewScheduleCron(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if !validSessionCSRF(request) {
		payload := scheduleCronPreviewPayload{Error: webText(resolveWebLocale(request), "cron.error.security")}
		if acceptsJSON(request) {
			a.writeScheduleCronPreviewJSON(response, http.StatusForbidden, payload)
		} else {
			http.Error(response, payload.Error, http.StatusForbidden)
		}
		return
	}
	locale := resolveWebLocale(request)
	preview, err := a.scheduler.Preview(request.FormValue("expression"))
	if err != nil {
		payload := scheduleCronPreviewPayload{Error: scheduleCronErrorText(locale, err)}
		if acceptsJSON(request) {
			a.writeScheduleCronPreviewJSON(response, http.StatusUnprocessableEntity, payload)
		} else {
			data := a.scheduleTaskDataFromRequest(request)
			data.CronError = payload.Error
			a.renderTaskPageStatus(response, request, http.StatusUnprocessableEntity, data)
		}
		return
	}
	payload := newScheduleCronPreviewPayload(locale, preview)
	if acceptsJSON(request) {
		a.writeScheduleCronPreviewJSON(response, http.StatusOK, payload)
		return
	}
	data := a.scheduleTaskDataFromRequest(request)
	data.Expression = preview.Expression
	data.CronPreview = payload
	a.renderTaskPageStatus(response, request, http.StatusOK, data)
}

func (a *App) scheduleTaskDataFromRequest(request *http.Request) taskPageData {
	id := request.PathValue("id")
	kind := "schedule-new"
	titleKey := "task.schedule_new.title"
	action := "/config/schedules"
	previewAction := "/config/schedules/preview"
	if id != "" {
		kind = "schedule-edit"
		titleKey = "task.schedule_edit.title"
		action = "/config/schedules/" + url.PathEscape(id) + "/update"
		previewAction = "/config/schedules/" + url.PathEscape(id) + "/preview"
	}
	locale := resolveWebLocale(request)
	groups, _ := a.loadScheduleGroups()
	return taskPageData{
		Kind: kind, Title: webText(locale, titleKey),
		Description: webText(locale, "task.schedule_description"),
		BackURL:     "/config/schedules", Action: action, PreviewAction: previewAction,
		Name: request.FormValue("name"), ScheduleGroupID: strings.TrimSpace(request.FormValue("group_id")),
		ScheduleGroups: groups, Script: request.FormValue("script"),
		Arguments: request.FormValue("arguments"), Expression: request.FormValue("expression"),
		TimeoutInput:    request.FormValue("timeout_seconds"),
		DisallowOverlap: request.FormValue("disallow_overlap") != "",
	}
}

func (a *App) writeScheduleCronPreviewJSON(response http.ResponseWriter, status int, payload scheduleCronPreviewPayload) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func newScheduleCronPreviewPayload(locale webLocale, preview scheduler.ExpressionPreview) scheduleCronPreviewPayload {
	payload := scheduleCronPreviewPayload{
		Valid:                true,
		NormalizedExpression: preview.Expression,
		Summary:              scheduleCronSummary(locale, preview.Fields),
		Timezone:             scheduleCronTimezone(preview.NextFive[0]),
		Next:                 make([]scheduleCronPreviewTime, 0, len(preview.NextFive)),
	}
	if preview.UsesDayOr {
		payload.DayOrWarning = webText(locale, "cron.day_or_warning")
	}
	for _, next := range preview.NextFive {
		payload.Next = append(payload.Next, scheduleCronPreviewTime{
			Datetime: next.Format(time.RFC3339),
			Label:    scheduleCronTimeLabel(locale, next),
		})
	}
	return payload
}

func scheduleCronTimezone(value time.Time) string {
	location := value.Location().String()
	abbreviation, offset := value.Zone()
	if location == "" || location == "Local" {
		location = abbreviation
	}
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s · UTC%s%02d:%02d", location, sign, offset/3600, offset%3600/60)
}

func scheduleCronTimeLabel(locale webLocale, value time.Time) string {
	if locale == localeSimplifiedChinese {
		weekdays := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
		return fmt.Sprintf("%s %s", value.Format("2006年01月02日 15:04"), weekdays[value.Weekday()])
	}
	return value.Format("Mon, Jan 02, 2006 · 15:04")
}

func scheduleCronSummary(locale webLocale, fields [5]string) string {
	expression := strings.Join(fields[:], " ")
	key := ""
	switch expression {
	case "*/15 * * * *":
		key = "cron.summary.every_15_minutes"
	case "0 * * * *":
		key = "cron.summary.hourly"
	case "0 0 * * *":
		key = "cron.summary.daily"
	case "0 9 * * 1", "0 9 * * MON":
		key = "cron.summary.monday"
	case "0 9 * * 1-5", "0 9 * * MON-FRI":
		key = "cron.summary.weekdays"
	case "0 0 1 * *":
		key = "cron.summary.monthly"
	}
	if key != "" {
		return webText(locale, key)
	}
	if fields[1] == "*" && fields[2] == "*" && fields[3] == "*" && fields[4] == "*" && strings.HasPrefix(fields[0], "*/") {
		if amount, err := strconv.Atoi(strings.TrimPrefix(fields[0], "*/")); err == nil && amount > 0 {
			if locale == localeSimplifiedChinese {
				return fmt.Sprintf("每 %d 分钟执行一次。", amount)
			}
			return fmt.Sprintf("Every %d minutes.", amount)
		}
	}
	if fields[0] == "0" && fields[2] == "*" && fields[3] == "*" && fields[4] == "*" && strings.HasPrefix(fields[1], "*/") {
		if amount, err := strconv.Atoi(strings.TrimPrefix(fields[1], "*/")); err == nil && amount > 0 {
			if locale == localeSimplifiedChinese {
				return fmt.Sprintf("每 %d 小时执行一次。", amount)
			}
			return fmt.Sprintf("Every %d hours.", amount)
		}
	}
	minute, minuteErr := strconv.Atoi(fields[0])
	hour, hourErr := strconv.Atoi(fields[1])
	if minuteErr == nil && hourErr == nil && fields[3] == "*" {
		timeLabel := fmt.Sprintf("%02d:%02d", hour, minute)
		switch {
		case fields[2] == "*" && fields[4] == "*":
			if locale == localeSimplifiedChinese {
				return fmt.Sprintf("每天 %s 执行。", timeLabel)
			}
			return fmt.Sprintf("Every day at %s.", timeLabel)
		case fields[4] == "*":
			if day, err := strconv.Atoi(fields[2]); err == nil {
				if locale == localeSimplifiedChinese {
					return fmt.Sprintf("每月 %d 日 %s 执行。", day, timeLabel)
				}
				return fmt.Sprintf("Day %d of every month at %s.", day, timeLabel)
			}
		case fields[2] == "*":
			if weekdays, ok := scheduleCronWeekdaySummary(locale, fields[4]); ok {
				if locale == localeSimplifiedChinese {
					return fmt.Sprintf("%s %s 执行。", weekdays, timeLabel)
				}
				return fmt.Sprintf("%s at %s.", weekdays, timeLabel)
			}
		}
	}
	if locale == localeSimplifiedChinese {
		return fmt.Sprintf("分钟 %s · 小时 %s · 日期 %s · 月份 %s · 星期 %s", fields[0], fields[1], fields[2], fields[3], fields[4])
	}
	return fmt.Sprintf("Minute %s · hour %s · day %s · month %s · weekday %s", fields[0], fields[1], fields[2], fields[3], fields[4])
}

func scheduleCronWeekdaySummary(locale webLocale, field string) (string, bool) {
	english := [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	chinese := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	names := map[string]int{"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6}
	toDay := func(value string) (int, bool) {
		if day, ok := names[value]; ok {
			return day, true
		}
		day, err := strconv.Atoi(value)
		if err != nil || day < 0 || day > 7 {
			return 0, false
		}
		if day == 7 {
			day = 0
		}
		return day, true
	}
	seen := map[int]bool{}
	var days []int
	for _, part := range strings.Split(field, ",") {
		rangeParts := strings.Split(part, "-")
		if len(rangeParts) == 1 {
			day, ok := toDay(rangeParts[0])
			if !ok {
				return "", false
			}
			if !seen[day] {
				seen[day] = true
				days = append(days, day)
			}
			continue
		}
		if len(rangeParts) != 2 {
			return "", false
		}
		start, startOK := toDay(rangeParts[0])
		end, endOK := toDay(rangeParts[1])
		if !startOK || !endOK || start > end {
			return "", false
		}
		for day := start; day <= end; day++ {
			if !seen[day] {
				seen[day] = true
				days = append(days, day)
			}
		}
	}
	if len(days) == 0 {
		return "", false
	}
	labels := make([]string, 0, len(days))
	for _, day := range days {
		if locale == localeSimplifiedChinese {
			labels = append(labels, chinese[day])
		} else {
			labels = append(labels, english[day])
		}
	}
	if locale == localeSimplifiedChinese {
		return strings.Join(labels, "、"), true
	}
	return strings.Join(labels, ", "), true
}

func scheduleCronErrorText(locale webLocale, err error) string {
	var expressionErr *scheduler.ExpressionError
	if !errors.As(err, &expressionErr) {
		return webText(locale, "cron.error.invalid")
	}
	key := "cron.error." + string(expressionErr.Kind)
	if expressionErr.Kind == scheduler.ExpressionInvalidField && expressionErr.Field != "" {
		key += "." + string(expressionErr.Field)
	}
	return webText(locale, key)
}

func isScheduleCronError(err error) bool {
	var expressionErr *scheduler.ExpressionError
	return errors.As(err, &expressionErr)
}

func (a *App) renderScheduleCronSubmissionError(response http.ResponseWriter, request *http.Request, err error) {
	data := a.scheduleTaskDataFromRequest(request)
	data.CronError = scheduleCronErrorText(resolveWebLocale(request), err)
	a.renderTaskPageStatus(response, request, http.StatusUnprocessableEntity, data)
}
