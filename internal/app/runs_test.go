package app_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/app"
)

func TestAdminCanRunScriptAndReadCompletedOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "hello.sh"
	scriptContent := "printf 'hello-run\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "hello.cmd"
		scriptContent = "@echo off\r\necho hello-run\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}

	response, err = client.PostForm(serverURL+"/runs/start", url.Values{
		"script":     {scriptName},
		"arguments":  {""},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), "/runs/") {
		t.Fatalf("start response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	runURL := serverURL + response.Header.Get("Location")

	deadline := time.Now().Add(10 * time.Second)
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
		if strings.Contains(text, "succeeded") && strings.Contains(text, "hello-run") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete with output: status=%d body=%s", response.StatusCode, text)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAdminCanStopRunningScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "wait.sh"
	scriptContent := "sleep 30\n"
	if runtime.GOOS == "windows" {
		scriptName = "wait.cmd"
		scriptContent = "@echo off\r\nping 127.0.0.1 -n 31 >nul\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/runs/start", url.Values{
		"script":     {scriptName},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")
	if !strings.HasPrefix(runPath, "/runs/") {
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
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "leased.sh"
	scriptContent := "sleep 30\n"
	if runtime.GOOS == "windows" {
		scriptName = "leased.cmd"
		scriptContent = "@echo off\r\nping 127.0.0.1 -n 31 >nul\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/runs/start", url.Values{
		"script":     {scriptName},
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
	response, err = client.PostForm(serverURL+"/files/delete", url.Values{
		"path":       {scriptName},
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
	if _, err := os.Stat(filepath.Join(managedRoot, scriptName)); err != nil {
		t.Fatalf("running script was removed: %v", err)
	}
}

func TestNonZeroRunFailsAndPreservesOutputSources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "fail.sh"
	scriptContent := "printf 'from-out\\n'\nprintf 'from-err\\n' >&2\nexit 7\n"
	if runtime.GOOS == "windows" {
		scriptName = "fail.cmd"
		scriptContent = "@echo off\r\necho from-out\r\necho from-err 1>&2\r\nexit /b 7\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/runs/start", url.Values{
		"script":     {scriptName},
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
		if strings.Contains(page, "failed") && strings.Contains(page, "from-out") && strings.Contains(page, "from-err") {
			if !strings.Contains(page, `data-source="stdout"`) || !strings.Contains(page, `data-source="stderr"`) {
				t.Fatalf("output sources missing: %s", page)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed run result missing: %s", page)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestRunTimeoutEndsAsTimedOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "timeout.sh"
	scriptContent := "sleep 30\n"
	if runtime.GOOS == "windows" {
		scriptName = "timeout.cmd"
		scriptContent = "@echo off\r\nping 127.0.0.1 -n 31 >nul\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{ManagedRoot: managedRoot, StateRoot: stateRoot, RunTimeoutGrace: 100 * time.Millisecond})
	response, err := client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/runs/start", url.Values{
		"script":          {scriptName},
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
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "argument.sh"
	scriptContent := "printf '[%s]\\n' \"$1\"\n"
	if runtime.GOOS == "windows" {
		scriptName = "argument.cmd"
		scriptContent = "@echo off\r\necho [%~1]\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/variables")
	if err != nil {
		t.Fatalf("get variables: %v", err)
	}
	variablesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/variables", url.Values{
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
	response, err = client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/runs/start", url.Values{
		"script":     {scriptName},
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
		if strings.Contains(string(body), "succeeded") && strings.Contains(string(body), "[hello variable]") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resolved variable output missing: %s", body)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
