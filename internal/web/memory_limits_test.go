package web_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"scriptboard/internal/store/memorysettings"
	"strings"
	"testing"
	"time"
)

func TestTaskMemoryLimitPersistence(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	state := filepath.Join(root, "state")
	client, base := authenticatedClient(t, managed, state)
	name := "memory.sh"
	source := "#!/bin/sh\necho memory-ok\n"
	if runtime.GOOS == "windows" {
		name = "memory.cmd"
		source = "@echo memory-ok\r\n"
	}
	path := filepath.Join(managed, name)
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(hostFileRequestURL(base, "/resources/files/quick-run", path))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	token := formToken(t, body)
	post := func(route string, values url.Values, intended int) {
		t.Helper()
		values.Set("csrf_token", token)
		response, err := client.PostForm(base+route, values)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != intended {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("%s: %d %s", route, response.StatusCode, body)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	post("/config/quick-runs", url.Values{"name": {"Memory quick"}, "script": {path}, "memory_limit": {"512MiB"}}, http.StatusSeeOther)
	var id, memory string
	var revision int
	if err := db.QueryRow("SELECT id,memory_limit,revision FROM quick_runs WHERE name='Memory quick'").Scan(&id, &memory, &revision); err != nil || memory != "512MiB" {
		t.Fatalf("memory=%q err=%v", memory, err)
	}
	response, err = client.Get(base + "/config/quick-runs/" + id + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), `value="512MiB"`) {
		t.Fatal("edit lost memory limit")
	}
	post("/config/quick-runs/"+id+"/update", url.Values{"name": {"Memory quick"}, "arguments": {""}, "memory_limit": {"unlimited"}}, http.StatusSeeOther)
	var next int
	if err := db.QueryRow("SELECT memory_limit,revision FROM quick_runs WHERE id=?", id).Scan(&memory, &next); err != nil || memory != "unlimited" || next != revision+1 {
		t.Fatalf("memory=%s revision=%d err=%v", memory, next, err)
	}
	post("/config/quick-runs/"+id+"/copy", url.Values{"name": {"Memory copy"}, "script": {path}, "memory_limit": {"256MiB"}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM quick_runs WHERE name='Memory copy'").Scan(&memory); err != nil || memory != "256MiB" {
		t.Fatalf("copy=%s %v", memory, err)
	}
	post("/config/quick-runs", url.Values{"name": {"Invalid"}, "script": {path}, "memory_limit": {"-1"}}, http.StatusBadRequest)
	post("/config/schedules", url.Values{"name": {"Memory schedule"}, "script": {path}, "expression": {"0 0 * * *"}, "memory_limit": {"128MiB"}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM schedules WHERE name='Memory schedule'").Scan(&memory); err != nil || memory != "128MiB" {
		t.Fatalf("schedule=%s %v", memory, err)
	}
	post("/history/runs/start", url.Values{"script": {path}, "memory_limit": {"256MiB"}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM runs ORDER BY created_at DESC LIMIT 1").Scan(&memory); err != nil || memory != "256MiB" {
		t.Fatalf("run=%s %v", memory, err)
	}
	var manualID string
	if err := db.QueryRow("SELECT id FROM runs ORDER BY created_at DESC LIMIT 1").Scan(&manualID); err != nil {
		t.Fatal(err)
	}
	response, err = client.Get(base + "/history/runs/" + manualID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), `name="memory_limit" value="256MiB"`) {
		t.Fatal("manual rerun form lost memory limit")
	}
	post("/config/quick-runs/"+id+"/start", url.Values{"confirm_overlap": {"yes"}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM runs WHERE source_id=? ORDER BY created_at DESC LIMIT 1", id).Scan(&memory); err != nil || memory != "unlimited" {
		t.Fatalf("quick run=%s %v", memory, err)
	}
	var scheduleID string
	if err := db.QueryRow("SELECT id FROM schedules WHERE name='Memory schedule'").Scan(&scheduleID); err != nil {
		t.Fatal(err)
	}
	post("/config/schedules/"+scheduleID+"/run", url.Values{}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM runs WHERE source_id=? ORDER BY created_at DESC LIMIT 1", scheduleID).Scan(&memory); err != nil || memory != "128MiB" {
		t.Fatalf("scheduled run=%s %v", memory, err)
	}

	// Saving a default affects subsequent inherited runs while each existing run retains its snapshot.
	post("/config/quick-runs", url.Values{"name": {"Inherited memory"}, "script": {path}}, http.StatusSeeOther)
	var inheritedID string
	if err := db.QueryRow("SELECT id FROM quick_runs WHERE name='Inherited memory'").Scan(&inheritedID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := memorysettings.Read(state)
	if err != nil {
		t.Fatal(err)
	}
	saveDefault := func(revision, value string, status int) {
		t.Helper()
		values := url.Values{"revision": {revision}, "runner_memory_limit": {snapshot.Memory.Total}, "run_memory_limit": {value}, "runner_process_memory_limit": {snapshot.Memory.Process}, "runner_swap_limit": {snapshot.Memory.Swap}}
		post("/settings/memory", values, status)
	}
	startInherited := func(want string) string {
		t.Helper()
		post("/config/quick-runs/"+inheritedID+"/start", url.Values{"confirm_overlap": {"yes"}}, http.StatusSeeOther)
		var runID, quota string
		if err := db.QueryRow("SELECT id,memory_limit FROM runs WHERE source_id=? ORDER BY rowid DESC LIMIT 1", inheritedID).Scan(&runID, &quota); err != nil || quota != want {
			t.Fatalf("inherited limit=%s want=%s err=%v", quota, want, err)
		}
		return runID
	}
	originalRun := startInherited(snapshot.Memory.PerRun)
	saveDefault(snapshot.Revision, "768MiB", http.StatusSeeOther)
	startInherited("768MiB")
	saveDefault(snapshot.Revision, "1GiB", http.StatusConflict)
	startInherited("768MiB")
	saveDefault(snapshot.Revision, "0", http.StatusUnprocessableEntity)
	startInherited("768MiB")
	if err := db.QueryRow("SELECT memory_limit FROM runs WHERE id=?", originalRun).Scan(&memory); err != nil || memory != snapshot.Memory.PerRun {
		t.Fatalf("existing run changed: %s, %v", memory, err)
	}
	post("/config/quick-runs/"+id+"/start", url.Values{"confirm_overlap": {"yes"}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM runs WHERE source_id=? ORDER BY rowid DESC LIMIT 1", id).Scan(&memory); err != nil || memory != "unlimited" {
		t.Fatalf("explicit quick limit changed: %s, %v", memory, err)
	}
	page := getBody(t, client, base+"/settings/memory", http.StatusOK)
	if !strings.Contains(string(page), "768MiB</small>") && !strings.Contains(string(page), "768MiB ·") {
		t.Fatal("active default not updated")
	}
	if strings.Contains(string(page), "有待重启生效的修改") || strings.Contains(string(page), "changes pending restart") {
		t.Fatal("default-only change must not require restart")
	}
	waitForRuns := func() {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			var count int
			if err := db.QueryRow("SELECT count(*) FROM runs WHERE status IN ('starting','running','stopping','timing_out')").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("runs did not finish")
	}
	waitForRuns()
	language := "shell"
	if runtime.GOOS == "windows" {
		language = "batch"
	}
	post("/config/quick-runs/one-time", url.Values{"working_directory": {managed}, "language": {language}, "source": {source}, "memory_limit": {"192MiB"}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM runs WHERE script_kind='one_time' ORDER BY created_at DESC LIMIT 1").Scan(&memory); err != nil || memory != "192MiB" {
		t.Fatalf("one-time=%s %v", memory, err)
	}
	waitForRuns()
	post("/config/quick-runs/from-source", url.Values{"working_directory": {managed}, "language": {language}, "source": {source}, "file_name": {"memory-created"}, "name": {"Memory source"}, "memory_limit": {"384MiB"}}, http.StatusSeeOther)
	if err := db.QueryRow("SELECT memory_limit FROM quick_runs WHERE name='Memory source'").Scan(&memory); err != nil || memory != "384MiB" {
		t.Fatalf("from-source=%s %v", memory, err)
	}

}
