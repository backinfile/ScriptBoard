package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSecurityEventTokenIsBoundedAndTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  fixture-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readSecurityEventToken(path)
	if err != nil || value != "fixture-token" {
		t.Fatalf("token=%q err=%v", value, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4097)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecurityEventToken(path); err == nil {
		t.Fatal("oversized security event token accepted")
	}
}

func TestValidateNetworkConfigurationRequiresCompleteTLSAndHostPort(t *testing.T) {
	for _, input := range [][3]string{{"127.0.0.1", "", ""}, {"127.0.0.1:8787", "cert.pem", ""}} {
		if err := validateNetworkConfiguration(input[0], input[1], input[2]); err == nil {
			t.Fatalf("invalid network configuration accepted: %v", input)
		}
	}
	if err := validateNetworkConfiguration("127.0.0.1:8787", "", ""); err != nil {
		t.Fatal(err)
	}
}
