package appstatus

import (
	"context"
	"errors"
	"strings"

	"scriptboard/internal/logstream"
)

var (
	ErrApplicationLogsUnsupported = errors.New("application logs are unsupported for this application")
	ErrApplicationLogProbeMissing = errors.New("application log probe is unavailable")
)

type LogRequest struct {
	Application Application
	Container   *RawContainer
}

type LogProbe interface {
	LogSource(context.Context, LogRequest) (logstream.Source, error)
}

func (m *Monitor) LogSource(ctx context.Context, id string) (logstream.Source, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrApplicationNotFound
	}
	application, raw, err := m.applicationDetailSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if application.Kind != KindDocker {
		return nil, ErrApplicationLogsUnsupported
	}
	request := LogRequest{Application: application}
	for position := range raw.Containers {
		if normalizeContainerName(raw.Containers[position].Name) == application.Identity {
			request.Container = &raw.Containers[position]
			break
		}
	}
	probe, ok := m.probe.(LogProbe)
	if !ok {
		return nil, ErrApplicationLogProbeMissing
	}
	return probe.LogSource(ctx, request)
}
