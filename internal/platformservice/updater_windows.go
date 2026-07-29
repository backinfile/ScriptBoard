//go:build windows

package platformservice

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
)

func StartUpdater(executable, stateRoot, operationID string) error {
	if executable == "" || stateRoot == "" || operationID == "" || strings.ContainsAny(operationID, "/\\ \t\r\n") {
		return errors.New("invalid Windows updater launch request")
	}
	command := exec.Command(executable, "apply", "--state-root", stateRoot, "--operation", operationID)
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
		HideWindow:    true,
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
