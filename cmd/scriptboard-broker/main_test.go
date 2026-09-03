package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBrokerSecretFileAcceptsOnlyBoundedSingleLineRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay-token")
	if err := os.WriteFile(path, []byte("broker-only-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if token, err := readBrokerSecretFile(path); err != nil || token != "broker-only-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if _, err := readBrokerSecretFile("relative-token"); err == nil {
		t.Fatal("relative token path was accepted")
	}
	if err := os.WriteFile(path, []byte("first\nsecond"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBrokerSecretFile(path); err == nil {
		t.Fatal("multiline token was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4097)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBrokerSecretFile(path); err == nil {
		t.Fatal("oversized token was accepted")
	}
}

func TestProtectBrokerSecretDirectoryRequiresDedicatedRealDirectory(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "broker-secrets")
	if err := os.Mkdir(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "relay-token")
	if err := protectBrokerSecretDirectory(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(root, "secrets")
	if err := os.Mkdir(wrong, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := protectBrokerSecretDirectory(filepath.Join(wrong, "relay-token")); err == nil {
		t.Fatal("shared secrets directory was accepted for a Broker-only relay token")
	}
}

func TestBrokerServiceLogPathRequiresAbsoluteStateRoot(t *testing.T) {
	stateRoot := t.TempDir()
	want := filepath.Join(stateRoot, "logs", "broker.log")
	if got := brokerServiceLogPath([]string{"--config", filepath.Join(stateRoot, "config.yaml"), "--state-root", stateRoot}); got != want {
		t.Fatalf("broker service log path = %q, want %q", got, want)
	}
	if got := brokerServiceLogPath([]string{"--state-root", "relative"}); got != "" {
		t.Fatalf("relative State Root produced Broker service log path %q", got)
	}
}
