//go:build windows

package pirpc

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsAssistantProcessController struct {
	job     windows.Handle
	process *os.Process
}

func configureAssistantProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func attachAssistantProcess(process *os.Process) (assistantProcessController, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create Pi Job Object: %w", err)
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("configure Pi Job Object: %w", err)
	}
	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("open Pi child process: %w", err)
	}
	err = windows.AssignProcessToJobObject(job, processHandle)
	windows.CloseHandle(processHandle)
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("assign Pi Job Object: %w", err)
	}
	return &windowsAssistantProcessController{job: job, process: process}, nil
}

func (controller *windowsAssistantProcessController) Terminate(force bool) error {
	if force {
		return windows.TerminateJobObject(controller.job, 1)
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(controller.process.Pid))
}

func (controller *windowsAssistantProcessController) Close() error {
	return windows.CloseHandle(controller.job)
}

func mkdirPrivate(path string) error { return os.MkdirAll(path, 0o700) }
