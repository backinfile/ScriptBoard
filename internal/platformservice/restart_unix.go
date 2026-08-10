//go:build !windows

package platformservice

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RequestRestart creates a transient systemd timer outside the ScriptBoard
// service cgroup. The HTTP response can finish before systemd restarts it.
func RequestRestart(delay time.Duration) error {
	if delay < 0 || delay > 30*time.Second {
		return errors.New("invalid service restart delay")
	}
	unit := fmt.Sprintf("scriptboard-restart-%d-%d", os.Getpid(), time.Now().UnixNano())
	output, err := exec.Command(
		"systemd-run", "--quiet", "--collect", "--on-active="+delay.String(), "--unit="+unit,
		"systemctl", "--no-block", "restart", "scriptboard.service",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schedule ScriptBoard service restart: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
