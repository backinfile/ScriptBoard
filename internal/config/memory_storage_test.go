package config_test

import (
	"os"
	"path/filepath"
	"scriptboard/internal/config"
	"scriptboard/internal/resourcelimits"
	"scriptboard/internal/store/memorysettings"
	"testing"
)

func TestStoredMemorySurvivesDeploymentOverrides(t *testing.T) {
	root := t.TempDir()
	memory := resourcelimits.Defaults()
	memory.Total = "6GiB"
	if _, err := memorysettings.Ensure(root, memory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("runner_memory_limit: 3GiB\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", path, "--state-root", root, "--runner-memory-limit", "4GiB"}, func(key string) string {
		if key == "SCRIPTBOARD_RUNNER_MEMORY_LIMIT" {
			return "5GiB"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Memory != memory {
		t.Fatalf("memory = %+v, want %+v", loaded.Memory, memory)
	}
}
