package web_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"scriptboard/internal/config"
	app "scriptboard/internal/web"
	"strings"
	"testing"
)

func TestMemorySettingsFormSaveValidationAndConflict(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	os.WriteFile(path, []byte("listen: 127.0.0.1:7777\n"), 0600)
	initial, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(root, "state"), ConfigPath: path, Memory: initial.Memory})
	page := getBody(t, client, base+"/settings/memory", 200)
	if !strings.Contains(string(page), "data-memory-settings") {
		t.Fatal("page missing")
	}
	revision := regexp.MustCompile(`name="revision" value="([^"]+)"`).FindSubmatch(page)
	if len(revision) != 2 {
		t.Fatal("revision missing")
	}
	token := formToken(t, page)
	values := url.Values{"csrf_token": {token}, "revision": {string(revision[1])}, "runner_memory_limit": {"8GiB"}, "run_memory_limit": {"512MiB"}}
	if runtime.GOOS == "windows" {
		values.Set("runner_process_memory_limit", "2GiB")
	} else {
		values.Set("runner_swap_limit", "0")
	}
	post := func(want int) {
		t.Helper()
		response, err := client.PostForm(base+"/settings/memory", values)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("got %d want %d", response.StatusCode, want)
		}
	}
	values.Set("csrf_token", "")
	post(403)
	values.Set("csrf_token", token)
	values.Set("runner_memory_limit", "0")
	post(422)
	values.Set("runner_memory_limit", "8GiB")
	post(303)
	saved, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Memory.Total != "8GiB" || saved.Memory.PerRun != "512MiB" {
		t.Fatal("settings not persisted")
	}
	post(409)
	values.Set("revision", saved.Revision)
	values.Set("run_memory_limit", "unlimited")
	post(303)
	request, _ := http.NewRequest("POST", base+"/settings/memory", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://foreign.example")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin accepted")
	}
	// An expired confirmation must challenge before changing the configuration.
	beforeChallenge, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	database := openConcurrentAppTestDatabase(t, filepath.Join(root, "state", "app.db"))
	if _, err := database.Exec("UPDATE sessions SET reauthenticated_at = 0"); err != nil {
		t.Fatal(err)
	}
	values.Set("revision", beforeChallenge.Revision)
	values.Set("runner_memory_limit", "9GiB")
	challengeRequest, _ := http.NewRequest("POST", base+"/settings/memory", strings.NewReader(values.Encode()))
	challengeRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	challengeRequest.Header.Set("X-ScriptBoard-Step-Up", "dialog")
	challengeResponse, err := client.Do(challengeRequest)
	if err != nil {
		t.Fatal(err)
	}
	challengeResponse.Body.Close()
	if challengeResponse.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("challenge status=%d", challengeResponse.StatusCode)
	}
	afterChallenge, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if afterChallenge != beforeChallenge {
		t.Fatal("expired confirmation changed settings")
	}
	anonymous, err := http.Get(base + "/settings/memory")
	if err != nil {
		t.Fatal(err)
	}
	anonymous.Body.Close()
	if !strings.Contains(anonymous.Request.URL.Path, "login") {
		t.Fatal("anonymous settings access")
	}
}
