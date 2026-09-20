package runmanager

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"scriptboard/internal/resourcelimits"
	"strings"
	"testing"
	"time"
)

func TestBuiltinGitUsesManagedProcess(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	request := LaunchRequest{RunID: "builtin-git-test", ScriptPath: filepath.Join(dir, "job.sbflow"), WorkingDirectory: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := runBuiltinCommand(ctx, request, resourcelimits.Defaults(), git, []string{"--version"})
	if err != nil || !strings.Contains(string(output), "git version") {
		t.Fatalf("managed Git execution: %q, %v", output, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := runBuiltinCommand(canceled, request, resourcelimits.Defaults(), git, []string{"--version"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled job launched: %v", err)
	}
}
