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
	"scriptboard/internal/resourcelimits"
	"strings"
)

type localProcessLauncher struct {
	executorChains map[string][]string
	memory         resourcelimits.Memory
}

type localManagedProcess struct {
	command *exec.Cmd
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	cleanup func()
}

func NewLocalProcessLauncher(executorChains map[string][]string, policies ...resourcelimits.Memory) ProcessLauncher {
	memory := resourcelimits.Defaults()
	if len(policies) > 0 {
		memory = policies[0].Resolved()
	}
	return &localProcessLauncher{executorChains: executorChains, memory: memory}
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
	memory := launcher.memory
	if request.MemoryLimit != "" {
		memory.PerRun = request.MemoryLimit
	}
	if err := memory.Validate(); err != nil {
		return nil, "", err
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
		resourceCleanup, resourceErr := prepareMemory(command, memory, request.RunID)
		if resourceErr != nil {
			return nil, "", resourceErr
		}
		stdout, pipeErr := command.StdoutPipe()
		if pipeErr != nil {
			resourceCleanup()
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
			resourceCleanup()
			startErrors = append(startErrors, executor.path+": "+startErr.Error())
			continue
		}
		cleanup, attachErr := attachProcess(command.Process, memory)
		if attachErr != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			resourceCleanup()
			return nil, "", attachErr
		}
		if err := resumeMemoryProcess(command); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			cleanup()
			resourceCleanup()
			return nil, "", err
		}
		combinedCleanup := func() { cleanup(); resourceCleanup() }
		return &localManagedProcess{command: command, stdout: stdout, stderr: stderr, cleanup: combinedCleanup}, executor.path, nil
	}
	return nil, "", fmt.Errorf("all configured executors failed to start: %s", strings.Join(startErrors, "; "))
}

func (process *localManagedProcess) Stdout() io.ReadCloser { return process.stdout }
func (process *localManagedProcess) Stderr() io.ReadCloser { return process.stderr }
func (process *localManagedProcess) Wait() error {
	return memoryWaitError(process.command, process.command.Wait())
}
func (process *localManagedProcess) Terminate(force bool) error {
	return terminateProcess(process.command.Process, force)
}
func (process *localManagedProcess) Close() error {
	if process.cleanup != nil {
		process.cleanup()
	}
	return nil
}
