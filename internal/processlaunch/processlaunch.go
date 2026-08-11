package processlaunch

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"
)

type EnvironmentPolicy uint8

const (
	EnvironmentUnspecified EnvironmentPolicy = iota
	EnvironmentInherit
	EnvironmentExact
)

type Spec struct {
	Context     context.Context
	Executable  string
	Arguments   []string
	Environment EnvironmentPolicy
	Env         []string
	Directory   string
}

// Prepare is the only production boundary that constructs an os/exec command.
// Callers must make environment inheritance explicit; argument-specific domain
// validation remains the responsibility of the owning module.
func Prepare(spec Spec) (*exec.Cmd, error) {
	if spec.Context == nil {
		return nil, errors.New("process context is required")
	}
	if !validProcessValue(spec.Executable) || strings.TrimSpace(spec.Executable) == "" {
		return nil, errors.New("process executable is invalid")
	}
	for index, argument := range spec.Arguments {
		if !validProcessValue(argument) {
			return nil, fmt.Errorf("process argument %d is invalid", index+1)
		}
	}
	if spec.Directory != "" && !validProcessValue(spec.Directory) {
		return nil, errors.New("process working directory is invalid")
	}
	command := exec.CommandContext(spec.Context, spec.Executable, append([]string(nil), spec.Arguments...)...)
	command.Dir = spec.Directory
	switch spec.Environment {
	case EnvironmentInherit:
		if len(spec.Env) != 0 {
			return nil, errors.New("inherited process environment cannot include explicit entries")
		}
	case EnvironmentExact:
		for index, entry := range spec.Env {
			if !validEnvironmentEntry(entry) {
				return nil, fmt.Errorf("process environment entry %d is invalid", index+1)
			}
		}
		command.Env = append([]string(nil), spec.Env...)
	default:
		return nil, errors.New("process environment policy is required")
	}
	return command, nil
}

func validEnvironmentEntry(entry string) bool {
	if !validProcessValue(entry) {
		return false
	}
	name, _, found := strings.Cut(entry, "=")
	if found && name != "" && !strings.Contains(name, "=") {
		return true
	}
	// Windows exposes per-drive current directories as hidden environment
	// entries such as "=C:=C:\\work". os/exec preserves these entries, and
	// callers that intentionally clone os.Environ must be able to do the same.
	return runtime.GOOS == "windows" && len(entry) >= 4 && entry[0] == '=' &&
		(entry[1] >= 'A' && entry[1] <= 'Z' || entry[1] >= 'a' && entry[1] <= 'z') &&
		entry[2] == ':' && entry[3] == '='
}

func validProcessValue(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
