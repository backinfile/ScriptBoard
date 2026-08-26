package web_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
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
)

func TestQuickRunPageOffersOneTimeAndCreateActions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{
		`href="/config/quick-runs/one-time/new"`,
		`href="/config/quick-runs/from-source/new"`,
		`>Run once<`,
		`>Create<`,
	} {
		if !bytes.Contains(page, []byte(expected)) {
			t.Fatalf("Quick Run page is missing %q: %s", expected, page)
		}
	}

	for _, task := range []struct {
		path, kind string
	}{
		{"/config/quick-runs/one-time/new", "one-time-run"},
		{"/config/quick-runs/from-source/new", "quick-create"},
	} {
		response, err = client.Get(serverURL + task.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`data-task-kind="`+task.kind+`"`)) {
			t.Fatalf("%s status=%d body=%s", task.path, response.StatusCode, body)
		}
		for _, expected := range []string{`name="source"`, `type="hidden" name="working_directory"`, `name="language"`, `data-directory-tree`} {
			if !bytes.Contains(body, []byte(expected)) {
				t.Fatalf("%s is missing %q: %s", task.path, expected, body)
			}
		}
		if bytes.Contains(body, []byte(`data-directory-browse`)) || bytes.Contains(body, []byte(`name="working_directory" autocomplete=`)) {
			t.Fatalf("%s still exposes a manually editable working directory: %s", task.path, body)
		}
		if !bytes.Contains(body, []byte(`name="working_directory" value="`+hostRoot+`"`)) {
			t.Fatalf("%s does not default to an executable host directory: %s", task.path, body)
		}
	}
}

func TestHostDirectoryPickerOnlyListsValidatedDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "ops", "nightly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "ops", "notes.txt"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	selectedDirectory := filepath.Join(hostRoot, "ops")
	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/directories", selectedDirectory))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"name":"nightly"`)) ||
		!bytes.Contains(body, []byte(`"path":`)) || bytes.Contains(body, []byte("notes.txt")) {
		t.Fatalf("directory response status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.Get(serverURL + "/resources/directories?path=..%2F..")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal status=%d, want 400", response.StatusCode)
	}
}

func TestOneTimeRunKeepsImmutableSourceSnapshotAndUsesSelectedWorkdir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	workdir := filepath.Join(hostRoot, "ops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(serverURL + "/config/quick-runs/one-time/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	language, extension, source := "shell", ".sh", "#!/bin/sh\nprintf one-time-ok > marker.txt\n"
	if runtime.GOOS == "windows" {
		language, extension, source = "batch", ".cmd", "@echo off\r\ntype nul > marker.txt\r\nping -n 2 127.0.0.1 >nul\r\necho one-time-ok>marker.txt\r\n"
	}
	response, err = client.PostForm(serverURL+"/config/quick-runs/one-time", url.Values{
		"csrf_token":        {formToken(t, taskPage)},
		"working_directory": {workdir},
		"language":          {language},
		"source":            {source},
		"timeout_seconds":   {"30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("start status=%d body=%s", response.StatusCode, body)
	}
	location := response.Header.Get("Location")
	const runPrefix = "/history/runs/"
	if !strings.HasPrefix(location, runPrefix) {
		t.Fatalf("redirect location=%q", location)
	}
	runID := strings.TrimPrefix(location, runPrefix)

	deadline := time.Now().Add(10 * time.Second)
	var marker []byte
	var readErr error
	for {
		marker, readErr = os.ReadFile(filepath.Join(workdir, "marker.txt"))
		if readErr == nil && strings.Contains(string(marker), "one-time-ok") {
			break
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatalf("read one-time marker: %v", readErr)
		}
		if time.Now().After(deadline) {
			if readErr != nil {
				t.Fatalf("one-time script did not run in selected workdir: %v", readErr)
			}
			t.Fatalf("marker=%q", marker)
		}
		time.Sleep(25 * time.Millisecond)
	}

	sourcePath := filepath.Join(stateRoot, "runs", runID, "source"+extension)
	snapshot, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read private source snapshot: %v", err)
	}
	if string(snapshot) != source {
		t.Fatalf("private source=%q, want %q", snapshot, source)
	}
	db, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var scriptKind, recordedWorkdir, sourceFilename, auditAction, auditTarget string
	if err := db.QueryRow(`SELECT runs.script_kind, runs.working_directory, runs.source_filename,
		audit_events.action, audit_events.target
		FROM runs JOIN audit_events ON audit_events.id = runs.source_audit_event_id
		WHERE runs.id = ?`, runID).Scan(&scriptKind, &recordedWorkdir, &sourceFilename, &auditAction, &auditTarget); err != nil {
		t.Fatalf("read one-time Run metadata: %v", err)
	}
	if scriptKind != "one_time" || recordedWorkdir != workdir || sourceFilename != "source"+extension ||
		auditAction != "start_one_time_run" || auditTarget != runID {
		t.Fatalf("metadata kind=%q workdir=%q source=%q audit=%q target=%q", scriptKind, recordedWorkdir, sourceFilename, auditAction, auditTarget)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o222 != 0 {
			t.Fatalf("private source is writable: mode=%#o", info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(workdir, "source"+extension)); !os.IsNotExist(err) {
		t.Fatalf("source was created in workdir: %v", err)
	}

	response, err = client.Get(serverURL + location)
	if err != nil {
		t.Fatal(err)
	}
	runPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{location + "/source", location + "/rerun", "<code>" + workdir + "</code>", "View source", "Run again"} {
		if !bytes.Contains(runPage, []byte(expected)) {
			t.Fatalf("Run detail is missing %q: %s", expected, runPage)
		}
	}
	if bytes.Contains(runPage, []byte("save-quick-run")) {
		t.Fatalf("one-time Run unexpectedly offers direct Quick Run persistence: %s", runPage)
	}

	response, err = client.Get(serverURL + location + "/source")
	if err != nil {
		t.Fatal(err)
	}
	sourcePage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(sourcePage, []byte(source)) {
		t.Fatalf("source view status=%d body=%s", response.StatusCode, sourcePage)
	}

	runPageToken := formToken(t, runPage)
	response, err = client.PostForm(serverURL+location+"/rerun", url.Values{"csrf_token": {runPageToken}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), runPrefix) || response.Header.Get("Location") == location {
		t.Fatalf("rerun status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestCreateQuickRunFromSourceWritesScriptWithoutRunningIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(serverURL + "/config/quick-runs/from-source/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	language, extension, source := "shell", ".sh", "#!/bin/sh\nprintf should-not-run > marker.txt\n"
	if runtime.GOOS == "windows" {
		language, extension, source = "batch", ".cmd", "@echo off\r\necho should-not-run>marker.txt\r\n"
	}
	response, err = client.PostForm(serverURL+"/config/quick-runs/from-source", url.Values{
		"csrf_token":        {formToken(t, taskPage)},
		"working_directory": {filepath.Join(hostRoot, "ops")},
		"language":          {language},
		"file_name":         {"inventory"},
		"source":            {source},
		"name":              {"Inventory snapshot"},
		"arguments":         {`--mode "safe"`},
		"timeout_seconds":   {"30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d body=%s", response.StatusCode, body)
	}

	scriptPath := filepath.Join(hostRoot, "ops", "inventory"+extension)
	created, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read created script: %v", err)
	}
	if string(created) != source {
		t.Fatalf("created source = %q, want %q", created, source)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, "ops", "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("script ran during creation: %v", err)
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var digest string
	var revision int64
	if err := database.QueryRow("SELECT script_sha256, revision FROM quick_runs WHERE name = 'Inventory snapshot'").Scan(&digest, &revision); err != nil {
		t.Fatal(err)
	}
	if expected := fmt.Sprintf("%x", sha256.Sum256([]byte(source))); digest != expected || revision != 1 {
		t.Fatalf("published Quick Run digest=%q revision=%d, want %q revision=1", digest, revision, expected)
	}
	var auditRevision, auditDigest string
	if err := database.QueryRow(`SELECT resource_revision, resource_digest_sha256 FROM audit_events
		WHERE action = 'create_quick_run_from_source' ORDER BY id DESC LIMIT 1`).Scan(&auditRevision, &auditDigest); err != nil {
		t.Fatal(err)
	}
	if auditRevision != "1" || auditDigest != digest {
		t.Fatalf("create audit revision=%q digest=%q, want revision=1 digest=%q", auditRevision, auditDigest, digest)
	}

	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{"Inventory snapshot", scriptPath} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("Quick Run page is missing %q: %s", expected, page)
		}
	}
}

func TestCreateQuickRunFromSourceDoesNotDuplicateLanguageExtension(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(serverURL + "/config/quick-runs/from-source/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	language, extension, source := "shell", ".sh", "#!/bin/sh\nprintf extension-ok\\n"
	if runtime.GOOS == "windows" {
		language, extension, source = "batch", ".cmd", "@echo off\r\necho extension-ok\r\n"
	}
	fileName := "extension-test" + extension
	response, err = client.PostForm(serverURL+"/config/quick-runs/from-source", url.Values{
		"csrf_token": {formToken(t, taskPage)}, "language": {language}, "source": {source},
		"working_directory": {hostRoot}, "file_name": {fileName}, "name": {"Extension test"},
		"timeout_seconds": {"30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("quick create status=%d body=%s", response.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, fileName)); err != nil {
		t.Fatalf("single-extension script was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, fileName+extension)); !os.IsNotExist(err) {
		t.Fatalf("duplicated-extension script exists: %v", err)
	}
}

func TestCreateQuickRunFromSourceRequiresExplicitCollisionChoice(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	language, extension, replacement := "shell", ".sh", "#!/bin/sh\nprintf replacement\n"
	if runtime.GOOS == "windows" {
		language, extension, replacement = "batch", ".cmd", "@echo replacement\r\n"
	}
	target := filepath.Join(hostRoot, "ops", "inventory"+extension)
	if err := os.WriteFile(target, []byte("original source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(serverURL + "/config/quick-runs/from-source/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	values := url.Values{
		"csrf_token":        {formToken(t, taskPage)},
		"working_directory": {filepath.Join(hostRoot, "ops")},
		"language":          {language},
		"file_name":         {"inventory"},
		"source":            {replacement},
		"name":              {"Replacement"},
	}
	response, err = client.PostForm(serverURL+"/config/quick-runs/from-source", values)
	if err != nil {
		t.Fatal(err)
	}
	conflictPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", response.StatusCode, conflictPage)
	}
	for _, expected := range []string{`name="conflict_action" value="rename"`, `name="conflict_action" value="overwrite"`, `name="rename_file_name" value="inventory (2)"`} {
		if !bytes.Contains(conflictPage, []byte(expected)) {
			t.Fatalf("conflict page is missing %q: %s", expected, conflictPage)
		}
	}
	unchanged, _ := os.ReadFile(target)
	if string(unchanged) != "original source\n" {
		t.Fatalf("source mutated before conflict choice: %q", unchanged)
	}

	values.Set("csrf_token", formToken(t, conflictPage))
	values.Set("conflict_action", "overwrite")
	response, err = client.PostForm(serverURL+"/config/quick-runs/from-source", values)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("overwrite status=%d body=%s", response.StatusCode, body)
	}
	created, _ := os.ReadFile(target)
	if string(created) != replacement {
		t.Fatalf("replacement source = %q, want %q", created, replacement)
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatal(err)
	}
	trashPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(trashPage, []byte(target)) {
		t.Fatalf("overwritten source is not recorded in Trash: %s", trashPage)
	}
}
