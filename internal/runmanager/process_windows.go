//go:build windows

package runmanager

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func newExecutorCommand(executor executorCandidate, script string, arguments []string) (*exec.Cmd, error) {
	commandArguments := append(append([]string{}, executor.prefix...), script)
	commandArguments = append(commandArguments, arguments...)
	command := exec.Command(executor.path, commandArguments...)
	if !executor.batch {
		return command, nil
	}
	if err := validateBatchScriptPath(script); err != nil {
		return nil, err
	}
	quoted := make([]string, 0, len(arguments)+1)
	quoted = append(quoted, `"`+script+`"`)
	for index, argument := range arguments {
		if err := validateBatchArgument(argument); err != nil {
			return nil, fmt.Errorf("batch argument %d: %w", index+1, err)
		}
		quoted = append(quoted, `"`+argument+`"`)
	}
	commandString := `"` + strings.Join(quoted, " ") + `"`
	command.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: strings.Join([]string{
			syscall.EscapeArg(executor.path),
			strings.Join(executor.prefix, " "),
			commandString,
		}, " "),
	}
	return command, nil
}

func validateBatchScriptPath(value string) error {
	if strings.ContainsAny(value, "\"^%!\x00\r\n") || containsBatchControlCharacter(value) {
		return errors.New("batch script path contains characters that cmd.exe cannot safely preserve")
	}
	return nil
}

func validateBatchArgument(value string) error {
	if strings.ContainsAny(value, "\"&|<>()^%!\x00\r\n") || containsBatchControlCharacter(value) {
		return errors.New("contains cmd.exe metacharacters")
	}
	return nil
}

func containsBatchControlCharacter(value string) bool {
	for _, character := range value {
		if character < 0x20 {
			return true
		}
	}
	return false
}

func configureProcess(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
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
