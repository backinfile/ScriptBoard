//go:build linux

package runmanager

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func attachProcess(_ *os.Process) (func(), error) { return func() {}, nil }

func terminateProcess(process *os.Process, force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	return syscall.Kill(-process.Pid, signal)
}
