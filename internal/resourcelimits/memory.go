package resourcelimits

import (
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Empty means inherit; unlimited is an explicit administrator choice.
type Memory struct {
	Total   string `yaml:"runner_memory_limit"`
	PerRun  string `yaml:"run_memory_limit"`
	Process string `yaml:"runner_process_memory_limit"`
	Swap    string `yaml:"runner_swap_limit"`
}

func Defaults() Memory {
	if runtime.GOOS == "windows" {
		return Memory{Total: "4GiB", PerRun: "4GiB", Process: "2GiB", Swap: "0"}
	}
	return Memory{Total: "2GiB", PerRun: "unlimited", Process: "unlimited", Swap: "0"}
}
func (m Memory) Resolved() Memory {
	d := Defaults()
	if m.Total != "" {
		d.Total = m.Total
	}
	if m.PerRun != "" {
		d.PerRun = m.PerRun
	}
	if m.Process != "" {
		d.Process = m.Process
	}
	if m.Swap != "" {
		d.Swap = m.Swap
	}
	return d
}

var sizePattern = regexp.MustCompile(`^([0-9]+)(B|KiB|MiB|GiB|TiB)?$`)

func Parse(value string) (uint64, error) {
	if value == "unlimited" {
		return 0, nil
	}
	parts := sizePattern.FindStringSubmatch(value)
	if parts == nil {
		return 0, fmt.Errorf("invalid memory limit %q: use an integer with B, KiB, MiB, GiB or TiB, or unlimited", value)
	}
	n, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory limit: %w", err)
	}
	shift := map[string]uint{"": 0, "B": 0, "KiB": 10, "MiB": 20, "GiB": 30, "TiB": 40}[parts[2]]
	if n > math.MaxInt64>>shift {
		return 0, fmt.Errorf("memory limit is too large")
	}
	if n == 0 {
		return 0, fmt.Errorf("use unlimited instead of zero for memory limits")
	}
	return n << shift, nil
}
func Task(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	_, err := Parse(value)
	return value, err
}
func (m Memory) Validate() error {
	m = m.Resolved()
	for name, value := range map[string]string{"runner_memory_limit": m.Total, "run_memory_limit": m.PerRun, "runner_process_memory_limit": m.Process, "runner_swap_limit": m.Swap} {
		if name == "runner_swap_limit" && value == "0" {
			continue
		}
		if _, err := Parse(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if runtime.GOOS != "windows" && m.Process != "unlimited" {
		return fmt.Errorf("runner_process_memory_limit is only supported on Windows; use run_memory_limit on Linux")
	}
	if runtime.GOOS == "windows" && m.Swap != "0" {
		return fmt.Errorf("runner_swap_limit is only supported on Linux")
	}
	return nil
}
func Systemd(value string) string {
	if value == "unlimited" {
		return "infinity"
	}
	if value == "0" {
		return "0"
	}
	n, _ := Parse(value)
	if n%(1<<30) == 0 {
		return strconv.FormatUint(n>>30, 10) + "G"
	}
	return strconv.FormatUint(n, 10)
}
