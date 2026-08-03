//go:build linux

package pirpc

import (
	"os"
	"os/exec"
	"syscall"
)

type unixAssistantProcessController struct{ process *os.Process }

func configureAssistantProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func attachAssistantProcess(process *os.Process) (assistantProcessController, error) {
	return &unixAssistantProcessController{process: process}, nil
}

func (controller *unixAssistantProcessController) Terminate(force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	return syscall.Kill(-controller.process.Pid, signal)
}

func (controller *unixAssistantProcessController) Close() error { return nil }

func mkdirPrivate(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}
