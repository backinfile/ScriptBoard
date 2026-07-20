package app_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scriptboard/internal/app"
)

func TestScheduleTriggersRunAtNextCronTime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "scheduled.sh"
	scriptContent := "printf 'scheduled-output\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "scheduled.cmd"
		scriptContent = "@echo off\r\necho scheduled-output\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	initial := time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(initial.UnixNano())
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot:   managedRoot,
		StateRoot:     stateRoot,
		SchedulerNow:  func() time.Time { return time.Unix(0, clock.Load()).UTC() },
		SchedulerTick: 10 * time.Millisecond,
	})
	response, err := client.Get(serverURL + "/schedules")
	if err != nil {
		t.Fatalf("get schedules: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/schedules", url.Values{
		"name":       {"每分钟计划"},
		"script":     {scriptName},
		"expression": {"1 0 * * *"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/schedules" {
		t.Fatalf("create schedule response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	clock.Store(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC).UnixNano())
	deadline := time.Now().Add(10 * time.Second)
	var runID string
	for {
		response, err = client.Get(serverURL + "/schedules")
		if err != nil {
			t.Fatalf("get schedules after trigger: %v", err)
		}
		page, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(page), `name="last_run_id" value="`) && !strings.Contains(string(page), `name="last_run_id" value=""`) {
			runID = hiddenValue(t, page, "last_run_id")
			if runID != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("schedule did not create Run: %s", page)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		response, err = client.Get(serverURL + "/runs/" + runID)
		if err != nil {
			t.Fatalf("get scheduled run: %v", err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(body), "succeeded") && strings.Contains(string(body), "scheduled-output") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduled Run did not finish: %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
