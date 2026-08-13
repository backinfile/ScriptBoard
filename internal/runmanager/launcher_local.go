package runmanager

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

type localProcessLauncher struct{ executorChains map[string][]string }

type localManagedProcess struct {
	command *exec.Cmd
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	cleanup func()
}

func NewLocalProcessLauncher(executorChains map[string][]string) ProcessLauncher {
	return &localProcessLauncher{executorChains: executorChains}
}

func (launcher *localProcessLauncher) RuntimeIdentity() string {
	if current, err := user.Current(); err == nil {
		return current.Username
	}
	return "unknown"
}

func (launcher *localProcessLauncher) Launch(_ context.Context, request LaunchRequest) (ManagedProcess, string, error) {
	if request.RunID == "" || strings.ContainsAny(request.RunID, "\x00\r\n") {
		return nil, "", errors.New("Runner job ID is invalid")
	}
	script, err := os.Open(request.ScriptPath)
	if err != nil {
		return nil, "", fmt.Errorf("open Runner script: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(script, 1<<30))
	closeErr := script.Close()
	if copyErr != nil || closeErr != nil || subtle.ConstantTimeCompare([]byte(fmt.Sprintf("%x", hash.Sum(nil))), []byte(request.ScriptDigest)) != 1 {
		return nil, "", errors.New("Runner script digest changed before execution")
	}
	working, err := os.Lstat(request.WorkingDirectory)
	if err != nil || !working.IsDir() || working.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("Runner working directory is unsafe")
	}
	executors, err := resolveExecutors(filepath.Ext(request.ScriptPath), launcher.executorChains)
	if err != nil {
		return nil, "", err
	}
	var startErrors []string
	for _, executor := range executors {
		command, commandErr := newExecutorCommand(executor, request.ScriptPath, request.Arguments)
		if commandErr != nil {
			startErrors = append(startErrors, executor.path+": "+commandErr.Error())
			continue
		}
		command.Dir = request.WorkingDirectory
		command.Env = runEnvironment(request.RunID, request.ScriptPath)
		configureProcess(command)
		stdout, pipeErr := command.StdoutPipe()
		if pipeErr != nil {
			startErrors = append(startErrors, executor.path+": "+pipeErr.Error())
			continue
		}
		stderr, pipeErr := command.StderrPipe()
		if pipeErr != nil {
			_ = stdout.Close()
			startErrors = append(startErrors, executor.path+": "+pipeErr.Error())
			continue
		}
		if startErr := command.Start(); startErr != nil {
			_ = stdout.Close()
			_ = stderr.Close()
			startErrors = append(startErrors, executor.path+": "+startErr.Error())
			continue
		}
		cleanup, attachErr := attachProcess(command.Process)
		if attachErr != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return nil, "", attachErr
		}
		return &localManagedProcess{command: command, stdout: stdout, stderr: stderr, cleanup: cleanup}, executor.path, nil
	}
	return nil, "", fmt.Errorf("all configured executors failed to start: %s", strings.Join(startErrors, "; "))
}

func (process *localManagedProcess) Stdout() io.ReadCloser { return process.stdout }
func (process *localManagedProcess) Stderr() io.ReadCloser { return process.stderr }
func (process *localManagedProcess) Wait() error           { return process.command.Wait() }
func (process *localManagedProcess) Terminate(force bool) error {
	return terminateProcess(process.command.Process, force)
}
func (process *localManagedProcess) Close() error {
	if process.cleanup != nil {
		process.cleanup()
	}
	return nil
}
