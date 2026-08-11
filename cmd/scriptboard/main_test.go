package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestHelpDoesNotDocumentRemovedManagedRootShortcuts(t *testing.T) {
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
	for _, removed := range []string{"--here", "--managed-root"} {
		if strings.Contains(string(help), removed) {
			t.Fatalf("help still documents removed option %s:\n%s", removed, help)
		}
	}
}

func TestValidateNetworkConfigurationAllowsConfiguredNonLoopbackHTTP(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8787", "192.168.1.20:8787", "[::]:8787"} {
		t.Run(address, func(t *testing.T) {
			if err := validateNetworkConfiguration(address, "", ""); err != nil {
				t.Fatalf("validateNetworkConfiguration(%q) returned %v", address, err)
			}
		})
	}
}

func TestValidateNetworkConfigurationRejectsMalformedListenAddress(t *testing.T) {
	if err := validateNetworkConfiguration("0.0.0.0", "", ""); err == nil {
		t.Fatal("validateNetworkConfiguration accepted an address without a port")
	}
}
