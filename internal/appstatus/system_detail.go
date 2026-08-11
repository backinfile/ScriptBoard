package appstatus

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"scriptboard/internal/secretredaction"
)

const maxProcessConnections = 256

func (p *SystemProbe) RuntimeDetail(ctx context.Context, request DetailRequest) RuntimeDetail {
	switch request.Application.Kind {
	case KindHost:
		return collectHostRuntimeDetail(ctx, request)
	case KindDocker:
		if p.dockerError != nil || p.docker == nil {
			message := "Docker Engine is unavailable."
			if p.dockerError != nil {
				message = secretredaction.String(p.dockerError.Error())
			}
			return RuntimeDetail{
				State: RuntimeUnavailable, Kind: KindDocker,
				Code: "docker_unavailable", Message: message,
			}
		}
		return p.docker.RuntimeDetail(ctx, request)
	default:
		return RuntimeDetail{
			State: RuntimeUnavailable, Kind: request.Application.Kind,
			Code: "unsupported_application_kind", Message: "This application kind is not supported.",
		}
	}
}

func collectHostRuntimeDetail(ctx context.Context, request DetailRequest) RuntimeDetail {
	result := RuntimeDetail{Kind: KindHost}
	if len(request.Processes) == 0 {
		result.State = RuntimeUnavailable
		result.Code = "process_exited"
		result.Message = "The process exited before its runtime details could be read."
		return result
	}
	processes := append([]RawProcess(nil), request.Processes...)
	primary := selectPrimaryProcess(processes)
	host := &HostRuntimeDetail{
		PID:              primary.PID,
		ParentPID:        primary.ParentPID,
		StartedAt:        timePointer(primary.CreatedAt),
		Architecture:     runtime.GOARCH,
		Threads:          primary.Threads,
		ExecutablePath:   primary.ExecutablePath,
		ListeningPorts:   make([]NetworkEndpoint, 0),
		Connections:      make([]NetworkConnection, 0),
		RelatedProcesses: make([]RelatedProcess, 0, len(processes)),
		StartMethod:      inferStartMethod(ctx, primary, request.HostOS),
	}
	if !primary.CreatedAt.IsZero() {
		until := request.CollectedAt
		if until.IsZero() {
			until = time.Now().UTC()
		}
		host.DurationSeconds = max(0, int64(until.Sub(primary.CreatedAt).Seconds()))
	}

	readable := 0
	failedReads := 0
	permissionDenied := false
	listening := make(map[NetworkEndpoint]struct{})
	connections := make(map[NetworkConnection]struct{})
	for _, raw := range processes {
		related := RelatedProcess{
			PID: raw.PID, ParentPID: raw.ParentPID, Name: raw.Name,
			Threads: raw.Threads, MemoryBytes: raw.ResidentMemoryBytes,
		}
		item, err := process.NewProcessWithContext(ctx, raw.PID)
		if err != nil || !sameProcessInstance(ctx, item, raw) {
			failedReads++
			permissionDenied = permissionDenied || isPermissionError(err)
			host.RelatedProcesses = append(host.RelatedProcesses, related)
			continue
		}
		processReadable := false
		if value, err := item.UsernameWithContext(ctx); err == nil {
			related.User = value
			processReadable = true
			if raw.PID == primary.PID {
				host.User = value
			}
		} else {
			failedReads++
			permissionDenied = permissionDenied || isPermissionError(err)
		}
		if value, err := item.CmdlineWithContext(ctx); err == nil {
			related.CommandLine = value
			processReadable = true
			if raw.PID == primary.PID {
				host.CommandLine = value
			}
		} else {
			failedReads++
			permissionDenied = permissionDenied || isPermissionError(err)
		}
		if raw.PID == primary.PID {
			if value, err := item.CwdWithContext(ctx); err == nil {
				host.WorkingDirectory = value
				processReadable = true
			} else {
				failedReads++
				permissionDenied = permissionDenied || isPermissionError(err)
			}
			if value, err := item.NumFDsWithContext(ctx); err == nil {
				host.Handles = value
				processReadable = true
			} else {
				failedReads++
				permissionDenied = permissionDenied || isPermissionError(err)
			}
		}
		if values, err := item.ConnectionsMaxWithContext(ctx, maxProcessConnections); err == nil {
			processReadable = true
			for _, value := range values {
				connection := connectionFromStat(value)
				connections[connection] = struct{}{}
				if strings.EqualFold(value.Status, "LISTEN") {
					listening[NetworkEndpoint{
						Protocol: connection.Protocol,
						Address:  connection.LocalAddress,
						Port:     connection.LocalPort,
					}] = struct{}{}
				}
			}
		} else {
			failedReads++
			permissionDenied = permissionDenied || isPermissionError(err)
		}
		if processReadable {
			readable++
		}
		host.RelatedProcesses = append(host.RelatedProcesses, related)
	}
	for endpoint := range listening {
		host.ListeningPorts = append(host.ListeningPorts, endpoint)
	}
	sort.Slice(host.ListeningPorts, func(i, j int) bool {
		if host.ListeningPorts[i].Port != host.ListeningPorts[j].Port {
			return host.ListeningPorts[i].Port < host.ListeningPorts[j].Port
		}
		if host.ListeningPorts[i].Protocol != host.ListeningPorts[j].Protocol {
			return host.ListeningPorts[i].Protocol < host.ListeningPorts[j].Protocol
		}
		return host.ListeningPorts[i].Address < host.ListeningPorts[j].Address
	})
	for connection := range connections {
		host.Connections = append(host.Connections, connection)
	}
	sort.Slice(host.Connections, func(i, j int) bool {
		left, right := host.Connections[i], host.Connections[j]
		if left.LocalPort != right.LocalPort {
			return left.LocalPort < right.LocalPort
		}
		if left.RemotePort != right.RemotePort {
			return left.RemotePort < right.RemotePort
		}
		return left.Protocol < right.Protocol
	})
	result.Host = host
	switch {
	case !request.Application.Pinnable:
		result.State = RuntimeRestricted
		result.Code = "identity_restricted"
		result.Message = "The executable identity is protected by the operating system; readable runtime facts are shown."
	case readable == len(processes) && failedReads == 0:
		result.State = RuntimeAvailable
	case readable > 0:
		result.State = RuntimePartial
		result.Code = "runtime_partially_readable"
		result.Message = "Some process details are unavailable because the process exited or access was denied."
	case permissionDenied:
		result.State = RuntimeRestricted
		result.Code = "permission_denied"
		result.Message = "The operating system denied access to runtime details."
	default:
		result.State = RuntimeUnavailable
		result.Code = "process_exited"
		result.Message = "The process exited before its runtime details could be read."
	}
	return result
}

func selectPrimaryProcess(processes []RawProcess) RawProcess {
	pids := make(map[int32]struct{}, len(processes))
	for _, item := range processes {
		pids[item.PID] = struct{}{}
	}
	sort.SliceStable(processes, func(i, j int) bool {
		leftExternal := processHasExternalParent(processes[i], pids)
		rightExternal := processHasExternalParent(processes[j], pids)
		if leftExternal != rightExternal {
			return leftExternal
		}
		if !processes[i].CreatedAt.Equal(processes[j].CreatedAt) {
			if processes[i].CreatedAt.IsZero() {
				return false
			}
			if processes[j].CreatedAt.IsZero() {
				return true
			}
			return processes[i].CreatedAt.Before(processes[j].CreatedAt)
		}
		return processes[i].PID < processes[j].PID
	})
	return processes[0]
}

func processHasExternalParent(item RawProcess, pids map[int32]struct{}) bool {
	_, parentInApplication := pids[item.ParentPID]
	return !parentInApplication
}

func sameProcessInstance(ctx context.Context, item *process.Process, raw RawProcess) bool {
	if item == nil {
		return false
	}
	if raw.CreatedAt.IsZero() {
		return true
	}
	value, err := item.CreateTimeWithContext(ctx)
	if err != nil {
		return false
	}
	return time.UnixMilli(value).UTC().Equal(raw.CreatedAt)
}

func inferStartMethod(ctx context.Context, primary RawProcess, hostOS string) string {
	if primary.ParentPID == 0 {
		return "unknown"
	}
	if primary.ParentPID == 1 {
		if hostOS == "windows" {
			return "windows-service"
		}
		return "system-manager"
	}
	parent, err := process.NewProcessWithContext(ctx, primary.ParentPID)
	if err != nil {
		return "external-process"
	}
	name, err := parent.NameWithContext(ctx)
	if err != nil {
		return "external-process"
	}
	switch strings.ToLower(strings.TrimSuffix(name, ".exe")) {
	case "services":
		if hostOS == "windows" {
			return "windows-service"
		}
	case "taskeng", "taskhost", "taskhostw":
		if hostOS == "windows" {
			return "scheduled-task"
		}
	case "systemd", "init", "runit", "s6-supervise", "supervisord":
		return "system-service"
	case "cmd", "powershell", "pwsh", "bash", "sh", "zsh", "fish":
		return "shell"
	}
	return "parent-process"
}

func connectionFromStat(value gnet.ConnectionStat) NetworkConnection {
	return NetworkConnection{
		Protocol:      socketProtocol(value.Type),
		LocalAddress:  value.Laddr.IP,
		LocalPort:     value.Laddr.Port,
		RemoteAddress: value.Raddr.IP,
		RemotePort:    value.Raddr.Port,
		State:         strings.ToLower(value.Status),
	}
}

func socketProtocol(socketType uint32) string {
	switch socketType {
	case 1:
		return "tcp"
	case 2:
		return "udp"
	default:
		return "other"
	}
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "permission denied") ||
		strings.Contains(value, "access is denied") ||
		strings.Contains(value, "operation not permitted")
}

var _ RuntimeDetailProbe = (*SystemProbe)(nil)
