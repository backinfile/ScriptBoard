package pirpc

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"scriptboard/internal/processlaunch"
)

type localProcessLauncher struct{ stateRoot string }

type localManagedProcess struct {
	command    *exec.Cmd
	controller assistantProcessController
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	stderr     io.ReadCloser
}

func newLocalProcessLauncher(stateRoot string) ProcessLauncher {
	return &localProcessLauncher{stateRoot: stateRoot}
}

func NewLocalRuntimeLauncher(stateRoot string) ProcessLauncher {
	return newLocalProcessLauncher(stateRoot)
}

func (launcher *localProcessLauncher) LaunchRuntime(ctx context.Context, request RuntimeLaunchRequest) (ManagedProcess, error) {
	if launcher.stateRoot == "" {
		return nil, fmt.Errorf("local Runtime launcher requires a trusted State Root")
	}
	spec, err := PrepareRuntimeLaunch(launcher.stateRoot, request)
	if err != nil {
		return nil, err
	}
	return launcher.LaunchSpec(ctx, spec)
}

func (launcher *localProcessLauncher) LaunchSpec(ctx context.Context, spec LaunchSpec) (ManagedProcess, error) {
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: ctx, Executable: spec.Executable, Arguments: spec.Args,
		Environment: processlaunch.EnvironmentExact, Env: spec.Env, Directory: spec.Workspace,
	})
	if err != nil {
		return nil, err
	}
	configureAssistantProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Pi stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open Pi stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("open Pi stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start private Pi runtime: %w", err)
	}
	controller, err := attachAssistantProcess(command.Process)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	return &localManagedProcess{command: command, controller: controller, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (process *localManagedProcess) Stdin() io.WriteCloser { return process.stdin }
func (process *localManagedProcess) Stdout() io.ReadCloser { return process.stdout }
func (process *localManagedProcess) Stderr() io.ReadCloser { return process.stderr }
func (process *localManagedProcess) Wait() error           { return process.command.Wait() }
func (process *localManagedProcess) Terminate(force bool) error {
	return process.controller.Terminate(force)
}
func (process *localManagedProcess) Close() error { return process.controller.Close() }
