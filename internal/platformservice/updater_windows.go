//go:build windows

package platformservice

import (
	"context"
	"errors"
	"strings"
	"syscall"

	"scriptboard/internal/processlaunch"
)

func StartUpdater(executable, stateRoot, operationID string) error {
	if executable == "" || stateRoot == "" || operationID == "" || strings.ContainsAny(operationID, "/\\ \t\r\n") {
		return errors.New("invalid Windows updater launch request")
	}
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: executable,
		Arguments: []string{"apply", "--state-root", stateRoot, "--operation", operationID}, Environment: processlaunch.EnvironmentInherit,
	})
	if err != nil {
		return err
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
		HideWindow:    true,
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
