package appstatus

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

type SystemProbe struct {
	logicalCores int
	docker       *dockerCollector
	dockerError  error
	mu           sync.Mutex
}

func NewSystemProbe() *SystemProbe {
	logicalCores, _ := cpu.Counts(true)
	if logicalCores < 1 {
		logicalCores = 1
	}
	docker, dockerError := newDockerCollector()
	return &SystemProbe{logicalCores: logicalCores, docker: docker, dockerError: dockerError}
}

func (p *SystemProbe) Snapshot(ctx context.Context) RawSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := RawSnapshot{
		CollectedAt:  time.Now().UTC(),
		LogicalCores: p.logicalCores,
		Errors:       map[string]string{},
	}
	if memory, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		result.TotalMemoryBytes = memory.Total
	} else {
		result.Errors["memory"] = err.Error()
	}
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		result.Errors["host"] = err.Error()
	} else {
		result.Processes = make([]RawProcess, 0, len(processes))
		for _, item := range processes {
			raw, ok := collectProcess(ctx, item)
			if ok {
				result.Processes = append(result.Processes, raw)
			}
		}
	}
	if p.dockerError != nil {
		result.Errors["docker"] = p.dockerError.Error()
	} else if p.docker != nil {
		containers, containerIDs, available, dockerError := p.docker.Snapshot(ctx, p.logicalCores, result.CollectedAt)
		result.Containers = containers
		result.DockerAvailable = available
		if dockerError != nil {
			result.Errors["docker"] = dockerError.Error()
		}
		if runtime.GOOS == "linux" && len(containerIDs) > 0 {
			result.Processes = excludeContainerProcesses(result.Processes, containerIDs)
		}
	}
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	return result
}

func (p *SystemProbe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.docker == nil {
		return nil
	}
	return p.docker.Close()
}

func excludeContainerProcesses(processes []RawProcess, containerIDs []string) []RawProcess {
	filtered := processes[:0]
	for _, process := range processes {
		content, err := os.ReadFile("/proc/" + stringProcessID(process.PID) + "/cgroup")
		if err == nil && cgroupMatchesContainer(string(content), containerIDs) {
			continue
		}
		filtered = append(filtered, process)
	}
	return filtered
}

func cgroupMatchesContainer(content string, containerIDs []string) bool {
	content = strings.ToLower(content)
	for _, id := range containerIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if strings.Contains(content, id) || len(id) >= 12 && strings.Contains(content, id[:12]) {
			return true
		}
	}
	return false
}

func stringProcessID(value int32) string {
	if value == 0 {
		return "0"
	}
	var buffer [12]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[position:])
}

func collectProcess(ctx context.Context, item *process.Process) (RawProcess, bool) {
	name, nameErr := item.NameWithContext(ctx)
	if nameErr != nil || name == "" {
		return RawProcess{}, false
	}
	raw := RawProcess{PID: item.Pid, Name: name}
	if value, err := item.PpidWithContext(ctx); err == nil {
		raw.ParentPID = value
	}
	if value, err := item.CreateTimeWithContext(ctx); err == nil {
		raw.CreatedAt = time.UnixMilli(value).UTC()
	}
	if value, err := item.ExeWithContext(ctx); err == nil {
		raw.ExecutablePath = value
	}
	if value, err := item.TimesWithContext(ctx); err == nil {
		raw.CPUSeconds = value.User + value.System + value.Idle + value.Nice +
			value.Iowait + value.Irq + value.Softirq + value.Steal
	}
	if value, err := item.MemoryInfoWithContext(ctx); err == nil {
		raw.ResidentMemoryBytes = value.RSS
	}
	if value, err := item.NumThreadsWithContext(ctx); err == nil {
		raw.Threads = value
	}
	if value, err := item.IOCountersWithContext(ctx); err == nil {
		raw.ReadBytes = value.ReadBytes
		raw.WriteBytes = value.WriteBytes
	}
	return raw, true
}

var _ Probe = (*SystemProbe)(nil)
