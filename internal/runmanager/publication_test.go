package runmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartRejectsScriptThatNoLongerMatchesPublishedDigest(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "published.cmd")
	if err := os.WriteFile(scriptPath, []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := New(nil, testHostFiles(t), root, 0, nil)
	_, err := manager.Start(StartRequest{ScriptPath: scriptPath, ExpectedDigest: strings.Repeat("0", 64)})
	if err == nil {
		t.Fatal("changed published script was accepted")
	}
}

func TestStartSerializesPublishedRunOverlapCheck(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "published.cmd")
	if err := os.WriteFile(scriptPath, []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := New(nil, testHostFiles(t), root, 0, nil)
	prepared, err := manager.files.PrepareScript(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	manager.active["already-running"] = &activeRun{scriptPath: normalizeHostPath(prepared.Path), fileInfo: prepared.Info}
	_, err = manager.Start(StartRequest{ScriptPath: prepared.Path, ExpectedDigest: prepared.Digest, DisallowOverlap: true})
	if !errors.Is(err, ErrRunOverlap) {
		t.Fatalf("overlapping published Run error = %v", err)
	}
}
