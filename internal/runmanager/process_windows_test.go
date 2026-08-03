//go:build windows

package runmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsBatchArgumentsRejectCmdMetacharacters(t *testing.T) {
	t.Parallel()

	executable, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip("cmd.exe is unavailable")
	}
	executor := executorCandidate{
		path: executable, prefix: []string{"/D", "/S", "/V:OFF", "/C"}, batch: true,
	}
	for _, argument := range []string{
		"safe & whoami",
		"safe | more",
		"safe > output.txt",
		"%COMSPEC%",
		"!SCRIPTBOARD_RUN_ID!",
		`safe" & whoami`,
		"safe\r\nwhoami",
	} {
		argument := argument
		t.Run(argument, func(t *testing.T) {
			if _, err := newExecutorCommand(executor, `C:\safe\script.cmd`, []string{argument}); err == nil {
				t.Fatalf("unsafe batch argument %q was accepted", argument)
			}
		})
	}
}

func TestWindowsBatchCommandPreservesOrdinaryArguments(t *testing.T) {
	t.Parallel()

	executable, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip("cmd.exe is unavailable")
	}
	root := filepath.Join(t.TempDir(), "batch files (safe)")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "echo argument.cmd")
	if err := os.WriteFile(script, []byte("@echo off\r\necho [%~1]\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command, err := newExecutorCommand(executorCandidate{
		path: executable, prefix: []string{"/D", "/S", "/V:OFF", "/C"}, batch: true,
	}, script, []string{"ordinary argument"})
	if err != nil {
		t.Fatalf("build batch command: %v", err)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run batch command: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "[ordinary argument]" {
		t.Fatalf("batch output = %q", got)
	}
}
