package web_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/hostfiles"
)

func TestFilesPageUsesAbsoluteHostPathsAndHasNoFileSettings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host & scripts")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host directory: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	filesPage := string(body)
	for _, expected := range []string{"This host", html.EscapeString(hostRoot), `name="path" value="` + html.EscapeString(hostRoot) + `"`, `class="file-search-links"`, `href="/resources/trash"`} {
		if !strings.Contains(filesPage, expected) {
			t.Fatalf("files page does not contain %q: %s", expected, filesPage)
		}
	}

	response, err = client.Get(serverURL + "/settings/files")
	if err != nil {
		t.Fatalf("get file settings: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read file settings: %v", err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("removed file settings status=%d, want 404: %s", response.StatusCode, body)
	}
}

func TestProtectedHostPathIsDeniedBeforeDeferredFilePageRendering(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	for _, deferred := range []bool{false, true} {
		request, err := http.NewRequest(http.MethodGet, hostFilesRequestURL(serverURL, stateRoot), nil)
		if err != nil {
			t.Fatal(err)
		}
		if deferred {
			request.Header.Set("X-ScriptBoard-Navigation", "pjax")
			request.Header.Set("X-ScriptBoard-Data", "shell")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("protected path deferred=%v status=%d, want %d", deferred, response.StatusCode, http.StatusForbidden)
		}
	}
}

func TestFilesPageOffersAnAbsolutePathMoveTask(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	sourceDirectory := filepath.Join(hostRoot, "source")
	destinationDirectory := filepath.Join(hostRoot, "archive")
	if err := os.MkdirAll(sourceDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDirectory, "report.txt")
	if err := os.WriteFile(source, []byte("move me"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURL(serverURL, sourceDirectory))
	if err != nil {
		t.Fatal(err)
	}
	listing, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	moveURL := hostFileHref("/resources/files/move", source)
	if !bytes.Contains(listing, []byte(`href="`+moveURL+`"`)) {
		t.Fatalf("files page does not offer move task %q: %s", moveURL, listing)
	}

	response, err = client.Get(serverURL + moveURL)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!bytes.Contains(task, []byte(`data-task-kind="move-file"`)) ||
		!bytes.Contains(task, []byte(`name="source" value="`+html.EscapeString(source)+`"`)) ||
		!bytes.Contains(task, []byte(`name="working_directory" value="`+html.EscapeString(sourceDirectory)+`"`)) {
		t.Fatalf("move task status=%d body=%s", response.StatusCode, task)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"csrf_token":        {formToken(t, task)},
		"source":            {source},
		"working_directory": {destinationDirectory},
		"name":              {"archived.txt"},
		"conflict_action":   {""},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move submission status=%d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if content, err := os.ReadFile(filepath.Join(destinationDirectory, "archived.txt")); err != nil || string(content) != "move me" {
		t.Fatalf("moved content=%q error=%v", content, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists after move: %v", err)
	}
}

func TestFilesPageOffersRenameDrawersForFilesAndDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	directoryPath := filepath.Join(hostRoot, "reports")
	filePath := filepath.Join(hostRoot, "draft.txt")
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("rename me"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	listing, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	for _, item := range []struct {
		path, name, renamed string
	}{
		{path: filePath, name: "draft.txt", renamed: "final.txt"},
		{path: directoryPath, name: "reports", renamed: "archive"},
	} {
		renameURL := hostFileHref("/resources/files/rename", item.path)
		if !bytes.Contains(listing, []byte(`href="`+renameURL+`" data-task-link`)) {
			t.Fatalf("files page does not offer rename task %q: %s", renameURL, listing)
		}
		response, err = client.Get(serverURL + renameURL)
		if err != nil {
			t.Fatal(err)
		}
		task, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK ||
			!bytes.Contains(task, []byte(`data-task-kind="rename-file"`)) ||
			!bytes.Contains(task, []byte(`name="source" value="`+html.EscapeString(item.path)+`"`)) ||
			!bytes.Contains(task, []byte(`name="working_directory" value="`+html.EscapeString(hostRoot)+`"`)) ||
			!bytes.Contains(task, []byte(`name="name" value="`+item.name+`"`)) ||
			bytes.Contains(task, []byte(`data-directory-picker`)) {
			t.Fatalf("rename task status=%d body=%s", response.StatusCode, task)
		}
		response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
			"csrf_token":        {formToken(t, task)},
			"source":            {item.path},
			"working_directory": {hostRoot},
			"name":              {item.renamed},
			"conflict_action":   {""},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("rename %s status=%d", item.path, response.StatusCode)
		}
		if _, err := os.Stat(filepath.Join(hostRoot, item.renamed)); err != nil {
			t.Fatalf("renamed entry %s is missing: %v", item.renamed, err)
		}
		if _, err := os.Stat(item.path); !os.IsNotExist(err) {
			t.Fatalf("original entry still exists after rename: %s", item.path)
		}
	}
}

func TestFilesPageOffersPlatformPermissionTasksForFilesAndDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	directoryPath := filepath.Join(hostRoot, "reports")
	filePath := filepath.Join(hostRoot, "daily.txt")
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("report"), 0o640); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	listing, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, path := range []string{directoryPath, filePath} {
		permissionURL := hostFileHref("/resources/files/permissions", path)
		if !bytes.Contains(listing, []byte(`href="`+permissionURL+`" data-task-link`)) {
			t.Fatalf("files page does not offer permission task %q: %s", permissionURL, listing)
		}
	}

	permissionURL := hostFileHref("/resources/files/permissions", directoryPath)
	response, err = client.Get(serverURL + permissionURL)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!bytes.Contains(task, []byte(`data-task-kind="file-permissions"`)) ||
		!bytes.Contains(task, []byte(`name="path" value="`+html.EscapeString(directoryPath)+`"`)) ||
		!bytes.Contains(task, []byte(`data-platform="`+runtime.GOOS+`"`)) {
		t.Fatalf("permission task status=%d body=%s", response.StatusCode, task)
	}
	if runtime.GOOS == "windows" {
		if !bytes.Contains(task, []byte(`<details class="permission-owner-editor">`)) {
			t.Fatalf("Windows permission task does not include the owner editor: %s", task)
		}
		if bytes.Contains(task, []byte(`<details class="permission-owner-editor" open`)) {
			t.Fatalf("Windows owner editor must be collapsed by default: %s", task)
		}
		for _, expected := range []string{`name="owner"`, `name="principal"`, `name="inheritance_enabled"`} {
			if !bytes.Contains(task, []byte(expected)) {
				t.Fatalf("Windows permission task does not contain %q: %s", expected, task)
			}
		}
	} else if runtime.GOOS == "linux" {
		for _, expected := range []string{`name="owner_read"`, `name="group_write"`, `name="other_execute"`, `name="recursive"`} {
			if !bytes.Contains(task, []byte(expected)) {
				t.Fatalf("Linux permission task does not contain %q: %s", expected, task)
			}
		}
	}
}

func TestFilesPageMovesDirectoryAndExcludesItsTreeFromDestinationPicker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	source := filepath.Join(hostRoot, "source")
	nested := filepath.Join(source, "nested")
	destination := filepath.Join(hostRoot, "archive")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "report.txt"), []byte("move directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	listing, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	moveURL := hostFileHref("/resources/files/move", source)
	if !bytes.Contains(listing, []byte(`href="`+moveURL+`"`)) {
		t.Fatalf("files page does not offer directory move task %q: %s", moveURL, listing)
	}

	response, err = client.Get(serverURL + moveURL)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!bytes.Contains(task, []byte(`data-task-kind="move-file"`)) ||
		!bytes.Contains(task, []byte(`data-exclude-path="`+html.EscapeString(source)+`"`)) ||
		!bytes.Contains(task, []byte(`>Move folder<`)) {
		t.Fatalf("directory move task status=%d body=%s", response.StatusCode, task)
	}
	response, err = client.Get(serverURL + "/assets/app-v2.js")
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(script, []byte(`endpoint.searchParams.set("exclude", root.dataset.excludePath)`)) {
		t.Fatalf("directory picker does not forward the excluded source tree")
	}

	directoriesURL := serverURL + "/resources/directories?" + url.Values{
		"path":    {hostRoot},
		"exclude": {source},
	}.Encode()
	response, err = client.Get(directoriesURL)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Directories []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"directories"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	for _, directory := range payload.Directories {
		if hostfiles.Contains(source, directory.Path) {
			t.Fatalf("destination picker includes source tree entry %#v", directory)
		}
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"csrf_token":        {formToken(t, task)},
		"source":            {source},
		"working_directory": {destination},
		"name":              {"moved-source"},
		"conflict_action":   {""},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("directory move submission status=%d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	movedFile := filepath.Join(destination, "moved-source", "nested", "report.txt")
	if content, err := os.ReadFile(movedFile); err != nil || string(content) != "move directory" {
		t.Fatalf("moved directory content=%q error=%v", content, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source directory still exists after move: %v", err)
	}
}

func TestFilesPageOffersCopyPathsForDirectoriesAndFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host & scripts")
	directoryPath := filepath.Join(hostRoot, "reports")
	filePath := filepath.Join(hostRoot, "daily report.txt")
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	listing, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(listing)
	parentPath := filepath.Dir(hostRoot)
	for _, expected := range []string{
		`data-file-parent`,
		`href="` + hostFilesHrefWithQuery(parentPath, nil) + `" aria-label="Back to parent folder"`,
		`data-copy-current-path data-copy-value="` + html.EscapeString(hostRoot) + `"`,
		`aria-label="Copy current path"`,
		`data-copy-value="` + html.EscapeString(directoryPath) + `" data-copy-label="Copy path"`,
		`data-copy-value="` + html.EscapeString(hostRoot) + `" data-copy-label="Copy path"`,
		`data-copy-value="` + html.EscapeString(filePath) + `" data-copy-label="Copy full path"`,
		`data-copy-value-label>Copy full path</span>`,
		`data-copied-label="Path copied"`,
		`data-copy-failed-label="Copy failed. Try again."`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("files page does not contain %q: %s", expected, page)
		}
	}

	response, err = client.Get(serverURL + "/assets/app-v2.js")
	if err != nil {
		t.Fatal(err)
	}
	script, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`[data-copy-value]`, `copyTextToClipboard(copyButton.dataset.copyValue || "")`} {
		if !bytes.Contains(script, []byte(expected)) {
			t.Fatalf("interaction script does not contain %q", expected)
		}
	}
}

func TestFilesPageOffersDropUploadForCurrentDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "nested"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, filepath.Join(hostRoot, "nested")))
	if err != nil {
		t.Fatalf("get nested files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read nested files: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`id="file-drop-upload-form"`,
		`class="file-upload-form"`,
		`data-file-drop-form`,
		`data-file-upload-form`,
		`data-file-drop-surface="file-drop-surface"`,
		`action="/resources/files/upload-batch"`,
		`data-active-description="Files are committed together only after the whole batch is validated."`,
		`enctype="multipart/form-data"`,
		`name="path" value="` + html.EscapeString(filepath.Join(hostRoot, "nested")) + `"`,
		`name="conflict_action" value=""`,
		`name="files" type="file" multiple required hidden`,
		`id="file-drop-surface"`,
		`data-file-drop-zone`,
		`class="file-drop-feedback"`,
		`data-file-drop-title`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("files page does not contain %q: %s", expected, page)
		}
	}
	for _, excluded := range []string{`name="replace"`, `Drop files here to upload`, `Choose files`, `<noscript>`} {
		if strings.Contains(page, excluded) {
			t.Fatalf("files page unexpectedly contains %q: %s", excluded, page)
		}
	}
}

func TestFilesPageHidesDotEntriesByDefaultAndPreservesTheVisibilityChoice(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, ".config"), 0o755); err != nil {
		t.Fatalf("create hidden directory: %v", err)
	}
	for name, content := range map[string]string{
		".env":        "SECRET=fixture",
		"visible.txt": "visible",
	} {
		if err := os.WriteFile(filepath.Join(hostRoot, name), []byte(content), 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	page := string(body)
	for _, expected := range []string{`name="show_hidden" value="1"`, "Show hidden files", "visible.txt"} {
		if !strings.Contains(page, expected) {
			t.Fatalf("default files page does not contain %q: %s", expected, page)
		}
	}
	for _, excluded := range []string{".config", ".env", `name="show_hidden" value="1" checked`} {
		if strings.Contains(page, excluded) {
			t.Fatalf("default files page unexpectedly contains %q: %s", excluded, page)
		}
	}

	response, err = client.Get(hostFilesRequestURL(serverURL, hostRoot) + "&show_hidden=1&sort=name&direction=desc")
	if err != nil {
		t.Fatalf("get files with hidden entries: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files with hidden entries: %v", err)
	}
	page = string(body)
	for _, expected := range []string{
		`name="show_hidden" value="1" checked`,
		`.config`,
		`.env`,
		`class="file-hidden-badge"`,
		html.EscapeString(hostFilesHrefWithQuery(filepath.Join(hostRoot, ".config"), url.Values{"direction": {"desc"}, "show_hidden": {"1"}, "sort": {"name"}})),
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("visible-hidden files page does not contain %q: %s", expected, page)
		}
	}
}

func TestFilesPageOffersCollapsedInstanceQuickAccess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "automation"), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`class="file-quick-access"`,
		`data-file-quick-access`,
		`data-file-quick-list`,
		`data-file-pin-path="` + html.EscapeString(filepath.Join(hostRoot, "automation")) + `"`,
		`data-file-pin-label="automation"`,
		`data-file-pin-href="` + html.EscapeString(hostFileHref("/resources/files", filepath.Join(hostRoot, "automation"))) + `"`,
		`data-validation-url="/resources/files/validate"`,
		`data-pins-url="/resources/files/quick-access"`,
		`data-csrf-token="`,
		`data-file-quick-status`,
		`data-file-quick-one-label>item</span>`,
		`data-file-quick-many-label>items</span>`,
		"Quick access",
		"Pin directory",
		`class="icon-button file-pin-button" type="button" hidden data-file-pin data-file-pin-path="` + html.EscapeString(filepath.Join(hostRoot, "notes.txt")) + `"`,
		`data-file-quick-edit-drawer`,
		`data-file-quick-edit-form`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("files page does not contain %q: %s", expected, page)
		}
	}
	if strings.Contains(page, `data-file-quick-access data-validation-url="/resources/files/validate" open`) {
		t.Fatalf("quick access should be collapsed outside a Files tab navigation: %s", page)
	}
	if strings.Contains(page, "Pinned folders are saved") {
		t.Fatalf("Quick access still renders explanatory copy: %s", page)
	}
	if strings.Contains(page, `data-file-pin-action-label`) {
		t.Fatalf("file Pin action should use the same icon-button treatment as directories: %s", page)
	}

	response, err = client.Get(serverURL + "/assets/app-v2.js")
	if err != nil {
		t.Fatal(err)
	}
	script, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`fetch(disclosure.dataset.pinsUrl`,
		`method: "POST"`,
		`localStorage.removeItem(storageKey)`,
		`if (link.hasAttribute("href")) disclosure.open = false`,
		`openFileQuickAccess: mainNavigation && destination.pathname === "/resources/files"`,
		`initFileQuickAccess(document, cleanups, options.openFileQuickAccess === true)`,
		`savePin("rename"`,
		`savePin("reorder"`,
		`item.draggable = true`,
		`focusedRow.scrollIntoView`,
	} {
		if !bytes.Contains(script, []byte(expected)) {
			t.Fatalf("Quick access interaction script does not contain %q", expected)
		}
	}
	if bytes.Contains(script, []byte(`scriptboard.files.quickAccessOpen`)) {
		t.Fatal("Quick access disclosure state should not use browser storage")
	}
}

func TestFileQuickAccessPinsPersistGlobally(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	pinnedPath := filepath.Join(hostRoot, "automation")
	if err := os.MkdirAll(pinnedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	admin, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := admin.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	response, err = admin.PostForm(serverURL+"/resources/files/quick-access", url.Values{
		"csrf_token": {formToken(t, page)},
		"path":       {pinnedPath},
		"pinned":     {"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("pin directory status=%d body=%s", response.StatusCode, body)
	}
	var pinned struct {
		Pins []struct {
			Path, Label, Href string
		} `json:"pins"`
	}
	if err := json.NewDecoder(response.Body).Decode(&pinned); err != nil {
		t.Fatal(err)
	}
	if len(pinned.Pins) != 1 || pinned.Pins[0].Path != pinnedPath || pinned.Pins[0].Label != "automation" || pinned.Pins[0].Href != hostFileHref("/resources/files", pinnedPath) {
		t.Fatalf("persisted Quick access pins = %#v", pinned.Pins)
	}

	response, err = admin.Get(serverURL + "/resources/files/quick-access")
	if err != nil {
		t.Fatal(err)
	}
	pinned.Pins = nil
	if err := json.NewDecoder(response.Body).Decode(&pinned); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(pinned.Pins) != 1 || pinned.Pins[0].Path != pinnedPath {
		t.Fatalf("reloaded Quick access pins = %#v", pinned.Pins)
	}

	operator := createRoleUserClient(t, admin, serverURL, "quick-access-operator", "operator")
	response, err = operator.Get(serverURL + "/resources/files/quick-access")
	if err != nil {
		t.Fatal(err)
	}
	var operatorPins struct {
		Pins []fileQuickAccessPinTestView `json:"pins"`
	}
	if err := json.NewDecoder(response.Body).Decode(&operatorPins); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(operatorPins.Pins) != 1 || operatorPins.Pins[0].Path != pinnedPath {
		t.Fatalf("operator did not receive the global Quick access pins: %#v", operatorPins.Pins)
	}

	response, err = operator.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	operatorPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	response, err = operator.PostForm(serverURL+"/resources/files/quick-access", url.Values{
		"csrf_token": {formToken(t, operatorPage)},
		"path":       {pinnedPath},
		"pinned":     {"false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("operator unpin status=%d body=%s", response.StatusCode, body)
	}
}

type fileQuickAccessPinTestView struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Href  string `json:"href"`
	Kind  string `json:"kind"`
}

func TestFileQuickAccessSupportsFilesLabelsAndOrdering(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	firstPath := filepath.Join(hostRoot, "first.txt")
	secondPath := filepath.Join(hostRoot, "second")
	if err := os.MkdirAll(secondPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstPath, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	token := formToken(t, page)
	for _, path := range []string{firstPath, secondPath} {
		response, err = client.PostForm(serverURL+"/resources/files/quick-access", url.Values{
			"csrf_token": {token}, "action": {"pin"}, "path": {path},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("pin %s status=%d", path, response.StatusCode)
		}
	}

	response, err = client.PostForm(serverURL+"/resources/files/quick-access", url.Values{
		"csrf_token": {token}, "action": {"rename"}, "path": {firstPath}, "label": {"Release notes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("rename file pin status=%d", response.StatusCode)
	}
	order, _ := json.Marshal([]string{secondPath, firstPath})
	response, err = client.PostForm(serverURL+"/resources/files/quick-access", url.Values{
		"csrf_token": {token}, "action": {"reorder"}, "order": {string(order)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Pins []fileQuickAccessPinTestView `json:"pins"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Pins) != 2 || result.Pins[0].Path != secondPath || result.Pins[1].Label != "Release notes" || result.Pins[1].Kind != "file" {
		t.Fatalf("updated Quick access pins = %#v", result.Pins)
	}
	wantHref := "/resources/files?" + url.Values{"path": {hostRoot}, "focus_path": {firstPath}}.Encode()
	if result.Pins[1].Href != wantHref {
		t.Fatalf("file Quick access href=%q, want %q", result.Pins[1].Href, wantHref)
	}
	response, err = client.Get(serverURL + result.Pins[1].Href)
	if err != nil {
		t.Fatal(err)
	}
	focusedPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(focusedPage, []byte(`data-file-focus`)) || !bytes.Contains(focusedPage, []byte(`>first.txt</a>`)) {
		t.Fatalf("file Quick access target was not focused: %s", focusedPage)
	}
}

func TestOperatorGetsReadOnlyFilesWithoutAnUploadDropTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	admin, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	operator := createRoleUserClient(t, admin, serverURL, "files-operator", "operator")

	response, err := operator.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files as operator: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files as operator: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("operator files status=%d, want %d: %s", response.StatusCode, http.StatusOK, page)
	}
	for _, expected := range []string{`data-file-quick-access`, `name="show_hidden" value="1"`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("operator files page does not contain %q: %s", expected, page)
		}
	}
	for _, excluded := range []string{`data-file-drop-form`, `data-file-drop-zone`, `/resources/files/new-directory`, `/resources/files/upload?`, `href="/resources/trash"`} {
		if strings.Contains(page, excluded) {
			t.Fatalf("operator files page exposes write control %q: %s", excluded, page)
		}
	}

	response, err = operator.Get(serverURL + "/settings/files")
	if err != nil {
		t.Fatalf("get file settings as operator: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read file settings as operator: %v", err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("removed file settings status=%d body=%s", response.StatusCode, body)
	}
}

func TestFilesPageSearchesCurrentDirectoryAndHighlightsMatches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "nested"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	for name, content := range map[string]string{
		"Deploy & Verify.ps1": "Write-Output verified",
		"notes.txt":           "deploy belongs in content only",
	} {
		if err := os.WriteFile(filepath.Join(hostRoot, name), []byte(content), 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "nested", "nested-deploy.ps1"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("create nested match: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot) + "&q=%20DEPLOY%20")
	if err != nil {
		t.Fatalf("search files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files search: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`data-file-search`,
		`data-search-input`,
		`value="DEPLOY"`,
		`Found <strong>1</strong> item in this directory`,
		`<mark>Deploy</mark> &amp; Verify.ps1`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("files search does not contain %q: %s", expected, page)
		}
	}
	for _, excluded := range []string{"notes.txt", "nested-deploy.ps1"} {
		if strings.Contains(page, excluded) {
			t.Fatalf("files search unexpectedly contains %q: %s", excluded, page)
		}
	}
}

func TestFilesPageSortsByVisibleTypeAndPreservesSortAcrossDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "workspace"), 0o755); err != nil {
		t.Fatalf("create workspace directory: %v", err)
	}
	for name, content := range map[string][]byte{
		"automation.ps1": []byte("automation.ps1"),
		"diagram.png":    []byte("diagram.png"),
		"notes.txt":      []byte("notes.txt"),
		"archive.bin":    {0x00, 0x01, 0x02, 0x03},
	} {
		if err := os.WriteFile(filepath.Join(hostRoot, name), content, 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot) + "&sort=type&direction=asc&q=auto")
	if err != nil {
		t.Fatalf("get typed file sort: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read typed file sort: %v", err)
	}
	filteredPage := string(body)
	for _, expected := range []string{
		`value="type" selected`,
		`Type · Ascending`,
		`Runnable script`,
		html.EscapeString(hostFilesHrefWithQuery(hostRoot, url.Values{"direction": {"asc"}, "sort": {"type"}})),
	} {
		if !strings.Contains(filteredPage, expected) {
			t.Fatalf("typed file sort does not contain %q: %s", expected, filteredPage)
		}
	}

	response, err = client.Get(hostFilesRequestURL(serverURL, hostRoot) + "&sort=type&direction=asc")
	if err != nil {
		t.Fatalf("get all typed files: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read all typed files: %v", err)
	}
	page := string(body)
	ordered := []string{"workspace", "automation.ps1", "diagram.png", "notes.txt", "archive.bin"}
	last := -1
	for _, name := range ordered {
		position := strings.Index(page, ">"+name+"<")
		if position < 0 {
			t.Fatalf("typed file sort is missing %q: %s", name, page)
		}
		if position <= last {
			t.Fatalf("typed file sort order %v is not preserved: %s", ordered, page)
		}
		last = position
	}
	for _, label := range []string{"Directory", "Runnable script", "Image", "Previewable text", "Other file"} {
		if !strings.Contains(page, label) {
			t.Fatalf("typed file sort is missing label %q: %s", label, page)
		}
	}
	if !strings.Contains(page, `href="`+html.EscapeString(hostFilesHrefWithQuery(filepath.Join(hostRoot, "workspace"), url.Values{"direction": {"asc"}, "sort": {"type"}}))+`"`) {
		t.Fatalf("directory link does not clear the query and preserve sorting: %s", page)
	}
}

func TestFilesPageNormalizesSortAndShowsDedicatedNoResultsState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "visible.txt"), []byte("visible"), 0o644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot) + "&q=missing&sort=unknown&direction=desc&page=9")
	if err != nil {
		t.Fatalf("get empty search result: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read empty search result: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`data-no-search-results`,
		`No filenames match`,
		`<code>missing</code>`,
		`href="` + html.EscapeString(hostFileHref("/resources/files", hostRoot)) + `"`,
		`<option value="" selected>Natural order</option>`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("empty search result does not contain %q: %s", expected, page)
		}
	}
	for _, excluded := range []string{`class="file-table"`, `class="pagination"`, `visible.txt`, `files.unknown`} {
		if strings.Contains(page, excluded) {
			t.Fatalf("empty search result unexpectedly contains %q: %s", excluded, page)
		}
	}
}

func TestFilesPagePaginationPreservesNormalizedSearchState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	for index := 1; index <= 22; index++ {
		name := fmt.Sprintf("report-%02d.txt", index)
		if err := os.WriteFile(filepath.Join(hostRoot, name), []byte(name), 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot) + "&q=%20REPORT%20&sort=name&direction=desc")
	if err != nil {
		t.Fatalf("get paginated file search: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read paginated file search: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`Found <strong>22</strong> items in this directory`,
		`href="` + html.EscapeString(hostFilesHrefWithQuery(hostRoot, url.Values{"direction": {"desc"}, "page": {"2"}, "q": {"REPORT"}, "sort": {"name"}})) + `"`,
		`href="` + html.EscapeString(hostFilesHrefWithQuery(hostRoot, url.Values{"direction": {"desc"}, "sort": {"name"}})) + `"`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("paginated file search does not contain %q: %s", expected, page)
		}
	}
}

func TestFilesPageListsHostEntriesAndHidesReservedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "alpha"), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "zeta.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(hostRoot, ".scriptboard-trash"), 0o755); err != nil {
		t.Fatalf("create trash: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, ".scriptboard-trash", "hidden.txt"), []byte("hidden"), 0o644); err != nil {
		t.Fatalf("create hidden file: %v", err)
	}

	linkCreated := os.Symlink(root, filepath.Join(hostRoot, "outside")) == nil
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("files status = %d, want %d: %s", response.StatusCode, http.StatusOK, page)
	}
	if !strings.Contains(page, "alpha") || !strings.Contains(page, "zeta.txt") {
		t.Fatalf("host entries missing from page: %s", page)
	}
	if strings.Contains(page, ".scriptboard-trash") || strings.Contains(page, "hidden.txt") {
		t.Fatalf("reserved trash leaked into page: %s", page)
	}
	if strings.Index(page, "alpha") > strings.Index(page, "zeta.txt") {
		t.Fatalf("directory is not listed before file: %s", page)
	}
	if linkCreated && (!strings.Contains(page, "outside") || !strings.Contains(page, "Restricted entry")) {
		t.Fatalf("link is not shown as restricted: %s", page)
	}
}

func TestAdminCanBrowseNestedDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "子目录"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "子目录", "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatalf("create nested file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, filepath.Join(hostRoot, "子目录")))
	if err != nil {
		t.Fatalf("browse nested directory: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read nested directory: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "inside.txt") {
		t.Fatalf("nested directory response: status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), `path=`+url.QueryEscape(filepath.Join(hostRoot, "子目录"))) {
		t.Fatalf("nested operations do not preserve current path: %s", body)
	}
}

func TestAdminCanMoveAndRenameHostEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(hostRoot, "source"), 0o755); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(hostRoot, "target"), 0o755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "source", "old.txt"), []byte("move me"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, filepath.Join(hostRoot, "source")))
	if err != nil {
		t.Fatalf("get source directory: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read source directory: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":      {filepath.Join(hostRoot, "source", "old.txt")},
		"destination": {filepath.Join(hostRoot, "target", "new.txt")},
		"csrf_token":  {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("move file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != hostFileHref("/resources/files", filepath.Join(hostRoot, "target")) {
		t.Fatalf("move response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(hostRoot, "source", "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(hostRoot, "target", "new.txt"))
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if string(content) != "move me" {
		t.Fatalf("moved content = %q", content)
	}
}

func TestMoveNameConflictRequiresAChoiceAndKeepsOverwriteRecoverable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	for _, directory := range []string{"source", "target"} {
		if err := os.MkdirAll(filepath.Join(hostRoot, directory), 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	for relative, content := range map[string]string{
		"source/item.txt":      "incoming",
		"target/item.txt":      "current",
		"source/overwrite.txt": "incoming overwrite",
		"target/overwrite.txt": "current overwrite",
	} {
		if err := os.WriteFile(filepath.Join(hostRoot, filepath.FromSlash(relative)), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, filepath.Join(hostRoot, "source")))
	if err != nil {
		t.Fatalf("get source directory: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read source directory: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":      {filepath.Join(hostRoot, "source", "item.txt")},
		"destination": {filepath.Join(hostRoot, "target", "item.txt")},
		"csrf_token":  {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("request move conflict: %v", err)
	}
	conflictPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read move conflict: %v", err)
	}
	if response.StatusCode != http.StatusConflict ||
		!bytes.Contains(conflictPage, []byte(`data-file-conflict`)) ||
		!bytes.Contains(conflictPage, []byte(`value="item (2).txt"`)) {
		t.Fatalf("move conflict response: status=%d body=%s", response.StatusCode, conflictPage)
	}
	if current, err := os.ReadFile(filepath.Join(hostRoot, "target", "item.txt")); err != nil || string(current) != "current" {
		t.Fatalf("move conflict changed current target: content=%q err=%v", current, err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":          {filepath.Join(hostRoot, "source", "item.txt")},
		"destination":     {filepath.Join(hostRoot, "target", "item.txt")},
		"csrf_token":      {formToken(t, conflictPage)},
		"conflict_action": {"rename"},
		"new_name":        {"../escaped.txt"},
	})
	if err != nil {
		t.Fatalf("reject path-like rename: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("path-like rename status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, "source", "item.txt")); err != nil {
		t.Fatalf("path-like rename changed source: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":          {filepath.Join(hostRoot, "source", "item.txt")},
		"destination":     {filepath.Join(hostRoot, "target", "item.txt")},
		"csrf_token":      {formToken(t, conflictPage)},
		"conflict_action": {"rename"},
		"new_name":        {"item-copy.txt"},
	})
	if err != nil {
		t.Fatalf("rename incoming move: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != hostFileHref("/resources/files", filepath.Join(hostRoot, "target")) {
		t.Fatalf("rename move response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if renamed, err := os.ReadFile(filepath.Join(hostRoot, "target", "item-copy.txt")); err != nil || string(renamed) != "incoming" {
		t.Fatalf("renamed move content=%q err=%v", renamed, err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":          {filepath.Join(hostRoot, "source", "overwrite.txt")},
		"destination":     {filepath.Join(hostRoot, "target", "overwrite.txt")},
		"csrf_token":      {formToken(t, page)},
		"conflict_action": {"overwrite"},
	})
	if err != nil {
		t.Fatalf("overwrite move target: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("overwrite move status=%d", response.StatusCode)
	}
	if overwritten, err := os.ReadFile(filepath.Join(hostRoot, "target", "overwrite.txt")); err != nil || string(overwritten) != "incoming overwrite" {
		t.Fatalf("overwrite move content=%q err=%v", overwritten, err)
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash after overwrite move: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash after overwrite move: %v", err)
	}
	if !bytes.Contains(trashPage, []byte(html.EscapeString(filepath.Join(hostRoot, "target", "overwrite.txt")))) {
		t.Fatalf("overwritten move target was not retained in trash: %s", trashPage)
	}
}

func postHostUpload(t *testing.T, client *http.Client, serverURL, csrfToken, directory, name, content, conflictAction string) (int, []byte) {
	return postHostUploadWithQuickRunSync(t, client, serverURL, csrfToken, directory, name, content, conflictAction, false)
}

func postHostUploadWithQuickRunSync(t *testing.T, client *http.Client, serverURL, csrfToken, directory, name, content, conflictAction string, syncQuickRuns bool) (int, []byte) {
	t.Helper()
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	for _, field := range []struct{ name, value string }{
		{name: "csrf_token", value: csrfToken},
		{name: "path", value: directory},
		{name: "conflict_action", value: conflictAction},
	} {
		if err := writer.WriteField(field.name, field.value); err != nil {
			t.Fatalf("write upload field %s: %v", field.name, err)
		}
	}
	if syncQuickRuns {
		if err := writer.WriteField("sync_quick_runs", "1"); err != nil {
			t.Fatalf("write Quick Run synchronization field: %v", err)
		}
	}
	filePart, err := writer.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("create upload part: %v", err)
	}
	if _, err := filePart.Write([]byte(content)); err != nil {
		t.Fatalf("write upload part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close upload body: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/resources/files/upload", &requestBody)
	if err != nil {
		t.Fatalf("create upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read upload response: %v", err)
	}
	return response.StatusCode, body
}

func postHostUploadBatch(t *testing.T, client *http.Client, serverURL, csrfToken, directory, conflictAction string, files map[string]string) (int, []byte) {
	return postHostUploadBatchWithQuickRunSync(t, client, serverURL, csrfToken, directory, conflictAction, files, false)
}

func postHostUploadBatchWithQuickRunSync(t *testing.T, client *http.Client, serverURL, csrfToken, directory, conflictAction string, files map[string]string, syncQuickRuns bool) (int, []byte) {
	t.Helper()
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	for _, field := range []struct{ name, value string }{
		{name: "csrf_token", value: csrfToken},
		{name: "path", value: directory},
		{name: "conflict_action", value: conflictAction},
	} {
		if err := writer.WriteField(field.name, field.value); err != nil {
			t.Fatal(err)
		}
	}
	if syncQuickRuns {
		if err := writer.WriteField("sync_quick_runs", "1"); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/resources/files/upload-batch", &requestBody)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func TestHostUploadBatchCommitsThirteenFilesTogether(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	files := make(map[string]string, 13)
	for index := 1; index <= 13; index++ {
		files[fmt.Sprintf("batch-%02d.txt", index)] = fmt.Sprintf("content-%02d", index)
	}
	status, body := postHostUploadBatch(t, client, serverURL, formToken(t, page), hostRoot, "skip", files)
	if status != http.StatusOK || !bytes.Contains(body, []byte("batch-13.txt")) {
		t.Fatalf("batch response status=%d body=%s", status, body)
	}
	for name, want := range files {
		content, err := os.ReadFile(filepath.Join(hostRoot, name))
		if err != nil || string(content) != want {
			t.Fatalf("uploaded %s content=%q err=%v", name, content, err)
		}
	}
}

func TestHostUploadBatchRejectsEveryFileWhenOneConflicts(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	conflict := filepath.Join(hostRoot, "existing.txt")
	if err := os.WriteFile(conflict, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	status, _ := postHostUploadBatch(t, client, serverURL, formToken(t, page), hostRoot, "skip", map[string]string{
		"new.txt": "new", "existing.txt": "replacement",
	})
	if status != http.StatusConflict {
		t.Fatalf("batch status=%d, want %d", status, http.StatusConflict)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("non-conflicting file was committed from rejected batch: %v", err)
	}
	content, err := os.ReadFile(conflict)
	if err != nil || string(content) != "original" {
		t.Fatalf("conflicting file changed: content=%q err=%v", content, err)
	}
}

func TestOverwritingUploadedScriptCanSynchronizeQuickRunVersions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "managed"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName := "deploy.sh"
	if runtime.GOOS == "windows" {
		scriptName = "deploy.cmd"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	original := "original script"
	if err := os.WriteFile(scriptPath, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	originalDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(original)))
	if _, err := database.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at, locked, script_sha256, revision, updated_at)
		VALUES ('upload-quick', 'Deploy service', ?, ?, '', 30, 1, 1, 1, ?, 4, 1)`, scriptPath, hostfiles.ComparisonKey(scriptPath), originalDigest); err != nil {
		t.Fatal(err)
	}

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	csrfToken := formToken(t, page)
	response, err = client.PostForm(serverURL+"/resources/files/conflicts", url.Values{
		"csrf_token": {csrfToken}, "path": {hostRoot}, "name": {scriptName},
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(preflight, []byte(`"quickRunCount":1`)) || !bytes.Contains(preflight, []byte(`"Deploy service"`)) {
		t.Fatalf("upload preflight did not disclose Quick Run impact: status=%d body=%s", response.StatusCode, preflight)
	}

	withoutSync := "replacement without sync"
	status, body := postHostUpload(t, client, serverURL, csrfToken, hostRoot, scriptName, withoutSync, "overwrite")
	if status != http.StatusOK {
		t.Fatalf("overwrite without synchronization: status=%d body=%s", status, body)
	}
	var digest string
	var revision int64
	if err := database.QueryRow("SELECT script_sha256, revision FROM quick_runs WHERE id = 'upload-quick'").Scan(&digest, &revision); err != nil {
		t.Fatal(err)
	}
	if digest != originalDigest || revision != 4 {
		t.Fatalf("unsynchronized Quick Run changed: digest=%s revision=%d", digest, revision)
	}

	withSync := "replacement with sync"
	status, body = postHostUploadWithQuickRunSync(t, client, serverURL, csrfToken, hostRoot, scriptName, withSync, "overwrite", true)
	if status != http.StatusOK || !bytes.Contains(body, []byte("Quick Run version(s) synchronized")) {
		t.Fatalf("overwrite with synchronization: status=%d body=%s", status, body)
	}
	if err := database.QueryRow("SELECT script_sha256, revision FROM quick_runs WHERE id = 'upload-quick'").Scan(&digest, &revision); err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(withSync)))
	if digest != wantDigest || revision != 5 {
		t.Fatalf("synchronized Quick Run digest=%s revision=%d, want digest=%s revision=5", digest, revision, wantDigest)
	}
}

func TestBatchUploadCanSynchronizeOverwrittenQuickRunScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot, stateRoot := filepath.Join(root, "managed"), filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, original, replacement := "deploy.sh", "#!/bin/sh\nexit 0\n", "#!/bin/sh\necho synchronized\n"
	if runtime.GOOS == "windows" {
		scriptName, original, replacement = "deploy.cmd", "@echo off\r\nexit /b 0\r\n", "@echo off\r\necho synchronized\r\n"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	if err := os.WriteFile(scriptPath, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	database := openExternalTestDatabase(t, filepath.Join(stateRoot, "app.db"))
	originalDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(original)))
	if _, err := database.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, sort_order, created_at, locked, script_sha256, revision, updated_at)
		VALUES ('batch-upload-quick', 'Batch deploy', ?, ?, '', 30, 1, 1, 0, ?, 2, 1)`, scriptPath, hostfiles.ComparisonKey(scriptPath), originalDigest); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	status, body := postHostUploadBatchWithQuickRunSync(t, client, serverURL, formToken(t, page), hostRoot, "overwrite", map[string]string{
		scriptName:  replacement,
		"notes.txt": "same upload batch",
	}, true)
	if status != http.StatusOK {
		t.Fatalf("batch upload with synchronization status=%d body=%s", status, body)
	}
	var digest string
	var revision int64
	if err := database.QueryRow("SELECT script_sha256, revision FROM quick_runs WHERE id = 'batch-upload-quick'").Scan(&digest, &revision); err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(replacement)))
	if digest != wantDigest || revision != 3 {
		t.Fatalf("batch-synchronized Quick Run became invalid: digest=%s revision=%d, want digest=%s revision=3", digest, revision, wantDigest)
	}
}

func TestExecutableHostUploadIsSavedLikeRegularFile(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	content := "Write-Output 'staged'\n"
	status, result := postHostUpload(t, client, serverURL, formToken(t, page), hostRoot, "deploy.ps1", content, "")
	if status != http.StatusOK || !bytes.Contains(result, []byte("Succeeded")) {
		t.Fatalf("executable upload was not saved directly: status=%d body=%s", status, result)
	}
	stored, err := os.ReadFile(filepath.Join(hostRoot, "deploy.ps1"))
	if err != nil || string(stored) != content {
		t.Fatalf("uploaded executable content=%q err=%v", stored, err)
	}
	status, result = postHostUpload(t, client, serverURL, formToken(t, page), hostRoot, "deploy.ps1", "replacement", "overwrite")
	if status != http.StatusOK || !bytes.Contains(result, []byte("Succeeded")) {
		t.Fatalf("direct executable overwrite failed: status=%d body=%s", status, result)
	}
	stored, err = os.ReadFile(filepath.Join(hostRoot, "deploy.ps1"))
	if err != nil || string(stored) != "replacement" {
		t.Fatalf("executable overwrite content=%q err=%v", stored, err)
	}
}

func TestUploadNameConflictDefaultsToSkipAndSupportsRename(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "same.txt"), []byte("current"), 0o644); err != nil {
		t.Fatalf("write current file: %v", err)
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
	csrfToken := formToken(t, page)

	response, err = client.PostForm(serverURL+"/resources/files/conflicts", url.Values{
		"csrf_token": {csrfToken},
		"path":       {hostRoot},
		"name":       {"same.txt", "new.txt"},
	})
	if err != nil {
		t.Fatalf("preflight upload conflicts: %v", err)
	}
	preflight, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read upload conflicts: %v", err)
	}
	if response.StatusCode != http.StatusOK ||
		!bytes.Contains(preflight, []byte(`"name":"same.txt"`)) ||
		!bytes.Contains(preflight, []byte(`"suggested":"same (2).txt"`)) ||
		bytes.Contains(preflight, []byte(`"name":"new.txt"`)) {
		t.Fatalf("upload conflict preflight: status=%d body=%s", response.StatusCode, preflight)
	}
	response, err = client.PostForm(serverURL+"/resources/files/conflicts", url.Values{
		"csrf_token": {csrfToken},
		"path":       {hostRoot},
		"name":       {"../outside.txt"},
	})
	if err != nil {
		t.Fatalf("preflight invalid upload name: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid upload name status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}

	status, result := postHostUpload(t, client, serverURL, csrfToken, hostRoot, "same.txt", "incoming", "")
	if status != http.StatusMultiStatus || !bytes.Contains(result, []byte("Skipped")) {
		t.Fatalf("default conflict result: status=%d body=%s", status, result)
	}
	if current, err := os.ReadFile(filepath.Join(hostRoot, "same.txt")); err != nil || string(current) != "current" {
		t.Fatalf("default conflict changed current file: content=%q err=%v", current, err)
	}

	status, result = postHostUpload(t, client, serverURL, csrfToken, hostRoot, "same.txt", "incoming", "rename")
	if status != http.StatusOK || !bytes.Contains(result, []byte("same (2).txt")) {
		t.Fatalf("rename conflict result: status=%d body=%s", status, result)
	}
	if renamed, err := os.ReadFile(filepath.Join(hostRoot, "same (2).txt")); err != nil || string(renamed) != "incoming" {
		t.Fatalf("renamed upload content=%q err=%v", renamed, err)
	}
	if current, err := os.ReadFile(filepath.Join(hostRoot, "same.txt")); err != nil || string(current) != "current" {
		t.Fatalf("renamed upload changed current file: content=%q err=%v", current, err)
	}
}

func TestAdminCanStreamUploadAFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
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

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	if err := writer.WriteField("csrf_token", formToken(t, page)); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	if err := writer.WriteField("path", hostRoot); err != nil {
		t.Fatalf("write path field: %v", err)
	}
	filePart, err := writer.CreateFormFile("files", "hello.txt")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := filePart.Write([]byte("hello from upload")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, serverURL+"/resources/files/upload", &requestBody)
	if err != nil {
		t.Fatalf("create upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	resultPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read upload response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(resultPage, []byte("hello.txt")) || !bytes.Contains(resultPage, []byte("Succeeded")) ||
		!bytes.Contains(resultPage, []byte(`data-upload-results`)) || !bytes.Contains(resultPage, []byte(`data-upload-results-close`)) {
		t.Fatalf("upload response: status=%d body=%q", response.StatusCode, resultPage)
	}
	content, err := os.ReadFile(filepath.Join(hostRoot, "hello.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(content) != "hello from upload" {
		t.Fatalf("uploaded content = %q", content)
	}

	response, err = client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files before replacement: %v", err)
	}
	page, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files before replacement: %v", err)
	}
	requestBody.Reset()
	writer = multipart.NewWriter(&requestBody)
	for name, value := range map[string]string{
		"csrf_token":      formToken(t, page),
		"path":            hostRoot,
		"conflict_action": "overwrite",
	} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write replacement field %s: %v", name, err)
		}
	}
	filePart, err = writer.CreateFormFile("files", "hello.txt")
	if err != nil {
		t.Fatalf("create replacement file part: %v", err)
	}
	if _, err := filePart.Write([]byte("replacement upload")); err != nil {
		t.Fatalf("write replacement file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close replacement multipart writer: %v", err)
	}
	request, err = http.NewRequest(http.MethodPost, serverURL+"/resources/files/upload", &requestBody)
	if err != nil {
		t.Fatalf("create replacement upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("replace uploaded file: %v", err)
	}
	resultPage, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read replacement response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(resultPage, []byte("Succeeded")) {
		t.Fatalf("replacement response: status=%d body=%q", response.StatusCode, resultPage)
	}
	content, err = os.ReadFile(filepath.Join(hostRoot, "hello.txt"))
	if err != nil {
		t.Fatalf("read replacement file: %v", err)
	}
	if string(content) != "replacement upload" {
		t.Fatalf("replacement content = %q", content)
	}
	matches, err := filepath.Glob(filepath.Join(hostRoot, ".scriptboard-upload-*"))
	if err != nil {
		t.Fatalf("glob upload temporary files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("upload temporary files remain: %v", matches)
	}
}

func TestAdminCanCreateDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/mkdir", url.Values{
		"path":       {hostRoot},
		"name":       {"新目录"},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("create directory: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != hostFileHref("/resources/files", hostRoot) {
		t.Fatalf("create directory response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	info, err := os.Stat(filepath.Join(hostRoot, "新目录"))
	if err != nil {
		t.Fatalf("stat created directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("created path is not a directory")
	}
}

func TestAdminCanDownloadARegularFileWithRange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "report.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	request, err := http.NewRequest(http.MethodGet, hostFileRequestURL(serverURL, "/resources/files/download", filepath.Join(hostRoot, "report.txt")), nil)
	if err != nil {
		t.Fatalf("create download request: %v", err)
	}
	request.Header.Set("Range", "bytes=2-5")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("download file: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if response.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("range response: status=%d body=%q", response.StatusCode, body)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "report.txt") {
		t.Fatalf("content disposition = %q", disposition)
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header")
	}
}

func TestFileDownloadDoesNotReuseAStaleCachedResponseAfterReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	target := filepath.Join(hostRoot, "report.txt")
	lastModified := "Mon, 02 Jan 2006 15:04:05 GMT"
	modTime, err := http.ParseTime(lastModified)
	if err != nil {
		t.Fatalf("parse fixed modification time: %v", err)
	}
	if err := os.WriteFile(target, []byte("old-content"), 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	if err := os.Chtimes(target, modTime, modTime); err != nil {
		t.Fatalf("set original file timestamp: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	downloadURL := hostFileRequestURL(serverURL, "/resources/files/download", target)

	response, err := client.Get(downloadURL)
	if err != nil {
		t.Fatalf("download original file: %v", err)
	}
	originalBody, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || string(originalBody) != "old-content" {
		t.Fatalf("original download: status=%d body=%q err=%v", response.StatusCode, originalBody, readErr)
	}
	if err := os.WriteFile(target, []byte("new-content"), 0o644); err != nil {
		t.Fatalf("replace file contents: %v", err)
	}
	if err := os.Chtimes(target, modTime, modTime); err != nil {
		t.Fatalf("preserve file timestamp: %v", err)
	}

	request, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		t.Fatalf("create conditional download: %v", err)
	}
	request.Header.Set("If-Modified-Since", lastModified)
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("download replacement file: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read replacement download: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "new-content" {
		t.Fatalf("replacement download: status=%d body=%q", response.StatusCode, body)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
}

func TestAdminCanMoveFileToTrashAndRestoreIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "recover.txt"), []byte("recover me"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
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
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {filepath.Join(hostRoot, "recover.txt")},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/trash" {
		t.Fatalf("delete response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(hostRoot, "recover.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file remains at original path: %v", err)
	}

	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	if !strings.Contains(string(trashPage), "recover.txt") {
		t.Fatalf("trash entry missing: %s", trashPage)
	}

	response, err = client.PostForm(serverURL+"/resources/trash/restore", url.Values{
		"id":         {hiddenValue(t, trashPage, "id")},
		"csrf_token": {formToken(t, trashPage)},
	})
	if err != nil {
		t.Fatalf("restore file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != hostFileHref("/resources/files", hostRoot) {
		t.Fatalf("restore response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	content, err := os.ReadFile(filepath.Join(hostRoot, "recover.txt"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != "recover me" {
		t.Fatalf("restored content = %q", content)
	}
}

func TestTrashPageOffersBulkCleanupRetentionChoices(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	for _, expected := range []string{
		`data-trash-cleanup-drawer`,
		`action="/resources/trash/cleanup"`,
		`name="retention" value="all"`,
		`name="retention" value="1d"`,
		`name="retention" value="7d"`,
		`name="retention" value="30d"`,
		`role="dialog"`,
		`aria-modal="true"`,
	} {
		if !bytes.Contains(page, []byte(expected)) {
			t.Fatalf("trash cleanup drawer does not contain %q: %s", expected, page)
		}
	}
}

func TestAdminCanRestoreTrashEntryWhenOriginalPathIsOccupied(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	originalPath := filepath.Join(hostRoot, "recover.txt")
	if err := os.WriteFile(originalPath, []byte("recover me"), 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
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
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {filepath.Join(hostRoot, "recover.txt")},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete response status = %d", response.StatusCode)
	}
	if err := os.WriteFile(originalPath, []byte("new file"), 0o644); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}

	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	response, err = client.PostForm(serverURL+"/resources/trash/restore", url.Values{
		"id":         {hiddenValue(t, trashPage, "id")},
		"csrf_token": {formToken(t, trashPage)},
	})
	if err != nil {
		t.Fatalf("request restore conflict: %v", err)
	}
	conflictPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read restore conflict: %v", err)
	}
	if response.StatusCode != http.StatusConflict ||
		!bytes.Contains(conflictPage, []byte(`data-file-conflict`)) ||
		!bytes.Contains(conflictPage, []byte(`value="overwrite"`)) ||
		!bytes.Contains(conflictPage, []byte(`value="rename"`)) ||
		!bytes.Contains(conflictPage, []byte(`value="recover (2).txt"`)) {
		t.Fatalf("restore conflict response: status=%d body=%s", response.StatusCode, conflictPage)
	}
	current, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("read current file: %v", err)
	}
	if string(current) != "new file" {
		t.Fatalf("current content = %q", current)
	}

	response, err = client.PostForm(serverURL+"/resources/trash/restore", url.Values{
		"id":              {hiddenValue(t, conflictPage, "id")},
		"csrf_token":      {formToken(t, conflictPage)},
		"conflict_action": {"overwrite"},
	})
	if err != nil {
		t.Fatalf("overwrite occupied restore target: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != hostFileHref("/resources/files", hostRoot) {
		t.Fatalf("overwrite restore response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	restored, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(restored) != "recover me" {
		t.Fatalf("restored content = %q", restored)
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash after overwrite restore: %v", err)
	}
	updatedTrashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash after overwrite restore: %v", err)
	}
	if !bytes.Contains(updatedTrashPage, []byte("recover.txt")) {
		t.Fatalf("overwritten current file was not retained in trash: %s", updatedTrashPage)
	}
}

func TestAdminCanPermanentlyPurgeTrashEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "purge.txt"), []byte("delete forever"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {filepath.Join(hostRoot, "purge.txt")},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("trash file: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/trash/purge", url.Values{
		"id":         {hiddenValue(t, trashPage, "id")},
		"confirm":    {"yes"},
		"csrf_token": {formToken(t, trashPage)},
	})
	if err != nil {
		t.Fatalf("purge trash: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/trash" {
		t.Fatalf("purge response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	trashRoot := filepath.Join(hostRoot, ".scriptboard-trash")
	markerCount := 0
	if err := filepath.WalkDir(trashRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != ".scriptboard-owner" {
			t.Fatalf("purged content remains in filesystem trash: %s", path)
		}
		markerCount++
		return nil
	}); err != nil {
		t.Fatalf("read trash directory: %v", err)
	}
	if markerCount != 2 {
		t.Fatalf("filesystem and instance ownership markers are missing: %d", markerCount)
	}
}

func TestAdminCanCleanTrashWhileKeepingTheMostRecentSevenDays(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, name := range []string{"recent.txt", "eight-days-old.txt", "thirty-one-days-old.txt"} {
		path := filepath.Join(hostRoot, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
			"path":       {path},
			"csrf_token": {formToken(t, filesPage)},
		})
		if err != nil {
			t.Fatalf("trash %s: %v", name, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("trash %s status = %d", name, response.StatusCode)
		}
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatalf("open database for fixture timestamps: %v", err)
	}
	defer database.Close()
	now := time.Now().UTC()
	for name, deletedAt := range map[string]time.Time{
		"eight-days-old.txt":      now.Add(-8 * 24 * time.Hour),
		"thirty-one-days-old.txt": now.Add(-31 * 24 * time.Hour),
	} {
		if _, err := database.Exec("UPDATE trash_entries SET deleted_at = ? WHERE original_path = ?", deletedAt.Unix(), filepath.Join(hostRoot, name)); err != nil {
			t.Fatalf("set %s deletion time: %v", name, err)
		}
	}

	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/trash/cleanup", url.Values{
		"csrf_token": {formToken(t, trashPage)},
		"retention":  {"7d"},
		"confirm":    {"yes"},
	})
	if err != nil {
		t.Fatalf("clean trash: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/trash" {
		t.Fatalf("cleanup response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get cleaned trash: %v", err)
	}
	cleanedPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(cleanedPage, []byte("recent.txt")) {
		t.Fatalf("recent entry was not retained: %s", cleanedPage)
	}
	for _, removed := range []string{"eight-days-old.txt", "thirty-one-days-old.txt"} {
		if bytes.Contains(cleanedPage, []byte(removed)) {
			t.Fatalf("expired entry %s remains after cleanup: %s", removed, cleanedPage)
		}
	}
}

func TestAdminCanCleanAllTrashEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	path := filepath.Join(hostRoot, "remove-everything.txt")
	if err := os.WriteFile(path, []byte("remove everything"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	response, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {path},
		"csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("trash file: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/trash/cleanup", url.Values{
		"csrf_token": {formToken(t, trashPage)},
		"retention":  {"all"},
		"confirm":    {"yes"},
	})
	if err != nil {
		t.Fatalf("clean all trash: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("clean all status = %d", response.StatusCode)
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get cleaned trash: %v", err)
	}
	cleanedPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if bytes.Contains(cleanedPage, []byte("remove-everything.txt")) || !bytes.Contains(cleanedPage, []byte(`data-trash-cleanup-drawer`)) || !bytes.Contains(cleanedPage, []byte("Trash is empty.")) {
		t.Fatalf("trash was not emptied through the public interface: %s", cleanedPage)
	}
}

func TestTrashCleanupRejectsUnsupportedRetention(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/trash/cleanup", url.Values{
		"csrf_token": {formToken(t, trashPage)},
		"retention":  {"forever"},
		"confirm":    {"yes"},
	})
	if err != nil {
		t.Fatalf("submit unsupported retention: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported retention status = %d", response.StatusCode)
	}
}

func TestTextEditRejectsAnExternalChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	filePath := filepath.Join(hostRoot, "note.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/edit", filepath.Join(hostRoot, "note.txt")))
	if err != nil {
		t.Fatalf("get editor: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read editor: %v", err)
	}
	if !strings.Contains(string(page), "original") {
		t.Fatalf("editor does not contain original text: %s", page)
	}
	if err := os.WriteFile(filePath, []byte("external change"), 0o644); err != nil {
		t.Fatalf("write external change: %v", err)
	}

	response, err = client.PostForm(hostFileRequestURL(serverURL, "/resources/files/edit", filePath), url.Values{
		"content":    {"my change"},
		"digest":     {hiddenValue(t, page, "digest")},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("save text: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("save status = %d, want %d", response.StatusCode, http.StatusConflict)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file after conflict: %v", err)
	}
	if string(content) != "external change" {
		t.Fatalf("external change was overwritten: %q", content)
	}
}

func TestTextEditAtomicallySavesAndKeepsOldVersionInTrash(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatalf("create host root: %v", err)
	}
	filePath := filepath.Join(hostRoot, "note.txt")
	if err := os.WriteFile(filePath, []byte("before"), 0o640); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/edit", filepath.Join(hostRoot, "note.txt")))
	if err != nil {
		t.Fatalf("get editor: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read editor: %v", err)
	}
	response, err = client.PostForm(hostFileRequestURL(serverURL, "/resources/files/edit", filePath), url.Values{
		"content":    {"after"},
		"digest":     {hiddenValue(t, page, "digest")},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("save text: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("save status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(content) != "after" {
		t.Fatalf("saved content = %q", content)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat saved file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("saved permissions = %o, want 640", info.Mode().Perm())
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	if !strings.Contains(string(trashPage), "note.txt") {
		t.Fatalf("old version missing from trash: %s", trashPage)
	}
}

func TestTextEditPreservesExistingLineEndingStyle(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		original  string
		submitted string
		want      string
	}{
		{
			name:      "LF",
			original:  "#!/bin/sh\necho before\n",
			submitted: "#!/bin/sh\r\necho after\r\n",
			want:      "#!/bin/sh\necho after\n",
		},
		{
			name:      "CRLF",
			original:  "@echo off\r\necho before\r\n",
			submitted: "@echo off\r\necho after\r\n",
			want:      "@echo off\r\necho after\r\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			hostRoot := filepath.Join(root, "managed")
			stateRoot := filepath.Join(root, "state")
			if err := os.MkdirAll(hostRoot, 0o755); err != nil {
				t.Fatalf("create host root: %v", err)
			}
			filePath := filepath.Join(hostRoot, "script.txt")
			if err := os.WriteFile(filePath, []byte(testCase.original), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}
			client, serverURL := authenticatedClient(t, hostRoot, stateRoot)

			response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/edit", filepath.Join(hostRoot, "script.txt")))
			if err != nil {
				t.Fatalf("get editor: %v", err)
			}
			page, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatalf("read editor: %v", err)
			}

			response, err = client.PostForm(hostFileRequestURL(serverURL, "/resources/files/edit", filePath), url.Values{
				"content":    {testCase.submitted},
				"digest":     {hiddenValue(t, page, "digest")},
				"csrf_token": {formToken(t, page)},
			})
			if err != nil {
				t.Fatalf("save text: %v", err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("save status = %d, want %d", response.StatusCode, http.StatusSeeOther)
			}

			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("read saved file: %v", err)
			}
			if string(content) != testCase.want {
				t.Fatalf("saved content = %q, want %q", content, testCase.want)
			}
		})
	}
}
