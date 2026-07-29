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

func TestLoadHereUsesCurrentDirectoryAsManagedRoot(t *testing.T) {
	t.Parallel()

	currentDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	configPath := writeEmptyConfig(t)
	loaded, err := config.Load([]string{"--config", configPath, "--here"}, func(name string) string {
		if name == "SCRIPTBOARD_MANAGED_ROOT" {
			return "environment-managed-root"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.ManagedRoot != currentDirectory {
		t.Fatalf("managed root = %q, want current directory %q", loaded.ManagedRoot, currentDirectory)
	}
}

func TestLoadRejectsHereWithManagedRoot(t *testing.T) {
	t.Parallel()

	_, err := config.Load([]string{
		"--config", writeEmptyConfig(t),
		"--here",
		"--managed-root", "somewhere-else",
	}, func(string) string { return "" })
	if err == nil {
		t.Fatal("load config succeeded with both --here and --managed-root")
	}
}

func writeEmptyConfig(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}
