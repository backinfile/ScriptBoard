package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestHelpDocumentsHereShortcut(t *testing.T) {
	originalStdout := os.Stdout
	output, err := os.CreateTemp(t.TempDir(), "scriptboard-help-*.txt")
	if err != nil {
		t.Fatalf("create help output: %v", err)
	}
	t.Cleanup(func() {
		_ = output.Close()
	})
	os.Stdout = output
	t.Cleanup(func() {
		os.Stdout = originalStdout
	})

	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("show help: %v", err)
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind help output: %v", err)
	}
	help, err := io.ReadAll(output)
	if err != nil {
		t.Fatalf("read help output: %v", err)
	}
	if !strings.Contains(string(help), "--here") {
		t.Fatalf("help does not document --here:\n%s", help)
	}
}
