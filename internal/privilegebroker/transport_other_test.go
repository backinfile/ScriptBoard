//go:build !linux && !windows

package privilegebroker

import "testing"

func developmentTransportOptions(t *testing.T) TransportOptions {
	t.Helper()
	t.Skip("privileged Broker transport is supported only on Linux and Windows")
	return TransportOptions{}
}
