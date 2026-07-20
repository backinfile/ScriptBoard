package app_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFilesPageListsManagedEntriesAndHidesReservedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(managedRoot, "alpha"), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "zeta.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(managedRoot, ".scriptboard-trash"), 0o755); err != nil {
		t.Fatalf("create trash: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, ".scriptboard-trash", "hidden.txt"), []byte("hidden"), 0o644); err != nil {
		t.Fatalf("create hidden file: %v", err)
	}

	linkCreated := os.Symlink(root, filepath.Join(managedRoot, "outside")) == nil
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/files/")
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
		t.Fatalf("managed entries missing from page: %s", page)
	}
	if strings.Contains(page, ".scriptboard-trash") || strings.Contains(page, "hidden.txt") {
		t.Fatalf("reserved trash leaked into page: %s", page)
	}
	if strings.Index(page, "alpha") > strings.Index(page, "zeta.txt") {
		t.Fatalf("directory is not listed before file: %s", page)
	}
	if linkCreated && (!strings.Contains(page, "outside") || !strings.Contains(page, "受限")) {
		t.Fatalf("link is not shown as restricted: %s", page)
	}
}

func TestAdminCanStreamUploadAFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
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

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	if err := writer.WriteField("csrf_token", formToken(t, page)); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	if err := writer.WriteField("path", ""); err != nil {
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

	request, err := http.NewRequest(http.MethodPost, serverURL+"/files/upload", &requestBody)
	if err != nil {
		t.Fatalf("create upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/files/" {
		t.Fatalf("upload response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	content, err := os.ReadFile(filepath.Join(managedRoot, "hello.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(content) != "hello from upload" {
		t.Fatalf("uploaded content = %q", content)
	}
	matches, err := filepath.Glob(filepath.Join(managedRoot, ".scriptboard-upload-*"))
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
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}

	response, err = client.PostForm(serverURL+"/files/mkdir", url.Values{
		"path":       {""},
		"name":       {"新目录"},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("create directory: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/files/" {
		t.Fatalf("create directory response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	info, err := os.Stat(filepath.Join(managedRoot, "新目录"))
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
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "report.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	request, err := http.NewRequest(http.MethodGet, serverURL+"/files/download/report.txt", nil)
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

func TestAdminCanMoveFileToTrashAndRestoreIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "recover.txt"), []byte("recover me"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
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
	response, err = client.PostForm(serverURL+"/files/delete", url.Values{
		"path":       {"recover.txt"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/trash" {
		t.Fatalf("delete response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(managedRoot, "recover.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file remains at original path: %v", err)
	}

	response, err = client.Get(serverURL + "/trash")
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

	response, err = client.PostForm(serverURL+"/trash/restore", url.Values{
		"id":         {hiddenValue(t, trashPage, "id")},
		"csrf_token": {formToken(t, trashPage)},
	})
	if err != nil {
		t.Fatalf("restore file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/files/" {
		t.Fatalf("restore response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	content, err := os.ReadFile(filepath.Join(managedRoot, "recover.txt"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != "recover me" {
		t.Fatalf("restored content = %q", content)
	}
}

func TestTextEditRejectsAnExternalChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	filePath := filepath.Join(managedRoot, "note.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/files/edit/note.txt")
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

	response, err = client.PostForm(serverURL+"/files/edit/note.txt", url.Values{
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
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	filePath := filepath.Join(managedRoot, "note.txt")
	if err := os.WriteFile(filePath, []byte("before"), 0o640); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/files/edit/note.txt")
	if err != nil {
		t.Fatalf("get editor: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read editor: %v", err)
	}
	response, err = client.PostForm(serverURL+"/files/edit/note.txt", url.Values{
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
	response, err = client.Get(serverURL + "/trash")
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
