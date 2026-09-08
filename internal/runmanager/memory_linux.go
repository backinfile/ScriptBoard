//go:build linux

package runmanager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"scriptboard/internal/resourcelimits"
	"strconv"
	"strings"
	"sync"
	"time"
)

var memoryGroupMu sync.Mutex
var memoryGroupRoot string

func prepareMemory(command *exec.Cmd, memory resourcelimits.Memory, id string) (func(), error) {
	memoryGroupMu.Lock()
	defer memoryGroupMu.Unlock()
	if memory.PerRun == "unlimited" && memoryGroupRoot == "" {
		return func() {}, nil
	}
	root, err := runnerMemoryRoot()
	if err != nil {
		return nil, fmt.Errorf("per-run memory requires a delegated cgroup v2 memory controller: %w", err)
	}
	// MkdirTemp avoids trusting request IDs as filesystem paths.
	group, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = os.WriteFile(filepath.Join(group, "cgroup.kill"), []byte("1"), 0600)
		// cgroup.kill is asynchronous; wait briefly for descendants before removing the group.
		for attempt := 0; attempt < 100; attempt++ {
			if err := os.Remove(group); err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err = os.WriteFile(filepath.Join(group, "memory.max"), []byte(memoryBytes(memory.PerRun)), 0600); err != nil {
		cleanup()
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(group, "memory.oom.group"), []byte("1"), 0600); err != nil {
		cleanup()
		return nil, err
	}
	// Enter the group before exec without clone3, which RestrictNamespaces blocks.
	// Every argument stays positional; no user-controlled text is evaluated as shell code.
	arguments := append([]string(nil), command.Args[1:]...)
	executable := command.Path
	command.Path = "/bin/sh"
	command.Args = append([]string{"/bin/sh", "-c", `printf '%s' "$$" > "$1/cgroup.procs" || exit 125; shift; exec "$@"`, "scriptboard-memory", group, executable}, arguments...)
	environment := command.Env
	if environment == nil {
		environment = os.Environ()
	}
	// Noninteractive shell startup hooks must not run before group assignment.
	command.Env = make([]string, 0, len(environment))
	var restore []string
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key == "BASH_ENV" || key == "ENV" || strings.HasPrefix(key, "BASH_FUNC_") {
			restore = append(restore, entry)
		} else {
			command.Env = append(command.Env, entry)
		}
	}
	if len(restore) > 0 {
		prefix := append([]string(nil), command.Args[:5]...)
		prefix = append(prefix, "/usr/bin/env", "--")
		prefix = append(prefix, restore...)
		command.Args = append(prefix, command.Args[5:]...)
	}
	return cleanup, nil
}
func runnerMemoryRoot() (string, error) {
	if memoryGroupRoot != "" {
		return memoryGroupRoot, nil
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	var root string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::/") {
			root = filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(line, "0::"))
			break
		}
	}
	if root == "" || root == "/sys/fs/cgroup" || !strings.HasSuffix(filepath.Base(root), ".service") {
		return "", fmt.Errorf("Runner must run in a delegated service cgroup")
	}
	supervisor := filepath.Join(root, "supervisor")
	if err = os.MkdirAll(supervisor, 0755); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(supervisor, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("+memory"), 0600); err != nil {
		return "", err
	}
	memoryGroupRoot = root
	return root, nil
}
func resumeMemoryProcess(_ *exec.Cmd) error { return nil }

func memoryBytes(value string) string {
	if value == "unlimited" {
		return "max"
	}
	n, _ := resourcelimits.Parse(value)
	return strconv.FormatUint(n, 10)
}

// InitializeRunnerMemory runs before any child process is accepted.
func InitializeRunnerMemory(policies ...resourcelimits.Memory) error {
	memoryGroupMu.Lock()
	defer memoryGroupMu.Unlock()
	root, err := runnerMemoryRoot()
	if err != nil {
		return err
	}
	if len(policies) > 0 {
		memory := policies[0].Resolved()
		for name, value := range map[string]string{"memory.max": memory.Total, "memory.swap.max": memory.Swap} {
			expected := memoryBytes(value)
			body, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(body)) != expected {
				return fmt.Errorf("%s is %s, configured %s; restart with scriptboard service restart to apply resource settings", name, strings.TrimSpace(string(body)), expected)
			}
		}
	}
	return nil
}

func memoryWaitError(command *exec.Cmd, waitErr error) error {
	if len(command.Args) < 5 || command.Args[3] != "scriptboard-memory" {
		return waitErr
	}
	body, err := os.ReadFile(filepath.Join(command.Args[4], "memory.events"))
	if err != nil {
		return waitErr
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "oom_kill" && fields[1] != "0" {
			if waitErr != nil {
				return fmt.Errorf("task memory limit exceeded (cgroup OOM): %w", waitErr)
			}
			return fmt.Errorf("task memory limit exceeded (cgroup OOM)")
		}
	}
	return waitErr
}
