package web_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	app "scriptboard/internal/web"
)

func TestAdminCanRunScriptAndReadCompletedOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "hello.sh"
	scriptContent := "printf 'hello-run\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "hello.cmd"
		scriptContent = "@echo off\r\necho hello-run\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}

	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"arguments":  {""},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), "/history/runs/") {
		t.Fatalf("start response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatalf("open state database: %v", err)
	}
	defer database.Close()
	var auditDigest string
	if err := database.QueryRow(`SELECT resource_digest_sha256 FROM audit_events
		WHERE action = 'start_run' AND result = 'accepted' ORDER BY id DESC LIMIT 1`).Scan(&auditDigest); err != nil {
		t.Fatalf("read start audit digest: %v", err)
	}
	if expected := fmt.Sprintf("%x", sha256.Sum256([]byte(scriptContent))); auditDigest != expected {
		t.Fatalf("start audit digest = %q, want %q", auditDigest, expected)
	}
	runURL := serverURL + response.Header.Get("Location")

	deadline := time.Now().Add(10 * time.Second)
	var completedPage string
	for {
		response, err = client.Get(runURL)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read run: %v", readErr)
		}
		text := string(body)
		if strings.Contains(text, "succeeded") {
			completedPage = text
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete with output: status=%d body=%s", response.StatusCode, text)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(completedPage, "hello-run") || !strings.Contains(completedPage, `data-run-history-url="`) {
		t.Fatalf("completed Run detail must defer output to history paging: %s", completedPage)
	}
	history := readRunHistoryPage(t, client, runURL+"/history")
	if len(history.Events) != 1 || !strings.Contains(history.Events[0].Text, "hello-run") {
		t.Fatalf("Run history page = %#v", history)
	}
	if !strings.Contains(completedPage, `href="`+strings.TrimPrefix(runURL, serverURL)+`/download"`) ||
		!strings.Contains(completedPage, `data-native`) || !strings.Contains(completedPage, `data-lucide="download"`) {
		t.Fatalf("completed Run detail does not offer a native TXT download: %s", completedPage)
	}
	for _, expected := range []string{
		`data-run-jump-top`, `data-lucide="arrow-up-to-line"`,
		`data-run-jump-bottom`, `data-lucide="arrow-down-to-line"`,
	} {
		if !strings.Contains(completedPage, expected) {
			t.Fatalf("completed Run output controls missing %q: %s", expected, completedPage)
		}
	}

	response, err = client.Get(runURL + "/download")
	if err != nil {
		t.Fatalf("download run record: %v", err)
	}
	downloaded, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatalf("read downloaded run record: %v", readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download run record: status=%d body=%s", response.StatusCode, downloaded)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "text/plain; charset=utf-8" {
		t.Fatalf("download content type=%q", contentType)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "scriptboard-run-") || !strings.Contains(disposition, ".txt") {
		t.Fatalf("download disposition=%q", disposition)
	}
	if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("download cache control=%q", cacheControl)
	}
	downloadText := string(downloaded)
	for _, expected := range []string{"ScriptBoard Run Record", "Run ID:", "Script: " + filepath.Join(hostRoot, scriptName), "Status: succeeded", "Output", "hello-run"} {
		if !strings.Contains(downloadText, expected) {
			t.Fatalf("downloaded run record missing %q: %s", expected, downloadText)
		}
	}
}

func TestRunDisplaysLocalizedOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	const expected = "输出日志：中文正常"
	scriptName := "unicode.sh"
	scriptContent := []byte("printf '输出日志：中文正常\\n'\n")
	if runtime.GOOS == "windows" {
		scriptName = "unicode.ps1"
		scriptContent = []byte("$text = -join [char[]](0x8F93,0x51FA,0x65E5,0x5FD7,0xFF1A,0x4E2D,0x6587,0x6B63,0x5E38)\r\n$bytes = [Text.UTF8Encoding]::new($false).GetBytes($text + [Environment]::NewLine)\r\n[Console]::OpenStandardOutput().Write($bytes, 0, $bytes.Length)\r\n")
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), scriptContent, 0o755); err != nil {
		t.Fatalf("write unicode script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("start unicode run: %v", err)
	}
	_ = response.Body.Close()
	runURL := serverURL + response.Header.Get("Location")
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err = client.Get(runURL)
		if err != nil {
			t.Fatalf("get unicode run: %v", err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read unicode run: %v", readErr)
		}
		text := string(body)
		if strings.Contains(text, "succeeded") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unicode run did not complete: status=%d body=%s", response.StatusCode, text)
		}
		time.Sleep(50 * time.Millisecond)
	}
	history := readRunHistoryPage(t, client, runURL+"/history")
	if len(history.Events) != 1 || !strings.Contains(history.Events[0].Text, expected) {
		t.Fatalf("completed run output is not valid UTF-8 text %q: %#v", expected, history)
	}
}

type runHistoryTestPage struct {
	Events []struct {
		Sequence int64  `json:"sequence"`
		Text     string `json:"text"`
		Source   string `json:"source"`
	} `json:"events"`
	Before  int64 `json:"before"`
	HasMore bool  `json:"hasMore"`
}

func readRunHistoryPage(t *testing.T, client *http.Client, target string) runHistoryTestPage {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("get Run history: %v", err)
	}
	defer response.Body.Close()
	var page runHistoryTestPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatalf("decode Run history: status=%d err=%v", response.StatusCode, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Run history status=%d page=%#v", response.StatusCode, page)
	}
	return page
}

func TestRunEventsAreAvailableAsSSE(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "sse.sh"
	scriptContent := "printf 'sse-output\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "sse.cmd"
		scriptContent = "@echo off\r\necho sse-output\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")
	request, err := http.NewRequest(http.MethodGet, serverURL+runPath+"/events", nil)
	if err != nil {
		t.Fatalf("create SSE request: %v", err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read SSE body: %v", err)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE content type = %q", response.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), "id: 1") || !strings.Contains(string(body), "sse-output") || !strings.Contains(string(body), `"source":"stdout"`) {
		t.Fatalf("unexpected SSE body: %s", body)
	}
}

func TestAdminCanSaveAndStartQuickRunFromHistory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "quick.sh"
	scriptContent := "printf 'quick-output\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "quick.cmd"
		scriptContent = "@echo off\r\necho quick-output\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start source run: %v", err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")
	deadline := time.Now().Add(10 * time.Second)
	var runPage []byte
	for {
		response, _ = client.Get(serverURL + runPath)
		runPage, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(runPage), "succeeded") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("source run did not finish: %s", runPage)
		}
		time.Sleep(25 * time.Millisecond)
	}
	response, err = client.PostForm(serverURL+runPath+"/quick-run", url.Values{
		"name":       {"常用执行"},
		"csrf_token": {formToken(t, runPage)},
	})
	if err != nil {
		t.Fatalf("save quick run: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/config/quick-runs" {
		t.Fatalf("save quick run response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatalf("get quick runs: %v", err)
	}
	quickPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(quickPage), "常用执行") {
		t.Fatalf("saved quick run missing: %s", quickPage)
	}
	response, err = client.PostForm(serverURL+"/config/quick-runs/"+hiddenValue(t, quickPage, "id")+"/start", url.Values{
		"csrf_token": {formToken(t, quickPage)},
	})
	if err != nil {
		t.Fatalf("start quick run: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), "/history/runs/") {
		t.Fatalf("quick start response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestAdminCanCreateQuickRunFromHostFileWithoutStartingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "daily-check.sh"
	scriptContent := "printf 'daily-check\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "daily-check.cmd"
		scriptContent = "@echo off\r\necho daily-check\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	scriptPath := filepath.Join(hostRoot, scriptName)
	quickTaskPath := hostFileHref("/resources/files/quick-run", scriptPath)
	if !strings.Contains(string(filesPage), `href="`+quickTaskPath+`&amp;return_to=`) {
		t.Fatalf("files page does not offer direct Quick Run creation: %s", filesPage)
	}

	response, err = client.Get(serverURL + quickTaskPath)
	if err != nil {
		t.Fatalf("get Quick Run task: %v", err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{
		`data-task-kind="quick-new"`,
		`name="script" value="` + scriptPath + `"`,
		`name="name" autocomplete="off" value="daily-check"`,
		`name="arguments"`,
		`name="timeout_seconds"`,
	} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("Quick Run task does not contain %q: %s", expected, taskPage)
		}
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs", url.Values{
		"csrf_token":      {formToken(t, taskPage)},
		"name":            {"Daily check"},
		"script":          {scriptPath},
		"arguments":       {"--mode safe"},
		"timeout_seconds": {"45"},
	})
	if err != nil {
		t.Fatalf("create Quick Run from file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/config/quick-runs" {
		t.Fatalf("create Quick Run response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatalf("get Quick Runs: %v", err)
	}
	quickPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{"Daily check", scriptName} {
		if !strings.Contains(string(quickPage), expected) {
			t.Fatalf("created Quick Run does not contain %q: %s", expected, quickPage)
		}
	}
	quickRunID := hiddenValue(t, quickPage, "id")
	response, err = client.Get(serverURL + "/config/quick-runs/" + quickRunID + "/edit")
	if err != nil {
		t.Fatalf("get created Quick Run edit task: %v", err)
	}
	quickEditPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`name="arguments"`, `value="--mode safe"`, `name="timeout_seconds"`, `value="45"`} {
		if !strings.Contains(string(quickEditPage), expected) {
			t.Fatalf("created Quick Run edit task missing %q: %s", expected, quickEditPage)
		}
	}

	response, err = client.Get(serverURL + "/history/runs")
	if err != nil {
		t.Fatalf("get Runs: %v", err)
	}
	runsPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if strings.Contains(string(runsPage), scriptName) {
		t.Fatalf("creating a Quick Run unexpectedly started the script: %s", runsPage)
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+"/start", url.Values{
		"csrf_token": {formToken(t, quickPage)},
	})
	if err != nil {
		t.Fatalf("start file-created Quick Run: %v", err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(runPath, "/history/runs/") {
		t.Fatalf("start file-created Quick Run response: status=%d location=%q", response.StatusCode, runPath)
	}
	response, err = client.Get(serverURL + runPath)
	if err != nil {
		t.Fatalf("get file-created Quick Run detail: %v", err)
	}
	runPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(runPage), "Daily check") {
		t.Fatalf("Run detail does not retain Quick Run source name: %s", runPage)
	}
}

func TestQuickRunFromFileRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "safe.sh"
	if runtime.GOOS == "windows" {
		scriptName = "safe.cmd"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte("echo safe\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "notes.txt"), []byte("not executable"), 0o644); err != nil {
		t.Fatalf("write non-script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	scriptPath := filepath.Join(hostRoot, scriptName)
	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/quick-run", scriptPath))
	if err != nil {
		t.Fatalf("get Quick Run task: %v", err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	token := formToken(t, taskPage)

	response, err = client.Get(hostFileRequestURL(serverURL, "/resources/files/quick-run", filepath.Join(hostRoot, "notes.txt")))
	if err != nil {
		t.Fatalf("get non-script Quick Run task: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("non-script Quick Run task status=%d, want %d", response.StatusCode, http.StatusNotFound)
	}

	valid := url.Values{
		"csrf_token":      {token},
		"name":            {"Safe"},
		"script":          {scriptPath},
		"arguments":       {""},
		"timeout_seconds": {"0"},
	}
	tests := []struct {
		name   string
		change func(url.Values)
		status int
	}{
		{name: "missing CSRF", change: func(values url.Values) { values.Del("csrf_token") }, status: http.StatusForbidden},
		{name: "empty name", change: func(values url.Values) { values.Set("name", " ") }, status: http.StatusBadRequest},
		{name: "timeout too large", change: func(values url.Values) { values.Set("timeout_seconds", "86401") }, status: http.StatusBadRequest},
		{name: "malformed arguments", change: func(values url.Values) { values.Set("arguments", `"unterminated`) }, status: http.StatusBadRequest},
		{name: "missing variable", change: func(values url.Values) { values.Set("arguments", "{{MISSING}}") }, status: http.StatusBadRequest},
		{name: "non-script file", change: func(values url.Values) { values.Set("script", filepath.Join(hostRoot, "notes.txt")) }, status: http.StatusBadRequest},
		{name: "path traversal", change: func(values url.Values) { values.Set("script", "../outside.cmd") }, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := url.Values{}
			for key, items := range valid {
				values[key] = append([]string(nil), items...)
			}
			test.change(values)
			response, err := client.PostForm(serverURL+"/config/quick-runs", values)
			if err != nil {
				t.Fatalf("post invalid Quick Run: %v", err)
			}
			_ = response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status=%d, want %d", response.StatusCode, test.status)
			}
		})
	}

	values := url.Values{}
	for key, items := range valid {
		values[key] = append([]string(nil), items...)
	}
	values.Set("return_to", "https://example.com/steal")
	request, err := http.NewRequest(http.MethodPost, serverURL+"/config/quick-runs", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-ScriptBoard-Navigation", "pjax")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("post Quick Run with external return target: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/config/quick-runs" {
		t.Fatalf("unsafe return target was not rejected: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestAdminCanStopRunningScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "wait.sh"
	scriptContent := "sleep 30\n"
	if runtime.GOOS == "windows" {
		scriptName = "wait.cmd"
		scriptContent = "@echo off\r\nping 127.0.0.1 -n 31 >nul\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")
	if !strings.HasPrefix(runPath, "/history/runs/") {
		t.Fatalf("run location = %q", runPath)
	}

	var runPage []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err = client.Get(serverURL + runPath)
		if err != nil {
			t.Fatalf("get running run: %v", err)
		}
		runPage, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(runPage), "running") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not enter running: %s", runPage)
		}
		time.Sleep(25 * time.Millisecond)
	}
	response, err = client.PostForm(serverURL+runPath+"/stop", url.Values{
		"csrf_token": {formToken(t, runPage)},
	})
	if err != nil {
		t.Fatalf("stop run: %v", err)
	}
	stopBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("stop status = %d: %s", response.StatusCode, stopBody)
	}
	deadline = time.Now().Add(10 * time.Second)
	forced := false
	for {
		response, err = client.Get(serverURL + runPath)
		if err != nil {
			t.Fatalf("get stopped run: %v", err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(body), "cancelled") {
			break
		}
		if strings.Contains(string(body), "stopping") && !forced {
			response, err = client.PostForm(serverURL+runPath+"/stop", url.Values{
				"csrf_token": {formToken(t, body)},
			})
			if err != nil {
				t.Fatalf("force stop run: %v", err)
			}
			forceBody, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("force stop status = %d: %s", response.StatusCode, forceBody)
			}
			forced = true
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not become cancelled: %s", body)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestRunningScriptCannotBeDeleted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "leased.sh"
	scriptContent := "sleep 30\n"
	if runtime.GOOS == "windows" {
		scriptName = "leased.cmd"
		scriptContent = "@echo off\r\nping 127.0.0.1 -n 31 >nul\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")

	deadline := time.Now().Add(10 * time.Second)
	for {
		response, _ = client.Get(serverURL + runPath)
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(body), "running") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not enter running: %s", body)
		}
		time.Sleep(25 * time.Millisecond)
	}
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {filepath.Join(hostRoot, scriptName)},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("delete running script: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("delete running script status=%d body=%s", response.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, scriptName)); err != nil {
		t.Fatalf("running script was removed: %v", err)
	}
}

func TestNonZeroRunFailsAndPreservesOutputSources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "fail.sh"
	scriptContent := "printf 'from-out\\n'\nprintf 'from-err\\n' >&2\nexit 7\n"
	if runtime.GOOS == "windows" {
		scriptName = "fail.cmd"
		scriptContent = "@echo off\r\necho from-out\r\necho from-err 1>&2\r\nexit /b 7\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	runURL := serverURL + response.Header.Get("Location")
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err = client.Get(runURL)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		page := string(body)
		if strings.Contains(page, "failed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed run result missing: %s", page)
		}
		time.Sleep(25 * time.Millisecond)
	}
	history := readRunHistoryPage(t, client, runURL+"/history")
	if len(history.Events) != 2 || strings.TrimSpace(history.Events[0].Text) != "from-out" || history.Events[0].Source != "stdout" || strings.TrimSpace(history.Events[1].Text) != "from-err" || history.Events[1].Source != "stderr" {
		t.Fatalf("failed Run output history = %#v", history)
	}
}

func TestRunTimeoutEndsAsTimedOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "timeout.sh"
	scriptContent := "sleep 30\n"
	if runtime.GOOS == "windows" {
		scriptName = "timeout.cmd"
		scriptContent = "@echo off\r\nping 127.0.0.1 -n 31 >nul\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot, RunTimeoutGrace: 100 * time.Millisecond})
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":          {filepath.Join(hostRoot, scriptName)},
		"timeout_seconds": {"1"},
		"csrf_token":      {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start timed run: %v", err)
	}
	_ = response.Body.Close()
	runURL := serverURL + response.Header.Get("Location")
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err = client.Get(runURL)
		if err != nil {
			t.Fatalf("get timed run: %v", err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(body), "timed_out") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not time out: %s", body)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestRunResolvesVariableAsWholeArgument(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	scriptName := "argument.sh"
	scriptContent := "printf '[%s]\\n' \"$1\"\n"
	if runtime.GOOS == "windows" {
		scriptName = "argument.cmd"
		scriptContent = "@echo off\r\necho [%~1]\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(serverURL + "/resources/variables")
	if err != nil {
		t.Fatalf("get variables: %v", err)
	}
	variablesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/variables", url.Values{
		"name":       {"GREETING"},
		"value":      {"hello variable"},
		"csrf_token": {formToken(t, variablesPage)},
	})
	if err != nil {
		t.Fatalf("create variable: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create variable status = %d", response.StatusCode)
	}
	response, err = client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/history/runs/start", url.Values{
		"script":     {filepath.Join(hostRoot, scriptName)},
		"arguments":  {"{{GREETING}}"},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	runURL := serverURL + response.Header.Get("Location")
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err = client.Get(runURL)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(body), "succeeded") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resolved variable output missing: %s", body)
		}
		time.Sleep(25 * time.Millisecond)
	}
	history := readRunHistoryPage(t, client, runURL+"/history")
	if len(history.Events) != 1 || !strings.Contains(history.Events[0].Text, "[hello variable]") {
		t.Fatalf("resolved variable output missing: %#v", history)
	}
}
