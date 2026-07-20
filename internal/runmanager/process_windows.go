//go:build windows

package runmanager

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func attachProcess(process *os.Process) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create Job Object: %w", err)
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("configure Job Object: %w", err)
	}
	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("open child process: %w", err)
	}
	err = windows.AssignProcessToJobObject(job, processHandle)
	windows.CloseHandle(processHandle)
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("assign Job Object: %w", err)
	}
	return func() { _ = windows.CloseHandle(job) }, nil
}

func terminateProcess(process *os.Process, force bool) error {
	if !force {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(process.Pid))
	}
	arguments := []string{"/PID", strconv.Itoa(process.Pid), "/T"}
	arguments = append(arguments, "/F")
	if output, err := exec.Command("taskkill.exe", arguments...).CombinedOutput(); err != nil {
		return fmt.Errorf("taskkill: %w: %s", err, output)
	}
	return nil
}
