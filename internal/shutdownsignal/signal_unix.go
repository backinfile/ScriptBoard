//go:build !windows

package shutdownsignal

import (
	"os"
	"syscall"
)

func platformSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
