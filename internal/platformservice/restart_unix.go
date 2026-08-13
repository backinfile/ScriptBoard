//go:build !windows

package platformservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"scriptboard/internal/processlaunch"
)

// RequestRestart creates a transient systemd timer outside the ScriptBoard
// service cgroup. The HTTP response can finish before systemd restarts it.
func RequestRestart(delay time.Duration) error {
	if delay < 0 || delay > 30*time.Second {
		return errors.New("invalid service restart delay")
	}
	unit := fmt.Sprintf("scriptboard-restart-%d-%d", os.Getpid(), time.Now().UnixNano())
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: "systemd-run",
		Arguments: []string{"--quiet", "--collect", "--on-active=" + delay.String(), "--unit=" + unit,
			"systemctl", "--no-block", "restart", "scriptboard.service"},
		Environment: processlaunch.EnvironmentInherit,
	})
	if err != nil {
		return err
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schedule ScriptBoard service restart: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
