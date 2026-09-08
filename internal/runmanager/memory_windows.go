//go:build windows

package runmanager

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"scriptboard/internal/resourcelimits"
)

func prepareMemory(command *exec.Cmd, _ resourcelimits.Memory, _ string) (func(), error) {
	// Start suspended so children cannot escape Job Object assignment before limits apply.
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return func() {}, nil
}
func resumeMemoryProcess(command *exec.Cmd) error {
	handle, err := windows.OpenProcess(0x0800, false, uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(handle))
	if status != 0 {
		return fmt.Errorf("resume limited process: NTSTATUS 0x%x", status)
	}
	return nil
}

func InitializeRunnerMemory(policies ...resourcelimits.Memory) error {
	_, err := runnerAggregateJob(policies...)
	return err
}

func memoryWaitError(_ *exec.Cmd, waitErr error) error { return waitErr }
