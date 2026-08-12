//go:build linux

package privilegebroker

import (
	"path/filepath"
	"testing"
)

func developmentTransportOptions(t *testing.T) TransportOptions {
	t.Helper()
	return TransportOptions{StateRoot: t.TempDir(), Endpoint: filepath.Join(t.TempDir(), "broker.sock"), DevelopmentCurrentUser: true}
}

func TestStaleSocketOwnerAcceptsServiceAndAuthorizedWebIdentity(t *testing.T) {
	if !validStaleSocketOwner(0, 0, 1200) {
		t.Fatal("service-owned stale socket was rejected")
	}
	if !validStaleSocketOwner(1200, 0, 1200) {
		t.Fatal("authorized Web-owned stale socket was rejected")
	}
	if validStaleSocketOwner(1300, 0, 1200) {
		t.Fatal("unrelated stale socket owner was accepted")
	}
}
