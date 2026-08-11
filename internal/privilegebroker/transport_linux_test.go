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
