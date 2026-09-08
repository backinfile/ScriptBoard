//go:build windows

package runmanager

import (
	"bufio"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"scriptboard/internal/resourcelimits"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsMemoryAllocationProbe(t *testing.T) {
	if os.Getenv("SCRIPTBOARD_MEMORY_PROBE") != "1" {
		return
	}
	address, err := windows.VirtualAlloc(0, 96<<20, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if address == 0 || err != nil {
		os.Exit(42)
	}
	if os.Getenv("SCRIPTBOARD_MEMORY_HOLD") == "1" {
		fmt.Println("allocated")
		time.Sleep(20 * time.Second)
	}
	_ = windows.VirtualFree(address, 0, windows.MEM_RELEASE)
	os.Exit(0)
}
func TestWindowsTaskMemoryEnforcement(t *testing.T) {
	for _, test := range []struct {
		limit   string
		process string
		code    int
	}{{"64MiB", "unlimited", 42}, {"256MiB", "unlimited", 0}, {"unlimited", "unlimited", 0}, {"256MiB", "64MiB", 42}} {
		t.Run(test.limit, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestWindowsMemoryAllocationProbe$")
			command.Env = append(os.Environ(), "SCRIPTBOARD_MEMORY_PROBE=1")
			configureProcess(command)
			memory := resourcelimits.Defaults()
			memory.PerRun = test.limit
			memory.Process = test.process
			resourceCleanup, err := prepareMemory(command, memory, "test")
			if err != nil {
				t.Fatal(err)
			}
			defer resourceCleanup()
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			cleanup, err := attachProcess(command.Process, memory)
			if err != nil {
				command.Process.Kill()
				command.Wait()
				t.Fatal(err)
			}
			defer cleanup()
			if err = resumeMemoryProcess(command); err != nil {
				command.Process.Kill()
				command.Wait()
				t.Fatal(err)
			}
			_ = command.Wait()
			if command.ProcessState.ExitCode() != test.code {
				t.Fatalf("exit=%d, want %d", command.ProcessState.ExitCode(), test.code)
			}
		})
	}
}
func TestWindowsGlobalMemoryPolicy(t *testing.T) {
	if os.Getenv("SCRIPTBOARD_GLOBAL_MEMORY_PROBE") == "1" {
		memory := resourcelimits.Defaults()
		memory.Total = os.Getenv("SCRIPTBOARD_GLOBAL_MEMORY_VALUE")
		job, err := runnerAggregateJob(memory)
		if err != nil {
			t.Fatal(err)
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			t.Fatal(err)
		}
		expected, _ := resourcelimits.Parse(memory.Total)
		if uint64(info.JobMemoryLimit) != expected {
			t.Fatalf("global memory=%d want %d", info.JobMemoryLimit, expected)
		}
		if expected == 0 && info.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_JOB_MEMORY != 0 {
			t.Fatal("unlimited still restricted")
		}
		return
	}
	for _, value := range []string{"128MiB", "unlimited"} {
		command := exec.Command(os.Args[0], "-test.run=^TestWindowsGlobalMemoryPolicy$")
		command.Env = append(os.Environ(), "SCRIPTBOARD_GLOBAL_MEMORY_PROBE=1", "SCRIPTBOARD_GLOBAL_MEMORY_VALUE="+value)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", value, err, output)
		}
	}
}

func TestWindowsConcurrentMemoryBudget(t *testing.T) {
	if os.Getenv("SCRIPTBOARD_CONCURRENT_MEMORY_PROBE") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestWindowsConcurrentMemoryBudget$", "-test.timeout=30s")
		command.Env = append(os.Environ(), "SCRIPTBOARD_CONCURRENT_MEMORY_PROBE=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		return
	}
	memory := resourcelimits.Defaults()
	memory.Total = "256MiB"
	memory.PerRun = "256MiB"
	memory.Process = "unlimited"
	start := func(hold bool) (*exec.Cmd, *bufio.Reader) {
		command := exec.Command(os.Args[0], "-test.run=^TestWindowsMemoryAllocationProbe$")
		command.Env = append(os.Environ(), "SCRIPTBOARD_MEMORY_PROBE=1")
		if hold {
			command.Env = append(command.Env, "SCRIPTBOARD_MEMORY_HOLD=1")
		}
		configureProcess(command)
		resourceCleanup, err := prepareMemory(command, memory, "concurrent")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(resourceCleanup)
		pipe, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		cleanup, err := attachProcess(command.Process, memory)
		if err != nil {
			command.Process.Kill()
			command.Wait()
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		if err = resumeMemoryProcess(command); err != nil {
			t.Fatal(err)
		}
		return command, bufio.NewReader(pipe)
	}
	first, reader := start(true)
	defer func() { first.Process.Kill(); first.Wait() }()
	if line, err := reader.ReadString('\n'); err != nil || line != "allocated\n" {
		t.Fatalf("first allocation: %q %v", line, err)
	}
	second, _ := start(false)
	_ = second.Wait()
	if second.ProcessState.ExitCode() != 42 {
		t.Fatalf("concurrent allocation exit=%d", second.ProcessState.ExitCode())
	}
}
