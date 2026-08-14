package bootstrap

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectOnDemandHostRetriesUntilEndpointIsReady(t *testing.T) {
	starts, attempts := 0, 0
	peerClosed := make(chan struct{})
	connection, err := connectOnDemandHost(context.Background(), func(context.Context) error {
		starts++
		return nil
	}, func(context.Context) (net.Conn, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("endpoint is not ready")
		}
		client, peer := net.Pipe()
		go func() {
			_ = peer.Close()
			close(peerClosed)
		}()
		return client, nil
	}, "test host")
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	<-peerClosed
	if starts != 1 || attempts != 3 {
		t.Fatalf("starts=%d attempts=%d", starts, attempts)
	}
}

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
