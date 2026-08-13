//go:build windows

package pirpc

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	assistantMaximumProcesses   uint32  = 1
	assistantMaximumMemoryBytes uintptr = 1 << 30
	assistantMaximumCPU100ns    int64   = int64((15 * time.Minute) / (100 * time.Nanosecond))
	assistantUIRestrictions     uint32  = windows.JOB_OBJECT_UILIMIT_DESKTOP |
		windows.JOB_OBJECT_UILIMIT_DISPLAYSETTINGS |
		windows.JOB_OBJECT_UILIMIT_EXITWINDOWS |
		windows.JOB_OBJECT_UILIMIT_GLOBALATOMS |
		windows.JOB_OBJECT_UILIMIT_HANDLES |
		windows.JOB_OBJECT_UILIMIT_READCLIPBOARD |
		windows.JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS |
		windows.JOB_OBJECT_UILIMIT_WRITECLIPBOARD
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
	if err := configureAssistantJob(job); err != nil {
		windows.CloseHandle(job)
		return nil, err
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

func configureAssistantJob(job windows.Handle) error {
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
		windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
		windows.JOB_OBJECT_LIMIT_PROCESS_TIME |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	information.BasicLimitInformation.ActiveProcessLimit = assistantMaximumProcesses
	information.BasicLimitInformation.PerProcessUserTimeLimit = assistantMaximumCPU100ns
	information.ProcessMemoryLimit = assistantMaximumMemoryBytes
	information.JobMemoryLimit = assistantMaximumMemoryBytes
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
		return fmt.Errorf("configure Pi Job Object resource limits: %w", err)
	}
	ui := windows.JOBOBJECT_BASIC_UI_RESTRICTIONS{UIRestrictionsClass: assistantUIRestrictions}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectBasicUIRestrictions, uintptr(unsafe.Pointer(&ui)), uint32(unsafe.Sizeof(ui))); err != nil {
		return fmt.Errorf("configure Pi Job Object UI restrictions: %w", err)
	}
	return nil
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
