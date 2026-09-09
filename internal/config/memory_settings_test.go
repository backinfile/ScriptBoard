package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"scriptboard/internal/config"
	"strings"
	"testing"
)

func TestMemorySettingsPreserveConfigurationAndRejectStaleWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "# deployment comment\nlisten: 127.0.0.1:7777 # retain listener\nrun_memory_limit: 512MiB\n"
	if err := os.WriteFile(path, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	snapshot, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Memory
	next.Total = "8GiB"
	next.PerRun = "2GiB"
	if err := config.SaveMemorySettings(path, snapshot.Revision, next); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for _, keep := range []string{"# deployment comment", "127.0.0.1:7777", "# retain listener"} {
		if !strings.Contains(string(body), keep) {
			t.Fatalf("lost %s", keep)
		}
	}
	loaded, err := config.Load([]string{"--config", path}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Memory != next {
		t.Fatalf("loaded %+v want %+v", loaded.Memory, next)
	}
	if err := config.SaveMemorySettings(path, snapshot.Revision, next); !errors.Is(err, config.ErrMemorySettingsConflict) {
		t.Fatalf("stale write: %v", err)
	}
	current, _ := config.ReadMemorySettings(path)
	bad := next
	bad.Total = "0"
	if err := config.SaveMemorySettings(path, current.Revision, bad); err == nil {
		t.Fatal("accepted zero")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(body) {
		t.Fatal("failed save changed file")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0640 {
			t.Fatal("file permissions changed")
		}
	}
}
func TestMemorySettingsReadRejectsUnsafeDocuments(t *testing.T) {
	for _, body := range []string{"listen: 1\nlisten: 2\n", "listen: a\n---\nlisten: b\n", "- a\n", "runner_memory_limit: 0\n", "unknown_secret: value\n", "runner_memory_limit: &budget 512MiB\nexecutor_chains:\n  .ps1: [*budget]\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte(body), 0600)
		if _, err := config.ReadMemorySettings(path); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
}
func TestMemorySettingsCreatesMissingConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	snapshot, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Memory
	next.PerRun = "unlimited"
	if err := config.SaveMemorySettings(path, snapshot.Revision, next); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", path}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Memory != next {
		t.Fatal("settings did not reload")
	}
}

func TestMemorySettingsConcurrentSavesHaveOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	snapshot, err := config.ReadMemorySettings(path)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, value := range []string{"4GiB", "8GiB"} {
		go func(value string) {
			<-start
			next := snapshot.Memory
			next.Total = value
			results <- config.SaveMemorySettings(path, snapshot.Revision, next)
		}(value)
	}
	close(start)
	success, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, config.ErrMemorySettingsConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
}
