package runmanager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"scriptboard/internal/flowbuiltin"
	"scriptboard/internal/processlaunch"
	"scriptboard/internal/resourcelimits"
	"strings"
	"sync"
)

// Builtin jobs cross the same authenticated Runner transport and digest check as scripts.
type BuiltinJob struct {
	Uses string
	With map[string]string
}
type builtinProcess struct {
	stdout, stderr *io.PipeReader
	cancel         context.CancelFunc
	done           chan struct{}
	err            error
	once           sync.Once
}

func launchBuiltin(request LaunchRequest, memory resourcelimits.Memory) (ManagedProcess, string, error) {
	data, err := os.ReadFile(request.ScriptPath)
	if err != nil {
		return nil, "", err
	}
	if len(data) > 1<<20 {
		return nil, "", fmt.Errorf("builtin job too large")
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != request.ScriptDigest {
		return nil, "", fmt.Errorf("builtin job digest changed")
	}
	var job BuiltinJob
	if err = json.Unmarshal(data, &job); err != nil {
		return nil, "", err
	}
	if _, ok := flowbuiltin.LookupBuiltinNode(job.Uses); !ok {
		return nil, "", fmt.Errorf("unknown builtin")
	}
	// Relative file operations stay in the card workspace; absolute host paths require a published script.
	runner := flowbuiltin.NewBuiltinRunner(flowbuiltin.BuiltinRunnerOptions{WorkspaceRoot: filepath.Dir(request.WorkingDirectory), RunCommand: func(ctx context.Context, path string, args []string) ([]byte, error) {
		return runBuiltinCommand(ctx, request, memory, path, args)
	}})
	ctx, cancel := context.WithCancel(context.Background())
	out, writer := io.Pipe()
	stderr, errors := io.Pipe()
	p := &builtinProcess{stdout: out, stderr: stderr, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		defer writer.Close()
		defer errors.Close()
		message, runErr := runner.Run(ctx, filepath.Base(request.WorkingDirectory), job.Uses, job.With)
		p.err = runErr
		if runErr != nil {
			fmt.Fprintln(errors, runErr)
		} else {
			fmt.Fprintln(writer, message)
		}
	}()
	return p, "scriptboard-builtin", nil
}
func (p *builtinProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *builtinProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *builtinProcess) Wait() error           { <-p.done; return p.err }
func (p *builtinProcess) Terminate(bool) error  { p.cancel(); return nil }
func (p *builtinProcess) Close() error {
	p.once.Do(func() { p.cancel(); p.stdout.Close(); p.stderr.Close() })
	return nil
}
func PrepareFlowWorkspace(root, id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return "", fmt.Errorf("invalid workspace ID")
	}
	path := filepath.Join(root, id)
	if err := os.MkdirAll(path, 0750); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("unsafe flow workspace")
	}
	if err := grantFlowWorkspace(path); err != nil {
		return "", err
	}
	return path, nil
}

// Git children receive the same environment, memory and process-tree lifecycle as scripts.
func runBuiltinCommand(ctx context.Context, request LaunchRequest, memory resourcelimits.Memory, path string, args []string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trusted, err := validateExecutorTrust(path)
	if err != nil {
		return nil, err
	}
	command, err := processlaunch.Prepare(processlaunch.Spec{Context: context.Background(), Executable: trusted, Arguments: args, Directory: request.WorkingDirectory, Environment: processlaunch.EnvironmentExact, Env: runEnvironment(request.RunID, request.ScriptPath, request.ExtraEnv)})
	if err != nil {
		return nil, err
	}
	configureProcess(command)
	resourceCleanup, err := prepareMemory(command, memory, request.RunID)
	if err != nil {
		return nil, err
	}
	defer resourceCleanup()
	var output builtinCommandOutput
	command.Stdout, command.Stderr = &output, &output
	if err = command.Start(); err != nil {
		return nil, err
	}
	cleanup, err := attachProcess(command.Process, memory)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	defer cleanup()
	if err = resumeMemoryProcess(command); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = terminateProcess(command.Process, true)
		case <-done:
		}
	}()
	err = memoryWaitError(command, command.Wait())
	close(done)
	return []byte(output.text), err
}

type builtinCommandOutput struct{ text string }

func (b *builtinCommandOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - len(b.text); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.text += string(p)
	}
	return n, nil
}
