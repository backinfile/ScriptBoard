package hostfiles_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

type failingUploadReader struct {
	read bool
}

type callbackReader struct {
	reader   io.Reader
	callback func()
	called   bool
}

func (reader *callbackReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if errors.Is(err, io.EOF) && !reader.called {
		reader.called = true
		reader.callback()
	}
	return count, err
}

func (reader *failingUploadReader) Read(buffer []byte) (int, error) {
	if !reader.read {
		reader.read = true
		return copy(buffer, "partial"), nil
	}
	return 0, errors.New("source disconnected")
}

func TestUploadBatchLeavesEveryTargetUnchangedWhenStagingFails(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "uploads")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(directory, "first.txt")
	if err := os.WriteFile(first, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "batch-upload-test", Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.UploadBatch(directory, []hostfiles.UploadBatchInput{
		{Name: "first.txt", Source: strings.NewReader("replacement"), MaxBytes: 1 << 20, StoredName: "old-first"},
		{Name: "second.txt", Source: &failingUploadReader{}, MaxBytes: 1 << 20, StoredName: "old-second"},
	}, true)
	if err == nil {
		t.Fatal("expected batch upload to fail")
	}
	content, readErr := os.ReadFile(first)
	if readErr != nil || string(content) != "original" {
		t.Fatalf("first target changed after failed batch: content=%q err=%v", content, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "second.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("second target exists after failed batch: %v", statErr)
	}
	entries, readDirErr := os.ReadDir(directory)
	if readDirErr != nil {
		t.Fatal(readDirErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".scriptboard-upload-") {
			t.Fatalf("temporary upload leaked: %s", entry.Name())
		}
	}
}

var _ io.Reader = (*failingUploadReader)(nil)

func TestUploadBatchRollsBackEarlierCommitsWhenALaterCommitFails(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "uploads")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(directory, "first.txt")
	second := filepath.Join(directory, "second.txt")
	if err := os.WriteFile(first, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "batch-rollback-test", Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	interfering := &callbackReader{reader: strings.NewReader("second"), callback: func() {
		if err := os.Mkdir(second, 0o755); err != nil {
			t.Errorf("create commit conflict: %v", err)
		}
	}}
	_, err = manager.UploadBatch(directory, []hostfiles.UploadBatchInput{
		{Name: "first.txt", Source: strings.NewReader("replacement"), MaxBytes: 1024, StoredName: "old-first"},
		{Name: "second.txt", Source: interfering, MaxBytes: 1024, StoredName: "old-second"},
	}, true)
	if err == nil {
		t.Fatal("expected later commit to fail")
	}
	content, readErr := os.ReadFile(first)
	if readErr != nil || string(content) != "original" {
		t.Fatalf("earlier replacement was not rolled back: content=%q err=%v", content, readErr)
	}
}
