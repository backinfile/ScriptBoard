package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scriptboard/internal/config"
)

func TestLoadUsesMinimalInstallationDefaults(t *testing.T) {
	t.Parallel()

	loaded, err := config.Load([]string{"--config", writeEmptyConfig(t)}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StateRoot == "" {
		t.Fatal("default state root is empty")
	}
	if loaded.Listen != "127.0.0.1:8787" {
		t.Fatalf("default listen = %q", loaded.Listen)
	}
	if want := []string{"127.0.0.1/32"}; !reflect.DeepEqual(loaded.TrustedProxies, want) {
		t.Fatalf("default trusted proxies = %#v, want %#v", loaded.TrustedProxies, want)
	}
}

func TestLoadAllowsExplicitlyClearingDefaultTrustedProxies(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("trusted_proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", configPath}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.TrustedProxies) != 0 {
		t.Fatalf("explicitly cleared trusted proxies = %#v", loaded.TrustedProxies)
	}
}

func TestLoadLayersYAMLThenEnvironmentThenCLI(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte("state_root: yaml-state\nlisten: 127.0.0.1:9000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	environment := map[string]string{"SCRIPTBOARD_STATE_ROOT": "env-state", "SCRIPTBOARD_LISTEN": "127.0.0.1:9100"}
	loaded, err := config.Load([]string{
		"--config", configPath,
		"--state-root", "cli-state",
	}, func(name string) string { return environment[name] })
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.StateRoot != "cli-state" {
		t.Fatalf("state root = %q", loaded.StateRoot)
	}
	if loaded.Listen != "127.0.0.1:9100" {
		t.Fatalf("listen = %q", loaded.Listen)
	}
}

func TestLoadRejectsRemovedManagedRootConfiguration(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("managed_root: old-managed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load([]string{"--config", configPath}, func(string) string { return "" }); err == nil {
		t.Fatal("legacy managed_root configuration was accepted")
	} else if !strings.Contains(err.Error(), "managed_root was removed") {
		t.Fatalf("legacy managed_root error = %q", err)
	}
}

func TestLoadRejectsRemovedGitExecutableConfiguration(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("git_executable: git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load([]string{"--config", configPath}, func(string) string { return "" }); err == nil {
		t.Fatal("legacy git_executable configuration was accepted")
	} else if !strings.Contains(err.Error(), "git_executable was removed") {
		t.Fatalf("legacy git_executable error = %q", err)
	}
}

func TestLoadRejectsRemovedConfigurationKeysEvenWhenNull(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"managed_root", "git_executable"} {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(configPath, []byte(key+":\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load([]string{"--config", configPath}, func(string) string { return "" }); err == nil {
				t.Fatalf("null legacy %s configuration was accepted", key)
			} else if !strings.Contains(err.Error(), key+" was removed") {
				t.Fatalf("null legacy %s error = %q", key, err)
			}
		})
	}
}

func TestLoadRejectsRemovedEnvironmentVariables(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"SCRIPTBOARD_MANAGED_ROOT", "SCRIPTBOARD_GIT_EXECUTABLE"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Load([]string{"--config", writeEmptyConfig(t)}, func(candidate string) string {
				if candidate == name {
					return "legacy"
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), name+" was removed") {
				t.Fatalf("legacy environment error = %q", err)
			}
		})
	}
}

func TestLoadRejectsRemovedFileFlags(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"--config", writeEmptyConfig(t), "--here"},
		{"--config", writeEmptyConfig(t), "--managed-root", "somewhere"},
		{"--config", writeEmptyConfig(t), "--git-executable", "git"},
	} {
		if _, err := config.Load(arguments, func(string) string { return "" }); err == nil {
			t.Fatalf("removed flags were accepted: %v", arguments)
		} else if !strings.Contains(err.Error(), "was removed") {
			t.Fatalf("removed flag error = %q for %v", err, arguments)
		}
	}
}

func TestLoadUpdateCheckConfiguration(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("update_check: false\nupdate_check_interval_hours: 12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", configPath}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UpdateCheck || loaded.UpdateInterval.Hours() != 12 {
		t.Fatalf("update check=%v interval=%s", loaded.UpdateCheck, loaded.UpdateInterval)
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
