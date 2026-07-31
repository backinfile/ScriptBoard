package appstatus

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrApplicationNotFound = errors.New("application was not found")

type RuntimeState string

const (
	RuntimeAvailable   RuntimeState = "available"
	RuntimePartial     RuntimeState = "partial"
	RuntimeRestricted  RuntimeState = "restricted"
	RuntimeUnavailable RuntimeState = "unavailable"
)

type RelatedProcess struct {
	PID         int32  `json:"pid"`
	ParentPID   int32  `json:"parentPid"`
	Name        string `json:"name"`
	User        string `json:"user,omitempty"`
	CommandLine string `json:"commandLine,omitempty"`
	Threads     int32  `json:"threads,omitempty"`
	MemoryBytes uint64 `json:"memoryBytes,omitempty"`
}

type NetworkEndpoint struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint32 `json:"port"`
}

type NetworkConnection struct {
	Protocol      string `json:"protocol"`
	LocalAddress  string `json:"localAddress"`
	LocalPort     uint32 `json:"localPort"`
	RemoteAddress string `json:"remoteAddress,omitempty"`
	RemotePort    uint32 `json:"remotePort,omitempty"`
	State         string `json:"state,omitempty"`
}

type HostRuntimeDetail struct {
	CommandLine      string              `json:"commandLine,omitempty"`
	PID              int32               `json:"pid"`
	ParentPID        int32               `json:"parentPid"`
	User             string              `json:"user,omitempty"`
	StartedAt        *time.Time          `json:"startedAt,omitempty"`
	DurationSeconds  int64               `json:"durationSeconds,omitempty"`
	Architecture     string              `json:"architecture,omitempty"`
	Threads          int32               `json:"threads,omitempty"`
	Handles          int32               `json:"handles,omitempty"`
	ExecutablePath   string              `json:"executablePath,omitempty"`
	WorkingDirectory string              `json:"workingDirectory,omitempty"`
	ListeningPorts   []NetworkEndpoint   `json:"listeningPorts"`
	Connections      []NetworkConnection `json:"connections"`
	StartMethod      string              `json:"startMethod,omitempty"`
	RelatedProcesses []RelatedProcess    `json:"relatedProcesses"`
}

type DockerPort struct {
	Protocol      string `json:"protocol"`
	ContainerPort uint32 `json:"containerPort"`
	HostAddress   string `json:"hostAddress,omitempty"`
	HostPort      string `json:"hostPort,omitempty"`
}

type DockerMount struct {
	Type        string `json:"type"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly"`
}

type DockerRuntimeDetail struct {
	CommandLine      string           `json:"commandLine,omitempty"`
	ContainerID      string           `json:"containerId"`
	HostPID          int              `json:"hostPid,omitempty"`
	ContainerPID     int              `json:"containerPid,omitempty"`
	StartedAt        *time.Time       `json:"startedAt,omitempty"`
	DurationSeconds  int64            `json:"durationSeconds,omitempty"`
	Health           string           `json:"health,omitempty"`
	RestartPolicy    string           `json:"restartPolicy,omitempty"`
	RestartCount     int              `json:"restartCount"`
	Image            string           `json:"image,omitempty"`
	WorkingDirectory string           `json:"workingDirectory,omitempty"`
	Ports            []DockerPort     `json:"ports"`
	NetworkMode      string           `json:"networkMode,omitempty"`
	Mounts           []DockerMount    `json:"mounts"`
	RelatedProcesses []RelatedProcess `json:"relatedProcesses"`
}

type RuntimeDetail struct {
	State   RuntimeState         `json:"state"`
	Code    string               `json:"code,omitempty"`
	Message string               `json:"message,omitempty"`
	Kind    Kind                 `json:"kind"`
	Host    *HostRuntimeDetail   `json:"host,omitempty"`
	Docker  *DockerRuntimeDetail `json:"docker,omitempty"`
}

type DetailRequest struct {
	Application Application
	Processes   []RawProcess
	Container   *RawContainer
	CollectedAt time.Time
	HostOS      string
}

type RuntimeDetailProbe interface {
	RuntimeDetail(context.Context, DetailRequest) RuntimeDetail
}

type ApplicationDetails struct {
	Application Application   `json:"application"`
	Runtime     RuntimeDetail `json:"runtime"`
	History     History       `json:"history"`
}

func (m *Monitor) Details(ctx context.Context, id, selectedRange string) (ApplicationDetails, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ApplicationDetails{}, ErrApplicationNotFound
	}
	application, raw, err := m.applicationDetailSnapshot(ctx, id)
	if err != nil {
		return ApplicationDetails{}, err
	}
	history, err := m.History(ctx, id, selectedRange)
	if err != nil {
		return ApplicationDetails{}, err
	}
	result := ApplicationDetails{Application: application, History: history}
	request := DetailRequest{
		Application: application,
		CollectedAt: raw.CollectedAt,
		HostOS:      m.options.HostOS,
	}
	switch application.Kind {
	case KindHost:
		for _, process := range raw.Processes {
			identity := normalizeExecutablePath(process.ExecutablePath, m.options.HostOS)
			if identity == "" {
				identity = restrictedIdentity(process)
			}
			if identity == application.Identity {
				request.Processes = append(request.Processes, process)
			}
		}
	case KindDocker:
		for position := range raw.Containers {
			if normalizeContainerName(raw.Containers[position].Name) == application.Identity {
				request.Container = &raw.Containers[position]
				break
			}
		}
	}
	if !application.Running {
		result.Runtime = RuntimeDetail{
			State: RuntimeUnavailable, Kind: application.Kind,
			Code: "not_running", Message: "The application is not present in the current snapshot.",
		}
		return result, nil
	}
	probe, ok := m.probe.(RuntimeDetailProbe)
	if !ok {
		result.Runtime = RuntimeDetail{
			State: RuntimeUnavailable, Kind: application.Kind,
			Code: "detail_probe_unavailable", Message: "Runtime details are unavailable on this probe.",
		}
		return result, nil
	}
	result.Runtime = probe.RuntimeDetail(ctx, request)
	if result.Runtime.Kind == "" {
		result.Runtime.Kind = application.Kind
	}
	if result.Runtime.State == "" {
		result.Runtime.State = RuntimeUnavailable
		result.Runtime.Code = "detail_unavailable"
		result.Runtime.Message = "Runtime details could not be read."
	}
	return result, nil
}

func (m *Monitor) applicationDetailSnapshot(ctx context.Context, id string) (Application, RawSnapshot, error) {
	m.mu.RLock()
	raw := m.current
	for _, application := range m.apps {
		if application.ID == id {
			m.mu.RUnlock()
			return application, raw, nil
		}
	}
	m.mu.RUnlock()

	var application Application
	err := m.db.QueryRowContext(ctx, `SELECT id, kind, identity, name, technical
		FROM application_pins WHERE id = ?`, id).Scan(
		&application.ID, &application.Kind, &application.Identity,
		&application.Name, &application.Technical,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Application{}, RawSnapshot{}, ErrApplicationNotFound
	}
	if err != nil {
		return Application{}, RawSnapshot{}, err
	}
	application.Pinnable = true
	application.Pinned = true
	return application, raw, nil
}
