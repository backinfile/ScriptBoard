//go:build linux

package runmanager

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"scriptboard/internal/processlaunch"
)

func newExecutorCommand(executor executorCandidate, script string, arguments []string) (*exec.Cmd, error) {
	commandArguments := append(append([]string{}, executor.prefix...), script)
	commandArguments = append(commandArguments, arguments...)
	return processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: executor.path, Arguments: commandArguments,
		Environment: processlaunch.EnvironmentExact,
	})
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
