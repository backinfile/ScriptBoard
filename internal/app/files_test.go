package app_test

import (
	"bytes"
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
	for _, expected := range []string{"This host", html.EscapeString(hostRoot), `name="path" value="` + html.EscapeString(hostRoot) + `"`} {
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
	for _, expected := range []string{
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
	for _, expected := range []string{`[data-copy-value]`, `navigator.clipboard.writeText(copyButton.dataset.copyValue || "")`} {
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
		`action="/resources/files/upload"`,
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
		`data-file-quick-one-label>folder</span>`,
		`data-file-quick-many-label>folders</span>`,
		"Quick access",
		"Pin directory",
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
}

type fileQuickAccessPinTestView struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Href  string `json:"href"`
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
	for name := range map[string]struct{}{
		"automation.ps1": {},
		"diagram.png":    {},
		"notes.txt":      {},
		"archive.bin":    {},
	} {
		if err := os.WriteFile(filepath.Join(hostRoot, name), []byte(name), 0o644); err != nil {
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
	if response.StatusCode != http.StatusOK || !bytes.Contains(resultPage, []byte("hello.txt")) || !bytes.Contains(resultPage, []byte("Succeeded")) {
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
