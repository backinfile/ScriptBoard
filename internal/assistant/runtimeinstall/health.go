package runtimeinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/assistant/pirpc"
)

func healthCheckCandidate(ctx context.Context, candidate Candidate) error {
	versionContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(versionContext, candidate.Executable, "--version")
	command.Env = runtimeHealthEnvironment(candidate.StateRoot)
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("run pi --version: %w", err)
	}
	versionOutput := strings.TrimSpace(string(output))
	if len(versionOutput) > 128 || !strings.Contains(versionOutput, candidate.Version) {
		return fmt.Errorf("pi --version did not report %s", candidate.Version)
	}
	healthRoot := filepath.Join(candidate.StateRoot, "assistant", "runtime", "health")
	workspace := filepath.Join(healthRoot, "workspace")
	sessionDirectory := filepath.Join(healthRoot, "session")
	piHome := filepath.Join(healthRoot, "pi-home")
	for _, directory := range []string{workspace, sessionDirectory, piHome} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	defer os.RemoveAll(healthRoot)
	args := []string{
		"--mode", "rpc", "--session-dir", sessionDirectory,
		"--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve",
		"--no-extensions", "--no-builtin-tools", "-e", candidate.Extension,
	}
	supervisor := pirpc.NewSupervisor(1)
	session, err := supervisor.Start("runtime-health", pirpc.LaunchSpec{
		Executable: candidate.Executable, Workspace: workspace, Args: args,
		Env: append(runtimeHealthEnvironment(candidate.StateRoot), "PI_CODING_AGENT_DIR="+piHome),
	})
	if err != nil {
		return err
	}
	healthContext, healthCancel := context.WithTimeout(ctx, 15*time.Second)
	_, healthErr := session.Client().GetState(healthContext, "runtime-health-state")
	healthCancel()
	stopContext, stopCancel := context.WithTimeout(context.Background(), 4*time.Second)
	stopErr := supervisor.Close(stopContext)
	stopCancel()
	if healthErr != nil {
		return fmt.Errorf("Pi RPC get_state: %w", healthErr)
	}
	if stopErr != nil && !errors.Is(stopErr, context.Canceled) {
		return fmt.Errorf("stop Pi RPC health process: %w", stopErr)
	}
	return nil
}

func runtimeHealthEnvironment(stateRoot string) []string {
	allowed := map[string]bool{"SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "TEMP": true, "TMP": true, "TMPDIR": true, "LANG": true, "LC_ALL": true}
	result := make([]string, 0, len(allowed)+3)
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found && allowed[strings.ToUpper(name)] {
			result = append(result, entry)
		}
	}
	return append(result, "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0")
}
