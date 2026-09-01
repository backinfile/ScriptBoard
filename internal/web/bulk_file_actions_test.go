package web_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchDownloadStreamsSelectedFilesAndDirectoriesAsZip(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	directory := filepath.Join(hostRoot, "reports")
	if err := os.MkdirAll(filepath.Join(directory, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	standalone := filepath.Join(hostRoot, "notes.txt")
	if err := os.WriteFile(standalone, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", "report.txt"), []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	page, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()

	response, err := client.PostForm(serverURL+"/resources/files/batch-download", url.Values{
		"csrf_token": {formToken(t, body)},
		"path":       {standalone, directory},
	})
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("batch download status=%d body=%s", response.StatusCode, archive)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, "scriptboard-files.zip") {
		t.Fatalf("content disposition = %q", disposition)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	entries := make(map[string]string)
	for _, file := range reader.File {
		opened, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		content, readErr := io.ReadAll(opened)
		_ = opened.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		entries[file.Name] = string(content)
	}
	if entries["notes.txt"] != "notes" || entries["reports/nested/report.txt"] != "report" {
		t.Fatalf("zip entries = %#v", entries)
	}
}

func TestBatchMoveMovesEverySelectionAndStopsBeforeNameConflict(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	source := filepath.Join(hostRoot, "source")
	destination := filepath.Join(hostRoot, "destination")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(source, "first.txt")
	second := filepath.Join(source, "second.txt")
	for path, content := range map[string]string{first: "first", second: "second"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	page, err := client.Get(hostFilesRequestURL(serverURL, source))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	csrf := formToken(t, body)

	response, err := client.PostForm(serverURL+"/resources/files/batch-move", url.Values{
		"csrf_token":        {csrf},
		"working_directory": {destination},
		"path":              {first, second},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("batch move status=%d", response.StatusCode)
	}
	for name := range map[string]bool{"first.txt": true, "second.txt": true} {
		if _, err := os.Stat(filepath.Join(destination, name)); err != nil {
			t.Fatalf("moved file %s missing: %v", name, err)
		}
	}

	conflictSource := filepath.Join(source, "conflict.txt")
	otherSource := filepath.Join(source, "other.txt")
	if err := os.WriteFile(conflictSource, []byte("source conflict"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherSource, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "conflict.txt"), []byte("destination conflict"), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err = client.PostForm(serverURL+"/resources/files/batch-move", url.Values{
		"csrf_token":        {csrf},
		"working_directory": {destination},
		"path":              {conflictSource, otherSource},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting batch move status=%d, want %d", response.StatusCode, http.StatusConflict)
	}
	if _, err := os.Stat(otherSource); err != nil {
		t.Fatalf("non-conflicting source moved before preflight completed: %v", err)
	}
}

func TestBatchTrashMovesEverySelectionAndRejectsNestedDuplicates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	directory := filepath.Join(hostRoot, "directory")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(directory, "child.txt")
	standalone := filepath.Join(hostRoot, "standalone.txt")
	if err := os.WriteFile(child, []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(standalone, []byte("standalone"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	page, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()

	response, err := client.PostForm(serverURL+"/resources/files/batch-delete", url.Values{
		"csrf_token":         {formToken(t, body)},
		"confirm_references": {"yes"},
		"path":               {directory, child, standalone},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("batch trash status=%d", response.StatusCode)
	}
	for _, path := range []string{directory, standalone} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("trashed source still exists: %s (%v)", path, err)
		}
	}

	trash, err := client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatal(err)
	}
	trashBody, _ := io.ReadAll(trash.Body)
	_ = trash.Body.Close()
	if bytes.Count(trashBody, []byte("directory")) == 0 || bytes.Count(trashBody, []byte("standalone.txt")) == 0 {
		t.Fatalf("trash listing missing batch entries: %s", trashBody)
	}
}
