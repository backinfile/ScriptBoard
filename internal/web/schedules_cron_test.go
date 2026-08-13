package web_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/hostfiles"
	app "scriptboard/internal/web"
)

func TestScheduleCronPreviewReturnsLocalizedJSONWithoutCreatingSchedule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptPath := scheduleTestScript(t, root, "reports/morning.ps1")
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 7, 26, 8, 30, 0, 0, location)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		SchedulerNow: func() time.Time {
			return now
		},
	})
	response, err := client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatalf("get schedule task: %v", err)
	}
	taskBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read schedule task: %v", err)
	}

	values := url.Values{
		"csrf_token": {formToken(t, taskBody)},
		"name":       {"Morning report"},
		"script":     {scriptPath},
		"expression": {" 0 9 * * mon "},
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/config/schedules/preview", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("create preview request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", "en-US")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("preview schedule: %v", err)
	}
	defer response.Body.Close()

	var payload struct {
		Valid                bool   `json:"valid"`
		NormalizedExpression string `json:"normalized_expression"`
		Summary              string `json:"summary"`
		Timezone             string `json:"timezone"`
		Next                 []struct {
			Datetime string `json:"datetime"`
			Label    string `json:"label"`
		} `json:"next"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d, want 200", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q, want no-store", response.Header.Get("Cache-Control"))
	}
	if !payload.Valid || payload.NormalizedExpression != "0 9 * * MON" {
		t.Fatalf("preview validity/expression = %t/%q", payload.Valid, payload.NormalizedExpression)
	}
	if payload.Summary != "Every Monday at 09:00." {
		t.Fatalf("summary = %q", payload.Summary)
	}
	if payload.Timezone != "CST · UTC+08:00" {
		t.Fatalf("timezone = %q", payload.Timezone)
	}
	if len(payload.Next) != 5 || payload.Next[0].Datetime != "2026-07-27T09:00:00+08:00" || payload.Next[0].Label == "" {
		t.Fatalf("next times = %#v", payload.Next)
	}

	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatalf("get schedules: %v", err)
	}
	listBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if strings.Contains(string(listBody), "Morning report") {
		t.Fatal("preview created a schedule")
	}
}

func TestScheduleCronPreviewReturnsLocalizedFieldErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 7, 26, 8, 30, 0, 0, location)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		SchedulerNow: func() time.Time {
			return now
		},
	})
	response, err := client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatalf("get schedule task: %v", err)
	}
	taskBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	token := formToken(t, taskBody)

	tests := []struct {
		expression string
		error      string
	}{
		{expression: "CRON_TZ=Asia/Shanghai 0 9 * * *", error: "表达式不能设置 TZ 或 CRON_TZ；计划统一使用实例时区。"},
		{expression: "60 9 * * *", error: "分钟字段必须在 0–59 范围内。"},
		{expression: "0 9 ? * *", error: "不支持 ? 或 @ 开头的快捷语法。请使用标准五段表达式。"},
	}
	for _, test := range tests {
		values := url.Values{"csrf_token": {token}, "expression": {test.expression}}
		request, err := http.NewRequest(http.MethodPost, serverURL+"/config/schedules/preview", strings.NewReader(values.Encode()))
		if err != nil {
			t.Fatalf("create preview request: %v", err)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Accept-Language", "zh-CN")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("preview %q: %v", test.expression, err)
		}
		var payload struct {
			Valid bool   `json:"valid"`
			Error string `json:"error"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&payload)
		_ = response.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode preview %q: %v", test.expression, decodeErr)
		}
		if response.StatusCode != http.StatusUnprocessableEntity || payload.Valid || payload.Error != test.error {
			t.Fatalf("preview %q = status %d valid %t error %q", test.expression, response.StatusCode, payload.Valid, payload.Error)
		}
	}
}

func TestScheduleCronPreviewRendersNoScriptTaskWithoutLosingFormValues(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptPath := scheduleTestScript(t, root, "reports/morning.ps1")
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 7, 26, 8, 30, 0, 0, location)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		SchedulerNow: func() time.Time {
			return now
		},
	})
	maintenanceGroupID, _ := createScheduleGroupForTest(t, client, serverURL, "日常维护")
	response, err := client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatalf("get schedule task: %v", err)
	}
	taskBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	baseURL, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client.Jar.SetCookies(baseURL, []*http.Cookie{{Name: "scriptboard_locale", Value: "zh-CN"}})

	response, err = client.PostForm(serverURL+"/config/schedules/preview", url.Values{
		"csrf_token":       {formToken(t, taskBody)},
		"name":             {"晨间 <报告>"},
		"group_id":         {maintenanceGroupID},
		"script":           {scriptPath},
		"arguments":        {"--format detailed"},
		"expression":       {"0 9 * * MON"},
		"timeout_seconds":  {"90"},
		"disallow_overlap": {"1"},
	})
	if err != nil {
		t.Fatalf("preview schedule: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read preview: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("preview response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	for _, expected := range []string{
		`action="/config/schedules"`,
		`value="晨间 &lt;报告&gt;"`,
		`value="` + maintenanceGroupID + `" selected`,
		`>日常维护</option>`,
		`value="` + scriptPath + `"`,
		`value="--format detailed"`,
		`value="0 9 * * MON"`,
		`value="90"`,
		`name="disallow_overlap" type="checkbox" value="1" checked`,
		`每周一 09:00 执行。`,
		`CST · UTC&#43;08:00`,
		`datetime="2026-07-27T09:00:00&#43;08:00"`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("preview page missing %q: %s", expected, page)
		}
	}
	if count := strings.Count(page, `data-cron-time`); count != 5 {
		t.Fatalf("preview time count = %d, want 5", count)
	}
}

func TestScheduleGroupsPersistAcrossCreateAndEdit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	securityScript := scheduleTestScript(t, root, "checks/security.ps1")
	location := time.FixedZone("CST", 8*60*60)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		SchedulerNow: func() time.Time {
			return time.Date(2026, 7, 26, 8, 30, 0, 0, location)
		},
	})
	securityGroupID, _ := createScheduleGroupForTest(t, client, serverURL, "Security")
	response, err := client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatalf("get schedule task: %v", err)
	}
	taskBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	taskPage := string(taskBody)
	for _, expected := range []string{
		`class="schedule-plan-form"`,
		`name="group_id"`,
		`value="` + securityGroupID + `"`,
		`data-cron-guided hidden`,
		`class="cron-disclosure cron-raw-editor"`,
		`data-cron-parse`,
		`name="arguments"`,
	} {
		if !strings.Contains(taskPage, expected) {
			t.Fatalf("schedule task missing %q: %s", expected, taskPage)
		}
	}
	if strings.Contains(taskPage, `class="cron-disclosure cron-raw-editor" open`) {
		t.Fatalf("raw cron editor should be collapsed by default: %s", taskPage)
	}

	response, err = client.PostForm(serverURL+"/config/schedules", url.Values{
		"csrf_token":       {formToken(t, taskBody)},
		"name":             {"Daily security check"},
		"group_id":         {securityGroupID},
		"script":           {securityScript},
		"arguments":        {`--report "{{REPORT_PATH}}"`},
		"expression":       {"0 2 * * *"},
		"timeout_seconds":  {"90"},
		"disallow_overlap": {"1"},
	})
	if err != nil {
		t.Fatalf("create grouped schedule: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatalf("get grouped schedules: %v", err)
	}
	listBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	listPage := string(listBody)
	if !strings.Contains(listPage, "Daily security check") || !strings.Contains(listPage, "Security") {
		t.Fatalf("grouped schedule is missing from list: %s", listBody)
	}
	for _, expected := range []string{
		`data-grouped-records="schedule-groups"`,
		`data-schedule-group="` + securityGroupID + `"`,
		`data-group-name="Security"`,
		`data-schedule-id="`,
		`class="quick-run-group__toggle"`,
	} {
		if !strings.Contains(listPage, expected) {
			t.Fatalf("grouped schedule list missing %q: %s", expected, listPage)
		}
	}
	match := regexp.MustCompile(`/config/schedules/([^"/]+)/edit`).FindSubmatch(listBody)
	if len(match) != 2 {
		t.Fatalf("find grouped schedule id: %s", listBody)
	}
	id := string(match[1])

	response, err = client.Get(serverURL + "/config/schedules/" + id + "/edit")
	if err != nil {
		t.Fatalf("get grouped schedule edit task: %v", err)
	}
	editBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	editPage := string(editBody)
	if !strings.Contains(editPage, `name="group_id"`) ||
		!strings.Contains(editPage, `value="`+securityGroupID+`" selected`) ||
		!strings.Contains(editPage, `>Security</option>`) {
		t.Fatalf("edit task did not preserve the Schedule group: %s", editPage)
	}

	infrastructureGroupID, _ := createScheduleGroupForTest(t, client, serverURL, "Infrastructure")
	response, err = client.PostForm(serverURL+"/config/schedules/"+id+"/update", url.Values{
		"csrf_token":       {formToken(t, editBody)},
		"name":             {"Daily security check"},
		"group_id":         {infrastructureGroupID},
		"script":           {securityScript},
		"arguments":        {`--report "{{REPORT_PATH}}"`},
		"expression":       {"0 2 * * *"},
		"timeout_seconds":  {"90"},
		"disallow_overlap": {"1"},
	})
	if err != nil {
		t.Fatalf("update grouped schedule: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatalf("get updated grouped schedules: %v", err)
	}
	listBody, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(listBody), "Infrastructure") ||
		!regexp.MustCompile(`data-group-name="Infrastructure"[\s\S]*?Daily security check`).Match(listBody) {
		t.Fatalf("updated Schedule group is not reflected in the list: %s", listBody)
	}
}

func TestScheduleSubmissionRendersCronErrorsWithoutWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dailyScript := scheduleTestScript(t, root, "reports/daily.ps1")
	location := time.FixedZone("CST", 8*60*60)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(root, "state"),
		SchedulerNow: func() time.Time {
			return time.Date(2026, 7, 26, 8, 30, 0, 0, location)
		},
	})
	response, err := client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatalf("get schedule task: %v", err)
	}
	taskBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	invalidValues := url.Values{
		"csrf_token":       {formToken(t, taskBody)},
		"name":             {"Invalid schedule draft"},
		"script":           {dailyScript},
		"arguments":        {"--format detailed"},
		"expression":       {"60 9 * * *"},
		"timeout_seconds":  {"75"},
		"disallow_overlap": {"1"},
	}
	response, err = client.PostForm(serverURL+"/config/schedules", invalidValues)
	if err != nil {
		t.Fatalf("create invalid schedule: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	page := string(body)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create status = %d, want 422: %s", response.StatusCode, page)
	}
	for _, expected := range []string{
		"The minute field must be within 0–59.",
		`value="Invalid schedule draft"`,
		`value="` + dailyScript + `"`,
		`value="--format detailed"`,
		`value="60 9 * * *"`,
		`value="75"`,
		`name="disallow_overlap" type="checkbox" value="1" checked`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("invalid create page missing %q", expected)
		}
	}

	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatalf("get schedules after invalid create: %v", err)
	}
	listBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if strings.Contains(string(listBody), "Invalid schedule draft") {
		t.Fatal("invalid create wrote a schedule")
	}

	response, err = client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatalf("get fresh schedule task: %v", err)
	}
	taskBody, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/schedules", url.Values{
		"csrf_token": {formToken(t, taskBody)},
		"name":       {"Stable schedule"},
		"script":     {dailyScript},
		"expression": {"  0  9  *  *  mon  "},
	})
	if err != nil {
		t.Fatalf("create valid schedule: %v", err)
	}
	_ = response.Body.Close()

	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatalf("get schedules after valid create: %v", err)
	}
	listBody, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	match := regexp.MustCompile(`/config/schedules/([^"/]+)/edit`).FindSubmatch(listBody)
	if len(match) != 2 {
		t.Fatalf("find schedule id: %s", listBody)
	}
	if !strings.Contains(string(listBody), `datetime="2026-07-27T09:00:00&#43;08:00"`) || strings.Contains(string(listBody), "data-local-time") {
		t.Fatalf("schedule list does not preserve the instance timezone: %s", listBody)
	}
	id := string(match[1])

	response, err = client.Get(serverURL + "/config/schedules/" + id + "/edit")
	if err != nil {
		t.Fatalf("get edit schedule task: %v", err)
	}
	editBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/schedules/"+id+"/update", url.Values{
		"csrf_token": {formToken(t, editBody)},
		"name":       {"Edited draft"},
		"script":     {dailyScript},
		"expression": {"0 9 ? * *"},
	})
	if err != nil {
		t.Fatalf("update invalid schedule: %v", err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "Question marks and @ descriptors are not supported.") {
		t.Fatalf("invalid update response = %d: %s", response.StatusCode, body)
	}

	response, err = client.Get(serverURL + "/config/schedules/" + id + "/edit")
	if err != nil {
		t.Fatalf("get schedule after invalid update: %v", err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), `value="Stable schedule"`) || !strings.Contains(string(body), `value="0 9 * * MON"`) {
		t.Fatalf("invalid update changed stored schedule: %s", body)
	}
}

func TestSchedulerDisablesInvalidStoredExpressionsAtStartup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	config := app.Config{
		StateRoot: stateRoot,
		SchedulerNow: func() time.Time {
			return time.Date(2026, 7, 26, 8, 30, 0, 0, time.UTC)
		},
	}
	application, err := app.Open(config)
	if err != nil {
		t.Fatalf("create application database: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("close initial application: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	now := config.SchedulerNow().UnixNano()
	scriptPath := filepath.Join(root, "reports", "daily.ps1")
	if _, err := db.Exec(`INSERT INTO schedules
		(id, name, script_path, script_path_key, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, created_at, updated_at)
		VALUES ('invalid-cron', 'Invalid Cron', ?, ?, '', '0 9 ? * *', 0, 1, 1, ?, ?, ?)`,
		scriptPath, hostfiles.ComparisonKey(scriptPath), now, now, now); err != nil {
		t.Fatalf("insert invalid schedule: %v", err)
	}

	application, err = app.Open(config)
	if err != nil {
		t.Fatalf("reopen application: %v", err)
	}
	defer application.Close()

	var enabled bool
	if err := db.QueryRow("SELECT enabled FROM schedules WHERE id = 'invalid-cron'").Scan(&enabled); err != nil {
		t.Fatalf("read invalid schedule: %v", err)
	}
	if enabled {
		t.Fatal("invalid stored schedule remained enabled")
	}
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'disable_invalid_schedule' AND target = '0 9 ? * *' AND result = 'failed' AND source_address = 'scheduler'`).Scan(&auditCount); err != nil {
		t.Fatalf("read invalid schedule audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("invalid schedule audit count = %d, want 1", auditCount)
	}
}

func scheduleTestScript(t *testing.T, root, relative string) string {
	t.Helper()
	path := filepath.Join(root, "host", filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create schedule script directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("test script\n"), 0o755); err != nil {
		t.Fatalf("write schedule script: %v", err)
	}
	return path
}
