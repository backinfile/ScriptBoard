//go:build windows

package privilegebroker

import "testing"

func developmentTransportOptions(t *testing.T) TransportOptions {
	t.Helper()
	return TransportOptions{StateRoot: t.TempDir(), DevelopmentCurrentUser: true}
}
