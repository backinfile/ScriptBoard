package config_test

import (
	"os"
	"path/filepath"
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
	if loaded.RunnerIdentityMode != config.RunnerIdentityPrivileged {
		t.Fatalf("default runner identity mode = %q", loaded.RunnerIdentityMode)
	}
	if len(loaded.TrustedProxies) != 0 {
		t.Fatalf("default trusted proxies = %#v, want none", loaded.TrustedProxies)
	}
}

func TestLoadRunnerIdentityModeConfiguration(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("runner_identity_mode: isolated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", configPath}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunnerIdentityMode != config.RunnerIdentityIsolated {
		t.Fatalf("runner identity mode = %q", loaded.RunnerIdentityMode)
	}
	if _, err := config.Load([]string{"--config", configPath, "--runner-identity-mode", "invalid"}, func(string) string { return "" }); err == nil {
		t.Fatal("invalid runner identity mode was accepted")
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

func TestLoadDerivesLoopbackAllowedHostsAndCanonicalURL(t *testing.T) {
	t.Parallel()
	loaded, err := config.Load([]string{"--config", writeEmptyConfig(t)}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CanonicalExternalURL != "http://127.0.0.1:8787" {
		t.Fatalf("canonical URL = %q", loaded.CanonicalExternalURL)
	}
	if len(loaded.AllowedHosts) < 2 {
		t.Fatalf("allowed hosts = %#v", loaded.AllowedHosts)
	}
}

func TestLoadValidatesSecurityEventReceiverBoundary(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		"security_event_endpoint: http://siem.example/events\n",
		"security_event_endpoint: https://user:secret@siem.example/events\n",
		"security_event_token_file: relative-token\n",
		"security_event_allow_private: true\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load([]string{"--config", path}, func(string) string { return "" }); err == nil {
			t.Fatalf("unsafe security event configuration accepted: %s", data)
		}
	}
	tokenPath := filepath.Join(t.TempDir(), "receiver-token")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "security_event_endpoint: https://siem.example/events\nsecurity_event_token_file: " + filepath.ToSlash(tokenPath) + "\nsecurity_event_allow_private: true\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", path}, func(string) string { return "" })
	if err != nil || loaded.SecurityEventEndpoint == "" || loaded.SecurityEventTokenFile != filepath.ToSlash(tokenPath) || !loaded.SecurityEventAllowPrivate {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestLoadValidatesBrokerOwnedEmailRelayBoundary(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		"notification_email_relay_endpoint: http://mail.example/send\nnotification_email_recipient: admin@example.com\n",
		"notification_email_relay_endpoint: https://user:secret@mail.example/send\nnotification_email_recipient: admin@example.com\n",
		"notification_email_relay_endpoint: https://mail.example/send\nnotification_email_recipient: Admin <admin@example.com>\n",
		"notification_email_relay_token_file: relative-token\n",
		"notification_email_relay_allow_private: true\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load([]string{"--config", path}, func(string) string { return "" }); err == nil {
			t.Fatalf("unsafe email relay configuration accepted: %s", data)
		}
	}
	tokenPath := filepath.ToSlash(filepath.Join(t.TempDir(), "broker-secrets", "mail-token"))
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "notification_email_relay_endpoint: https://mail.example/send\nnotification_email_relay_token_file: " + tokenPath + "\nnotification_email_recipient: admin@example.com\nnotification_email_relay_allow_private: true\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", path}, func(string) string { return "" })
	if err != nil || loaded.NotificationEmailRecipient != "admin@example.com" || loaded.NotificationEmailRelayTokenFile != tokenPath || !loaded.NotificationEmailRelayAllowPrivate {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestLoadRequiresExplicitHostsForWildcardListenAndBindsCanonicalHost(t *testing.T) {
	t.Parallel()
	missingHosts := filepath.Join(t.TempDir(), "missing-hosts.yaml")
	if err := os.WriteFile(missingHosts, []byte("listen: 0.0.0.0:8787\ntls_cert: cert.pem\ntls_key: key.pem\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load([]string{"--config", missingHosts}, func(string) string { return "" }); err == nil {
		t.Fatal("wildcard listen without allowed_hosts was accepted")
	}

	wrongCanonical := filepath.Join(t.TempDir(), "wrong-canonical.yaml")
	data := "listen: 0.0.0.0:8787\ntls_cert: cert.pem\ntls_key: key.pem\nallowed_hosts: [panel.example]\ncanonical_external_url: https://evil.example\n"
	if err := os.WriteFile(wrongCanonical, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load([]string{"--config", wrongCanonical}, func(string) string { return "" }); err == nil {
		t.Fatal("canonical URL outside allowed_hosts was accepted")
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

	for _, key := range []string{"managed_root", "git_executable", "admin_password"} {
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

	for _, name := range []string{"SCRIPTBOARD_MANAGED_ROOT", "SCRIPTBOARD_GIT_EXECUTABLE", "SCRIPTBOARD_ADMIN_PASSWORD"} {
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
		{"--config", writeEmptyConfig(t), "--admin-password", "plaintext-secret"},
	} {
		if _, err := config.Load(arguments, func(string) string { return "" }); err == nil {
			t.Fatalf("removed flags were accepted: %v", arguments)
		} else if !strings.Contains(err.Error(), "was removed") {
			t.Fatalf("removed flag error = %q for %v", err, arguments)
		}
	}
}

func TestRemovedPlaintextAdminPasswordPointsToPasswordFile(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("admin_password: plaintext-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load([]string{"--config", configPath}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "admin_password_file") || !strings.Contains(err.Error(), "was removed") {
		t.Fatalf("plaintext password migration error = %q", err)
	}
}

func TestLoadAcceptsAdminPasswordFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	passwordPath := filepath.Join(root, "admin-password")
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(passwordPath, []byte("a-long-password-phrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("admin_password_file: "+passwordPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load([]string{"--config", configPath}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AdminPasswordFile != passwordPath {
		t.Fatalf("password file=%q", loaded.AdminPasswordFile)
	}
}

func TestLoadRejectsRelativeAdminPasswordFile(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("admin_password_file: relative-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load([]string{"--config", configPath}, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative password file error=%q", err)
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
