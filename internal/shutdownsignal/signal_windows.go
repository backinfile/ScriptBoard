//go:build windows

package shutdownsignal

import "os"

func platformSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
