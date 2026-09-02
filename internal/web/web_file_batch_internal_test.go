package web

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBatchArchiveReturnsDestinationFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("report contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("destination unavailable")
	destination := &failingArchiveWriter{remaining: 1, err: want}
	err = writeBatchArchive(destination, []batchArchiveEntry{{path: path, name: "report.txt", info: info}}, func(path string) (io.ReadCloser, error) {
		return os.Open(path)
	})
	if !errors.Is(err, want) {
		t.Fatalf("writeBatchArchive() error = %v, want %v", err, want)
	}
}

type failingArchiveWriter struct {
	remaining int
	err       error
}

func (writer *failingArchiveWriter) Write(value []byte) (int, error) {
	if writer.remaining <= 0 {
		return 0, writer.err
	}
	if len(value) > writer.remaining {
		written := writer.remaining
		writer.remaining = 0
		return written, writer.err
	}
	writer.remaining -= len(value)
	return len(value), nil
}
