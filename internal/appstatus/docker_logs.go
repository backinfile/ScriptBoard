package appstatus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"scriptboard/internal/logstream"
)

var ErrDockerLogContainerNotFound = errors.New("Docker container is no longer available")

type containerLogsClient interface {
	ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
}

type dockerLogAPI interface {
	containerLogsClient
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
}

type dockerLogSource struct {
	client        containerLogsClient
	containerID   string
	sourceVersion string
	name          string
	technical     string
	tty           bool
	running       bool
}

func (s *dockerLogSource) Metadata() logstream.Metadata {
	return logstream.Metadata{
		Kind: "docker", Name: s.name, Technical: s.technical,
		SourceVersion: s.sourceVersion, Running: s.running,
	}
}

func (s *dockerLogSource) History(ctx context.Context, before string) (logstream.Page, error) {
	options := client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true, Tail: "500",
	}
	var boundary logstream.Cursor
	if before != "" {
		cursor, err := logstream.DecodeCursor(before)
		if err != nil {
			return logstream.Page{}, err
		}
		if cursor.Kind != "docker" || cursor.SourceVersion != s.sourceVersion || cursor.Time.IsZero() {
			return logstream.Page{}, logstream.ErrInvalidCursor
		}
		boundary = cursor
		// Ask Docker for the boundary itself so its digest can be revalidated.
		// History still excludes that entry and everything after it below.
		options.Until = cursor.Time.Add(time.Nanosecond).UTC().Format(time.RFC3339Nano)
	}
	stream, err := s.client.ContainerLogs(ctx, s.containerID, options)
	if err != nil {
		return logstream.Page{}, err
	}
	defer stream.Close()
	page := logstream.Page{
		Entries:       make([]logstream.Entry, 0, logstream.DefaultPageLines),
		SourceVersion: s.sourceVersion,
	}
	totalBytes := 0
	dropped := false
	boundaryFound := before == ""
	states := make(map[logstream.EntrySource]dockerEntryState)
	err = decodeDockerLogStream(stream, s.tty, func(source logstream.EntrySource, raw []byte, continuation bool) error {
		entry, err := s.entry(source, raw, continuation, states)
		if err != nil {
			return err
		}
		if boundary.Digest != "" && entry.Cursor == before {
			boundaryFound = true
			return nil
		}
		if boundaryFound && before != "" {
			return nil
		}
		page.Entries = append(page.Entries, entry)
		totalBytes += len(entry.Text)
		for len(page.Entries) > logstream.DefaultPageLines || totalBytes > logstream.DefaultPageBytes {
			totalBytes -= len(page.Entries[0].Text)
			page.Entries = page.Entries[1:]
			dropped = true
		}
		return nil
	})
	if err != nil {
		return logstream.Page{}, err
	}
	if !boundaryFound {
		return logstream.Page{}, logstream.ErrInvalidCursor
	}
	page.HasMore = dropped || len(page.Entries) == logstream.DefaultPageLines
	if len(page.Entries) > 0 {
		page.Before = page.Entries[0].Cursor
	}
	return page, nil
}

func (s *dockerLogSource) Follow(ctx context.Context, after string, emit func(logstream.Event) error) error {
	options := client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: true, Tail: "500",
	}
	var boundary logstream.Cursor
	if after != "" {
		cursor, err := logstream.DecodeCursor(after)
		if err != nil {
			return err
		}
		if cursor.Kind != "docker" || cursor.SourceVersion != s.sourceVersion || cursor.Time.IsZero() {
			if err := emit(logstream.Event{
				Kind: logstream.EventGap, State: "source_changed",
				Message: "容器已经替换，无法证明断线期间的日志连续性。",
			}); err != nil {
				return err
			}
		} else {
			boundary = cursor
			options.Since = cursor.Time.UTC().Format(time.RFC3339Nano)
			options.Tail = ""
		}
	}
	stream, err := s.client.ContainerLogs(ctx, s.containerID, options)
	if err != nil {
		return err
	}
	defer stream.Close()
	if err := emit(logstream.Event{Kind: logstream.EventState, State: "live"}); err != nil {
		return err
	}
	states := make(map[logstream.EntrySource]dockerEntryState)
	boundaryFound := after == "" || boundary.Time.IsZero()
	gapEmitted := boundary.Time.IsZero()
	err = decodeDockerLogStream(stream, s.tty, func(source logstream.EntrySource, raw []byte, continuation bool) error {
		entry, err := s.entry(source, raw, continuation, states)
		if err != nil {
			return err
		}
		if !boundary.Time.IsZero() {
			if entry.Time != nil && entry.Time.Before(boundary.Time) {
				return nil
			}
			if entry.Cursor == after {
				boundaryFound = true
				return nil
			}
			if !boundaryFound && !gapEmitted {
				gapEmitted = true
				if err := emit(logstream.Event{
					Kind: logstream.EventGap, State: "resume_boundary_missing",
					Message: "Docker did not return the previous log boundary; continuity cannot be verified.",
				}); err != nil {
					return err
				}
			}
		}
		return emit(logstream.Event{Kind: logstream.EventEntry, Entry: &entry})
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return err
	}
	if !boundaryFound && !gapEmitted {
		if err := emit(logstream.Event{
			Kind: logstream.EventGap, State: "resume_boundary_missing",
			Message: "Docker did not return the previous log boundary; continuity cannot be verified.",
		}); err != nil {
			return err
		}
	}
	state := "ended"
	if !s.running {
		state = "stopped"
	}
	return emit(logstream.Event{Kind: logstream.EventComplete, State: state})
}

type dockerEntryState struct {
	timestamp time.Time
	segment   uint64
}

func (s *dockerLogSource) entry(
	source logstream.EntrySource,
	raw []byte,
	continuation bool,
	states map[logstream.EntrySource]dockerEntryState,
) (logstream.Entry, error) {
	previous := states[source]
	timestamp, content := parseDockerLogTimestamp(raw)
	if timestamp.IsZero() && continuation {
		timestamp = previous.timestamp
	}
	segment := uint64(0)
	if continuation && !timestamp.IsZero() && timestamp.Equal(previous.timestamp) {
		segment = previous.segment + 1
	}
	states[source] = dockerEntryState{timestamp: timestamp, segment: segment}
	entry := logstream.NewEntry(source, content, continuation)
	if !timestamp.IsZero() {
		timestamp = timestamp.UTC()
		entry.Time = &timestamp
	}
	digestInput := append([]byte(string(source)+"\x00"), content...)
	digestInput = strconv.AppendUint(append(digestInput, 0), segment, 10)
	digest := sha256.Sum256(digestInput)
	cursor, err := logstream.EncodeCursor(logstream.Cursor{
		Kind: "docker", SourceVersion: s.sourceVersion, Time: timestamp,
		Source: source, Digest: hex.EncodeToString(digest[:8]),
	})
	if err != nil {
		return logstream.Entry{}, err
	}
	entry.Cursor = cursor
	return entry, nil
}

func parseDockerLogTimestamp(raw []byte) (time.Time, []byte) {
	separator := bytes.IndexByte(raw, ' ')
	if separator <= 0 {
		return time.Time{}, raw
	}
	timestamp, err := time.Parse(time.RFC3339Nano, string(raw[:separator]))
	if err != nil {
		return time.Time{}, raw
	}
	return timestamp, raw[separator+1:]
}

func decodeDockerLogStream(
	source io.Reader,
	tty bool,
	emit func(logstream.EntrySource, []byte, bool) error,
) error {
	if tty {
		writer := &dockerLogLineWriter{source: logstream.SourceCombined, emit: emit}
		_, err := io.Copy(writer, source)
		if err == nil {
			err = writer.Flush()
		}
		return err
	}
	stdout := &dockerLogLineWriter{source: logstream.SourceStdout, emit: emit}
	stderr := &dockerLogLineWriter{source: logstream.SourceStderr, emit: emit}
	_, err := stdcopy.StdCopy(stdout, stderr, source)
	if err != nil {
		return err
	}
	if err := stdout.Flush(); err != nil {
		return err
	}
	return stderr.Flush()
}

type dockerLogLineWriter struct {
	source       logstream.EntrySource
	pending      []byte
	continuation bool
	emit         func(logstream.EntrySource, []byte, bool) error
}

func (w *dockerLogLineWriter) Write(content []byte) (int, error) {
	w.pending = append(w.pending, content...)
	for len(w.pending) > 0 {
		lineEnd := bytes.IndexByte(w.pending, '\n')
		if lineEnd < 0 && len(w.pending) < logstream.MaxEntryBytes {
			break
		}
		consume, rawEnd := lineEnd+1, lineEnd
		split := lineEnd < 0 || lineEnd >= logstream.MaxEntryBytes
		if split {
			consume, rawEnd = logstream.MaxEntryBytes, logstream.MaxEntryBytes
		}
		raw := w.pending[:rawEnd]
		if lineEnd >= 0 && len(raw) > 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1]
		}
		if err := w.emit(w.source, raw, w.continuation); err != nil {
			return 0, err
		}
		w.pending = w.pending[consume:]
		w.continuation = split
	}
	return len(content), nil
}

func (w *dockerLogLineWriter) Flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	raw := w.pending
	w.pending = nil
	return w.emit(w.source, raw, w.continuation)
}

func dockerLogSourceVersion(containerID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(containerID)))
	return hex.EncodeToString(digest[:16])
}

func resolveDockerLogSource(ctx context.Context, api dockerLogAPI, request LogRequest) (logstream.Source, error) {
	containerID := ""
	technical := request.Application.Technical
	if request.Container != nil {
		containerID = strings.TrimSpace(request.Container.ID)
		if request.Container.Image != "" {
			technical = request.Container.Image
		}
	}
	resolveContext, cancel := context.WithTimeout(ctx, dockerDetailTimeout)
	defer cancel()
	if containerID == "" {
		containers, err := api.ContainerList(resolveContext, client.ContainerListOptions{All: true})
		if err != nil {
			return nil, err
		}
		for _, item := range containers.Items {
			if normalizeContainerName(containerName(item)) == request.Application.Identity {
				containerID = item.ID
				if item.Image != "" {
					technical = item.Image
				}
				break
			}
		}
	}
	if containerID == "" {
		return nil, ErrDockerLogContainerNotFound
	}
	inspection, err := api.ContainerInspect(resolveContext, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}
	actualID := inspection.Container.ID
	if actualID == "" {
		actualID = containerID
	}
	tty := false
	if inspection.Container.Config != nil {
		tty = inspection.Container.Config.Tty
		if inspection.Container.Config.Image != "" {
			technical = inspection.Container.Config.Image
		}
	}
	running := request.Application.Running
	if inspection.Container.State != nil {
		running = inspection.Container.State.Running
	}
	return &dockerLogSource{
		client: api, containerID: actualID, sourceVersion: dockerLogSourceVersion(actualID),
		name: request.Application.Name, technical: technical, tty: tty, running: running,
	}, nil
}

func (c *dockerCollector) LogSource(ctx context.Context, request LogRequest) (logstream.Source, error) {
	if c == nil || c.client == nil {
		return nil, ErrDockerLogContainerNotFound
	}
	return resolveDockerLogSource(ctx, c.client, request)
}

func (p *SystemProbe) LogSource(ctx context.Context, request LogRequest) (logstream.Source, error) {
	if p.dockerError != nil {
		return nil, p.dockerError
	}
	if p.docker == nil {
		return nil, ErrDockerLogContainerNotFound
	}
	return p.docker.LogSource(ctx, request)
}

var _ logstream.Source = (*dockerLogSource)(nil)
var _ LogProbe = (*SystemProbe)(nil)
