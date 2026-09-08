//go:build linux

package runmanager

import (
	"os"
	"os/exec"
	"scriptboard/internal/resourcelimits"
	"strings"
	"testing"
)

func TestLinuxDelegatedMemory(t *testing.T) {
	if os.Getenv("SCRIPTBOARD_TEST_CGROUP") != "1" {
		t.Skip("requires a dedicated delegated cgroup v2 service")
	}
	if err := InitializeRunnerMemory(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		limit string
		fail  bool
	}{{"128MiB", false}, {"32MiB", true}, {"unlimited", false}} {
		t.Run(test.limit, func(t *testing.T) {
			command := exec.Command("/usr/bin/python3", "-c", "x=bytearray(64*1024*1024); print('allocated')")
			configureProcess(command)
			memory := resourcelimits.Defaults()
			memory.PerRun = test.limit
			cleanup, err := prepareMemory(command, memory, "test")
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			output, err := command.CombinedOutput()
			if (err != nil) != test.fail {
				t.Fatalf("limit=%s error=%v output=%s", test.limit, err, output)
			}
			if !test.fail && !strings.Contains(string(output), "allocated") {
				t.Fatal(string(output))
			}
		})
	}
}

func TestLinuxDescendantMemoryBudget(t *testing.T) {
	if os.Getenv("SCRIPTBOARD_TEST_CGROUP") != "1" {
		t.Skip("requires delegated cgroup")
	}
	if err := InitializeRunnerMemory(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/python3", "-c", "import os,time; p=os.fork(); x=bytearray(40*1024*1024); time.sleep(1)")
	configureProcess(command)
	memory := resourcelimits.Defaults()
	memory.PerRun = "64MiB"
	cleanup, err := prepareMemory(command, memory, "descendants")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("descendants exceeded their shared limit: %s", output)
	}
}

func TestLinuxMemoryLauncherPreservesArgumentsAndEnvironment(t *testing.T) {
	if os.Getenv("SCRIPTBOARD_TEST_CGROUP") != "1" {
		t.Skip("requires delegated cgroup")
	}
	if err := InitializeRunnerMemory(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/env")
	command.Env = []string{"ENV=literal value", "BASH_ENV=literal hook", "BASH_FUNC_probe%%=() { echo unused; }"}
	configureProcess(command)
	memory := resourcelimits.Defaults()
	memory.PerRun = "32MiB"
	cleanup, err := prepareMemory(command, memory, "env")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, output)
	}
	if !strings.Contains(string(output), "ENV=literal value") || !strings.Contains(string(output), "BASH_ENV=literal hook") {
		t.Fatalf("lost environment: %s", output)
	}
}
