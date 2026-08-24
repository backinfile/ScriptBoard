//go:build windows

package runmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestConfigureProcessUsesAHeadlessProcessGroup(t *testing.T) {
	command := exec.Command("cmd.exe")
	configureProcess(command)
	want := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags&want != want {
		t.Fatalf("creation flags = %#x, want %#x", command.SysProcAttr.CreationFlags, want)
	}
}

func TestWindowsRunnerAggregateJobBoundsAllRuns(t *testing.T) {
	job, err := runnerAggregateJob()
	if err != nil {
		t.Fatal(err)
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		t.Fatal(err)
	}
	required := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY)
	if limits.BasicLimitInformation.LimitFlags&required != required || limits.BasicLimitInformation.ActiveProcessLimit != 64 || limits.JobMemoryLimit != 4<<30 {
		t.Fatalf("aggregate limits = %#v", limits)
	}
}

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

func FuzzWindowsBatchArgumentRoundTrip(f *testing.F) {
	for _, seed := range []string{"ordinary argument", "unicode-参数", "trailing\\", "safe.dot-value", "%COMSPEC%", "a&whoami", "quote\"value", "line\r\nbreak"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, argument string) {
		if len(argument) > 256 {
			t.Skip()
		}
		executable, err := exec.LookPath("cmd.exe")
		if err != nil {
			t.Skip("cmd.exe is unavailable")
		}
		root := t.TempDir()
		script := filepath.Join(root, "round trip.cmd")
		if err := os.WriteFile(script, []byte("@echo off\r\n<nul set /p =[%~1]\r\nexit /b 0\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		command, commandErr := newExecutorCommand(executorCandidate{
			path: executable, prefix: []string{"/D", "/S", "/V:OFF", "/C"}, batch: true,
		}, script, []string{argument})
		unsafe := strings.ContainsAny(argument, "\"&|<>()^%!\x00\r\n") || containsBatchControlCharacter(argument)
		if unsafe {
			if commandErr == nil {
				t.Fatalf("unsafe argument %q was accepted", argument)
			}
			return
		}
		if commandErr != nil {
			t.Fatalf("safe argument %q rejected: %v", argument, commandErr)
		}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("execute argument %q: %v: %s", argument, err, output)
		}
		if got, want := string(output), "["+argument+"]"; got != want {
			t.Fatalf("batch argument changed: got=%q want=%q", got, want)
		}
	})
}
