package main

import (
	"reflect"
	"testing"
)

func TestRunnerConfigArgumentsOmitsEmptyConfigPath(t *testing.T) {
	want := []string{"--state-root", `C:\ScriptBoard\state`}
	if got := runnerConfigArguments("  ", want[1]); !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
}

func TestRunnerConfigArgumentsIncludesExplicitConfigPath(t *testing.T) {
	want := []string{"--config", `C:\ScriptBoard\config.yaml`, "--state-root", `C:\ScriptBoard\state`}
	if got := runnerConfigArguments(`C:\ScriptBoard\config.yaml`, want[3]); !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
}
