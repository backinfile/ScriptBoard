//go:build windows

package runmanager

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func terminateProcess(process *os.Process, force bool) error {
	if !force {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(process.Pid))
	}
	arguments := []string{"/PID", strconv.Itoa(process.Pid), "/T"}
	arguments = append(arguments, "/F")
	if output, err := exec.Command("taskkill.exe", arguments...).CombinedOutput(); err != nil {
		return fmt.Errorf("taskkill: %w: %s", err, output)
	}
	return nil
}
