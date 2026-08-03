//go:build linux

package runmanager

import (
	"os"
	"os/exec"
	"syscall"
)

func newExecutorCommand(executor executorCandidate, script string, arguments []string) (*exec.Cmd, error) {
	commandArguments := append(append([]string{}, executor.prefix...), script)
	commandArguments = append(commandArguments, arguments...)
	return exec.Command(executor.path, commandArguments...), nil
}

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
