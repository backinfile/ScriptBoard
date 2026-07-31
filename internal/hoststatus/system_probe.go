package hoststatus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

type SystemProbe struct {
	stateRoot, installRoot string
	startedAt              time.Time
	process                *process.Process
	logicalCores           int
}

func NewSystemProbe(stateRoot, installRoot string) (*SystemProbe, error) {
	current, _ := process.NewProcess(int32(os.Getpid()))
	cores, _ := cpu.Counts(true)
	if cores < 1 {
		cores = 1
	}
	return &SystemProbe{stateRoot: stateRoot, installRoot: installRoot, startedAt: time.Now().UTC(), process: current, logicalCores: cores}, nil
}

func (p *SystemProbe) Facts(ctx context.Context) (Facts, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return Facts{}, err
	}
	virtual, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return Facts{}, err
	}
	cpuInfo, _ := cpu.InfoWithContext(ctx)
	model := ""
	if len(cpuInfo) > 0 {
		model = cpuInfo[0].ModelName
	}
	return Facts{
		Hostname: info.Hostname, OS: info.OS, Platform: info.Platform, PlatformVersion: info.PlatformVersion,
		Architecture: runtime.GOARCH, CPUModel: model, LogicalCores: p.logicalCores,
		TotalMemoryBytes: virtual.Total, BootedAt: time.Unix(int64(info.BootTime), 0).UTC(), ServiceStartedAt: p.startedAt,
	}, nil
}

func (p *SystemProbe) Sample(ctx context.Context) RawSample {
	result := RawSample{At: time.Now().UTC(), Disks: map[string]DiskCounters{}, Networks: map[string]NetworkCounters{}, Interfaces: map[string]InterfaceInfo{}, Errors: map[string]string{}}
	if values, err := cpu.TimesWithContext(ctx, false); err != nil || len(values) == 0 {
		result.Errors["cpu"] = errorText(err, "没有 CPU 计数器")
	} else {
		value := values[0]
		result.CPU = &CPUCounters{
			TotalSeconds: value.User + value.System + value.Idle + value.Nice + value.Iowait + value.Irq + value.Softirq + value.Steal,
			IdleSeconds:  value.Idle + value.Iowait, UserSeconds: value.User + value.Nice,
			SystemSeconds: value.System + value.Irq + value.Softirq, IOWaitSeconds: value.Iowait,
		}
	}
	if value, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		result.Errors["memory"] = err.Error()
	} else {
		memory := &Memory{TotalBytes: value.Total, AvailableBytes: value.Available, UsedBytes: value.Total - value.Available}
		if value.Total > 0 {
			memory.UsedPercent = float64(memory.UsedBytes) / float64(value.Total) * 100
		}
		if swap, swapErr := mem.SwapMemoryWithContext(ctx); swapErr == nil {
			memory.SwapTotalBytes, memory.SwapUsedBytes, memory.SwapUsedPercent = swap.Total, swap.Used, swap.UsedPercent
		}
		memory.CommittedBytes, memory.CommitLimitBytes = committedMemory()
		result.Memory = memory
	}
	if runtime.GOOS != "windows" {
		if value, err := load.AvgWithContext(ctx); err == nil {
			result.Load = &[3]float64{value.Load1, value.Load5, value.Load15}
		}
	}
	p.collectFilesystems(ctx, &result)
	if counters, err := disk.IOCountersWithContext(ctx); err != nil {
		result.Errors["disk"] = err.Error()
	} else {
		for name, value := range counters {
			result.Disks[name] = DiskCounters{ReadBytes: value.ReadBytes, WriteBytes: value.WriteBytes, Reads: value.ReadCount, Writes: value.WriteCount, ReadTimeMS: value.ReadTime, WriteTimeMS: value.WriteTime}
		}
	}
	p.collectNetwork(ctx, &result)
	p.collectProcess(ctx, &result)
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	return result
}

func (p *SystemProbe) collectFilesystems(ctx context.Context, result *RawSample) {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		result.Errors["storage"] = err.Error()
		return
	}
	byID := map[string]Filesystem{}
	for _, partition := range partitions {
		usage, usageErr := disk.UsageWithContext(ctx, partition.Mountpoint)
		if usageErr != nil || usage.Total == 0 {
			continue
		}
		id := partition.Device + "|" + filepath.Clean(partition.Mountpoint)
		value := Filesystem{ID: id, Device: partition.Device, Mountpoint: partition.Mountpoint, Type: partition.Fstype, TotalBytes: usage.Total, UsedBytes: usage.Used, AvailableBytes: usage.Free, UsedPercent: usage.UsedPercent, Online: true}
		byID[id] = value
	}
	assignFilesystemRole(byID, p.stateRoot, "state")
	assignFilesystemRole(byID, p.installRoot, "install")
	for _, value := range byID {
		result.Filesystems = append(result.Filesystems, value)
	}
	sort.Slice(result.Filesystems, func(i, j int) bool { return result.Filesystems[i].Mountpoint < result.Filesystems[j].Mountpoint })
}

func assignFilesystemRole(filesystems map[string]Filesystem, path, role string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	selectedID, selectedLength := "", -1
	for id, value := range filesystems {
		if pathWithinMount(value.Mountpoint, path) && len(filepath.Clean(value.Mountpoint)) > selectedLength {
			selectedID, selectedLength = id, len(filepath.Clean(value.Mountpoint))
		}
	}
	if selectedID != "" {
		value := filesystems[selectedID]
		value.Roles = append(value.Roles, role)
		filesystems[selectedID] = value
	}
}

func (p *SystemProbe) collectNetwork(ctx context.Context, result *RawSample) {
	interfaces, err := gnet.InterfacesWithContext(ctx)
	if err != nil {
		result.Errors["network"] = err.Error()
		return
	}
	active := map[string]bool{}
	for _, value := range interfaces {
		up, loopback := false, false
		for _, flag := range value.Flags {
			up = up || flag == "up"
			loopback = loopback || flag == "loopback"
		}
		if !up || loopback {
			continue
		}
		addresses := make([]string, 0, len(value.Addrs))
		for _, address := range value.Addrs {
			addresses = append(addresses, address.Addr)
		}
		active[value.Name] = true
		result.Interfaces[value.Name] = InterfaceInfo{ID: value.Name, Name: value.Name, Addresses: addresses}
	}
	counters, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil {
		result.Errors["network"] = err.Error()
		return
	}
	for _, value := range counters {
		if !active[value.Name] {
			continue
		}
		result.Networks[value.Name] = NetworkCounters{ReceivedBytes: value.BytesRecv, SentBytes: value.BytesSent, ReceivedErrors: value.Errin, SentErrors: value.Errout, ReceivedDrops: value.Dropin, Drops: value.Dropout}
	}
}

func (p *SystemProbe) collectProcess(ctx context.Context, result *RawSample) {
	if p.process == nil {
		return
	}
	value := &Process{}
	if percent, err := p.process.PercentWithContext(ctx, 0); err == nil {
		value.CPUPercent = clampPercent(percent / float64(p.logicalCores))
	} else {
		result.Errors["process"] = err.Error()
	}
	if memory, err := p.process.MemoryInfoWithContext(ctx); err == nil {
		value.ResidentMemoryBytes = memory.RSS
	}
	if threads, err := p.process.NumThreadsWithContext(ctx); err == nil {
		value.Threads = threads
	}
	if files, err := p.process.NumFDsWithContext(ctx); err == nil {
		if runtime.GOOS == "windows" {
			value.Handles = files
		} else {
			value.OpenFiles = files
		}
	}
	result.Process = value
}

func pathWithinMount(mountpoint, candidate string) bool {
	mountpoint, mountErr := filepath.Abs(mountpoint)
	candidate, candidateErr := filepath.Abs(candidate)
	if mountErr != nil || candidateErr != nil {
		return false
	}
	relative, err := filepath.Rel(mountpoint, candidate)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		relative = strings.ToLower(relative)
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func errorText(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

var _ Probe = (*SystemProbe)(nil)

func (p *SystemProbe) String() string { return fmt.Sprintf("system probe for %s", p.stateRoot) }
