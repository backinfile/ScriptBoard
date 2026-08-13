//go:build windows

package pirpc

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestConfigureAssistantJobAppliesResourceAndUIBoundaries(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	if err := configureAssistantJob(job); err != nil {
		t.Fatal(err)
	}

	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		t.Fatal(err)
	}
	requiredLimits := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
		windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
		windows.JOB_OBJECT_LIMIT_PROCESS_TIME |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION)
	if limits.BasicLimitInformation.LimitFlags&requiredLimits != requiredLimits {
		t.Fatalf("limit flags = %#x, want %#x", limits.BasicLimitInformation.LimitFlags, requiredLimits)
	}
	if limits.BasicLimitInformation.ActiveProcessLimit != assistantMaximumProcesses || limits.ProcessMemoryLimit != assistantMaximumMemoryBytes || limits.JobMemoryLimit != assistantMaximumMemoryBytes {
		t.Fatalf("processes = %d, process memory = %d, job memory = %d", limits.BasicLimitInformation.ActiveProcessLimit, limits.ProcessMemoryLimit, limits.JobMemoryLimit)
	}
	if limits.BasicLimitInformation.PerProcessUserTimeLimit != assistantMaximumCPU100ns {
		t.Fatalf("CPU limit = %d", limits.BasicLimitInformation.PerProcessUserTimeLimit)
	}

	var ui windows.JOBOBJECT_BASIC_UI_RESTRICTIONS
	if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicUIRestrictions, uintptr(unsafe.Pointer(&ui)), uint32(unsafe.Sizeof(ui)), nil); err != nil {
		t.Fatal(err)
	}
	if ui.UIRestrictionsClass != assistantUIRestrictions {
		t.Fatalf("UI restrictions = %#x, want %#x", ui.UIRestrictionsClass, assistantUIRestrictions)
	}
}
