package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"scriptboard/internal/config"
)

func TestLoadLayersYAMLThenEnvironmentThenCLI(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte("managed_root: yaml-managed\nstate_root: yaml-state\nlisten: 127.0.0.1:9000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	environment := map[string]string{"SCRIPTBOARD_STATE_ROOT": "env-state"}
	loaded, err := config.Load([]string{
		"--config", configPath,
		"--managed-root", "cli-managed",
	}, func(name string) string { return environment[name] })
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.ManagedRoot != "cli-managed" {
		t.Fatalf("managed root = %q", loaded.ManagedRoot)
	}
	if loaded.StateRoot != "env-state" {
		t.Fatalf("state root = %q", loaded.StateRoot)
	}
	if loaded.Listen != "127.0.0.1:9000" {
		t.Fatalf("listen = %q", loaded.Listen)
	}
}
