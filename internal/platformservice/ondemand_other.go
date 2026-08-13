//go:build !windows

package platformservice

import "context"

// systemd owns socket activation on managed Linux installations.
func EnsureAIRuntimeHostRunning(context.Context) error { return nil }
func EnsureRunnerHostRunning(context.Context) error    { return nil }
