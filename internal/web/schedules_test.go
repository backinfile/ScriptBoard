package web_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	app "scriptboard/internal/web"
)

func TestScheduleTriggersRunAtNextCronTime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "scheduled.sh"
	scriptContent := "printf 'scheduled-output\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "scheduled.cmd"
		scriptContent = "@echo off\r\necho scheduled-output\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	initial := time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(initial.UnixNano())
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot:     stateRoot,
		SchedulerNow:  func() time.Time { return time.Unix(0, clock.Load()).UTC() },
		SchedulerTick: 10 * time.Millisecond,
	})
	response, err := client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatalf("get schedules: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/schedules", url.Values{
		"name":       {"每分钟计划"},
		"script":     {filepath.Join(hostRoot, scriptName)},
		"expression": {"1 0 * * *"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/config/schedules" {
		t.Fatalf("create schedule response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	clock.Store(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC).UnixNano())
	deadline := time.Now().Add(10 * time.Second)
	var runID string
	var scheduleID string
	for {
		response, err = client.Get(serverURL + "/config/schedules")
		if err != nil {
			t.Fatalf("get schedules after trigger: %v", err)
		}
		page, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(page), `name="last_run_id" value="`) && !strings.Contains(string(page), `name="last_run_id" value=""`) {
			runID = hiddenValue(t, page, "last_run_id")
			if runID != "" {
				historyLink := regexp.MustCompile(`href="/history/runs\?q=` + regexp.QuoteMeta(url.QueryEscape("每分钟计划")) + `&amp;schedule_id=([^&"]+)&amp;focus=search"`).FindStringSubmatch(string(page))
				if len(historyLink) != 2 ||
					!strings.Contains(string(page), `data-focus-after-navigation="#run-search"`) ||
					strings.Contains(string(page), `href="/history/runs/`+runID+`"`) {
					t.Fatalf("schedule does not link to focused run history: %s", page)
				}
				scheduleID = historyLink[1]
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("schedule did not create Run: %s", page)
		}
		time.Sleep(20 * time.Millisecond)
	}
	response, err = client.Get(serverURL + "/history/runs?q=" + url.QueryEscape("每分钟计划") + "&schedule_id=" + url.QueryEscape(scheduleID) + "&focus=search")
	if err != nil {
		t.Fatalf("get filtered run history: %v", err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(page), runID) ||
		!strings.Contains(string(page), `value="每分钟计划"`) ||
		!strings.Contains(string(page), `name="schedule_id" value="`+scheduleID+`"`) ||
		!strings.Contains(string(page), `id="run-search"`) ||
		!strings.Contains(string(page), `autofocus`) {
		t.Fatalf("focused run history does not show the scheduled Run: %s", page)
	}
	for {
		response, err = client.Get(serverURL + "/history/runs/" + runID)
		if err != nil {
			t.Fatalf("get scheduled run: %v", err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(body), "succeeded") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduled Run did not finish: %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	history := readRunHistoryPage(t, client, serverURL+"/history/runs/"+runID+"/history")
	if len(history.Events) != 1 || !strings.Contains(history.Events[0].Text, "scheduled-output") {
		t.Fatalf("scheduled Run output missing: %#v", history)
	}
}
