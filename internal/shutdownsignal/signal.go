// Package shutdownsignal provides the platform shutdown signals used by
// ScriptBoard's foreground and service entry points.
package shutdownsignal

import (
	"context"
	"os/signal"
)

func Context(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, platformSignals()...)
}
