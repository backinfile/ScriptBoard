//go:build !windows

package shutdownsignal

import (
	"syscall"
	"testing"
)

func TestPlatformSignalsIncludeSystemdTermination(t *testing.T) {
	for _, current := range platformSignals() {
		if current == syscall.SIGTERM {
			return
		}
	}
	t.Fatal("SIGTERM is not handled by service entry points")
}
