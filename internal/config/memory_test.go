package config_test

import (
	"os"
	"path/filepath"
	"scriptboard/internal/config"
	"testing"
)

func TestMemoryConfigurationPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("runner_memory_limit: 8GiB\nrun_memory_limit: 512MiB\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", path, "--runner-memory-limit", "unlimited"}, func(key string) string {
		if key == "SCRIPTBOARD_RUNNER_MEMORY_LIMIT" {
			return "16GiB"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Total != "unlimited" || loaded.PerRun != "512MiB" {
		t.Fatalf("%+v", loaded.Memory)
	}
	if _, err := config.Load([]string{"--config", path, "--run-memory-limit", "0"}, func(string) string { return "" }); err == nil {
		t.Fatal("invalid limit accepted")
	}
}
