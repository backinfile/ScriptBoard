//go:build windows

package platformservice

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// RequestRestart starts an independent helper so the service never waits for
// its own SCM stop operation from inside the service process.
func RequestRestart(delay time.Duration) error {
	if delay < 0 || delay > 30*time.Second {
		return errors.New("invalid service restart delay")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "service", "restart", "--delay", delay.String())
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
		HideWindow:    true,
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
