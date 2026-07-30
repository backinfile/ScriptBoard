package appstatus

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"scriptboard/internal/logstream"
)

type fakeContainerLogsClient struct {
	content []byte
	options client.ContainerLogsOptions
	err     error
}

type fakeDockerLogAPI struct {
	fakeContainerLogsClient
	list        client.ContainerListResult
	inspection  client.ContainerInspectResult
	listOptions client.ContainerListOptions
	inspectID   string
}

func (f *fakeDockerLogAPI) ContainerList(
	_ context.Context,
	options client.ContainerListOptions,
) (client.ContainerListResult, error) {
	f.listOptions = options
	return f.list, nil
}

func (f *fakeDockerLogAPI) ContainerInspect(
	_ context.Context,
	containerID string,
	_ client.ContainerInspectOptions,
) (client.ContainerInspectResult, error) {
	f.inspectID = containerID
	return f.inspection, nil
}

func (f *fakeContainerLogsClient) ContainerLogs(
	_ context.Context,
	_ string,
	options client.ContainerLogsOptions,
) (client.ContainerLogsResult, error) {
	f.options = options
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.content)), nil
}

func TestDockerLogSourceDemultiplexesHistoryAndClassifiesSeverity(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	writeDockerFrame(&stream, stdcopy.Stdout, []byte("2026-07-30T10:00:00.000000001Z cache warning\n"))
	writeDockerFrame(&stream, stdcopy.Stderr, []byte("2026-07-30T10:00:01.000000002Z request ERROR\n"))
	writeDockerFrame(&stream, stdcopy.Stderr, []byte("2026-07-30T10:00:02.000000003Z diagnostic details\n"))
	api := &fakeContainerLogsClient{content: stream.Bytes()}
	source := &dockerLogSource{
		client: api, containerID: "container-123", sourceVersion: "version-123",
		name: "api", technical: "example/api", tty: false, running: true,
	}

	page, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("entries = %#v", page.Entries)
	}
	if page.Entries[0].Source != logstream.SourceStdout ||
		page.Entries[0].Severity != logstream.SeverityWarning ||
		page.Entries[0].Text != "cache warning" {
		t.Fatalf("stdout entry = %#v", page.Entries[0])
	}
	if page.Entries[1].Source != logstream.SourceStderr ||
		page.Entries[1].Severity != logstream.SeverityError ||
		page.Entries[1].Text != "request ERROR" {
		t.Fatalf("stderr entry = %#v", page.Entries[1])
	}
	if page.Entries[2].Source != logstream.SourceStderr ||
		page.Entries[2].Severity != logstream.SeverityNormal {
		t.Fatalf("ordinary stderr entry = %#v", page.Entries[2])
	}
	if page.Entries[0].Time == nil ||
		!page.Entries[0].Time.Equal(time.Date(2026, 7, 30, 10, 0, 0, 1, time.UTC)) {
		t.Fatalf("stdout time = %v", page.Entries[0].Time)
	}
	if !api.options.ShowStdout || !api.options.ShowStderr || !api.options.Timestamps ||
		api.options.Follow || api.options.Tail != "500" {
		t.Fatalf("ContainerLogs options = %#v", api.options)
	}
}

func TestDockerLogSourceResumesWithSinceAndDeduplicatesTheBoundary(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	writeDockerFrame(&stream, stdcopy.Stdout, []byte("2026-07-30T10:00:00.000000001Z first\n"))
	writeDockerFrame(&stream, stdcopy.Stdout, []byte("2026-07-30T10:00:01.000000002Z second warn\n"))
	api := &fakeContainerLogsClient{content: stream.Bytes()}
	source := &dockerLogSource{
		client: api, containerID: "container-123", sourceVersion: "version-123",
		name: "api", tty: false, running: true,
	}
	page, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	var events []logstream.Event
	if err := source.Follow(context.Background(), page.Entries[0].Cursor, func(event logstream.Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != logstream.EventState ||
		events[1].Entry == nil || events[1].Entry.Text != "second warn" ||
		events[1].Entry.Severity != logstream.SeverityWarning ||
		events[2].Kind != logstream.EventComplete {
		t.Fatalf("resume events = %#v", events)
	}
	if api.options.Since != "2026-07-30T10:00:00.000000001Z" ||
		api.options.Tail != "" || !api.options.Follow {
		t.Fatalf("resume options = %#v", api.options)
	}

	older, err := source.History(context.Background(), page.Entries[1].Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 1 || older.Entries[0].Text != "first" {
		t.Fatalf("older entries = %#v, want only the entry before the boundary", older.Entries)
	}
	if api.options.Until != "2026-07-30T10:00:01.000000003Z" ||
		api.options.Tail != "500" || api.options.Follow {
		t.Fatalf("history options = %#v", api.options)
	}

	invalidBoundary, err := logstream.EncodeCursor(logstream.Cursor{
		Kind: "docker", SourceVersion: "version-123",
		Time: *page.Entries[1].Time, Source: logstream.SourceStdout, Digest: "not-the-boundary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.History(context.Background(), invalidBoundary); !errors.Is(err, logstream.ErrInvalidCursor) {
		t.Fatalf("invalid history boundary error = %v, want ErrInvalidCursor", err)
	}
}

func TestDockerLogSourceReportsAGapWhenTheContainerVersionChanges(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	writeDockerFrame(&stream, stdcopy.Stdout, []byte("2026-07-30T10:00:03Z replacement ready\n"))
	api := &fakeContainerLogsClient{content: stream.Bytes()}
	source := &dockerLogSource{
		client: api, containerID: "replacement", sourceVersion: "replacement-version",
		name: "api", tty: false, running: true,
	}
	cursor, err := logstream.EncodeCursor(logstream.Cursor{
		Kind: "docker", SourceVersion: "old-version",
		Time:   time.Date(2026, 7, 30, 9, 59, 59, 0, time.UTC),
		Source: logstream.SourceStdout, Digest: "old",
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []logstream.Event
	if err := source.Follow(context.Background(), cursor, func(event logstream.Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 || events[0].Kind != logstream.EventGap ||
		events[0].State != "source_changed" || events[1].Kind != logstream.EventState ||
		events[2].Entry == nil || events[2].Entry.Text != "replacement ready" {
		t.Fatalf("replacement events = %#v", events)
	}
	if api.options.Since != "" || api.options.Tail != "500" {
		t.Fatalf("replacement options = %#v", api.options)
	}
}

func TestDockerLogSourceReturnsLogDriverErrorsWithoutFallback(t *testing.T) {
	t.Parallel()

	want := errors.New("configured logging driver does not support reading")
	source := &dockerLogSource{
		client:      &fakeContainerLogsClient{err: want},
		containerID: "container-123", sourceVersion: "version-123",
	}
	if _, err := source.History(context.Background(), ""); !errors.Is(err, want) {
		t.Fatalf("history error = %v, want %v", err, want)
	}
}

func TestDockerLogSourceKeepsTimestampedCursorsOnLongLineContinuations(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	writeDockerFrame(
		&stream,
		stdcopy.Stdout,
		[]byte("2026-07-30T10:00:00.000000001Z "+strings.Repeat("x", 2*logstream.MaxEntryBytes+17)+"\n"),
	)
	source := &dockerLogSource{
		client:      &fakeContainerLogsClient{content: stream.Bytes()},
		containerID: "container-123", sourceVersion: "version-123",
	}
	page, err := source.History(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 || page.Entries[0].Time == nil || page.Entries[1].Time == nil ||
		page.Entries[2].Time == nil ||
		!page.Entries[0].Time.Equal(*page.Entries[1].Time) ||
		!page.Entries[0].Time.Equal(*page.Entries[2].Time) ||
		!page.Entries[1].Continuation || !page.Entries[2].Continuation ||
		page.Entries[1].Cursor == page.Entries[2].Cursor {
		lengths := make([]int, len(page.Entries))
		for index := range page.Entries {
			lengths[index] = len(page.Entries[index].Text)
		}
		t.Fatalf("long Docker entries count=%d lengths=%v times=%v",
			len(page.Entries), lengths, func() []bool {
				values := make([]bool, len(page.Entries))
				for index := range page.Entries {
					values[index] = page.Entries[index].Time != nil
				}
				return values
			}())
	}
	cursor, err := logstream.DecodeCursor(page.Entries[1].Cursor)
	if err != nil || cursor.Time.IsZero() {
		t.Fatalf("continuation cursor = %#v, err=%v", cursor, err)
	}
}

func writeDockerFrame(destination *bytes.Buffer, source stdcopy.StdType, content []byte) {
	var header [8]byte
	header[0] = byte(source)
	binary.BigEndian.PutUint32(header[4:], uint32(len(content)))
	_, _ = destination.Write(header[:])
	_, _ = destination.Write(content)
}

func TestResolveDockerLogSourceFindsAStoppedContainerByVisibleApplicationName(t *testing.T) {
	t.Parallel()

	api := &fakeDockerLogAPI{
		list: client.ContainerListResult{Items: []containertypes.Summary{{
			ID: "stopped-123", Names: []string{"/api"}, Image: "example/api:latest",
		}}},
		inspection: client.ContainerInspectResult{Container: containertypes.InspectResponse{
			ID: "stopped-123", State: &containertypes.State{Running: false},
			Config: &containertypes.Config{Tty: true, Image: "example/api:latest"},
		}},
	}
	source, err := resolveDockerLogSource(context.Background(), api, LogRequest{
		Application: Application{
			Kind: KindDocker, Name: "api", Identity: "api", Technical: "example/api:latest",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := source.Metadata()
	if !api.listOptions.All || api.inspectID != "stopped-123" ||
		metadata.Name != "api" || metadata.Running || metadata.Kind != "docker" {
		t.Fatalf("resolution list=%#v inspect=%q metadata=%#v", api.listOptions, api.inspectID, metadata)
	}
}

func TestDockerLogSourceFollowsTTYOutputAndCompletesAStoppedContainer(t *testing.T) {
	t.Parallel()

	api := &fakeContainerLogsClient{
		content: []byte("2026-07-30T10:00:02.000000003Z worker panic\n"),
	}
	source := &dockerLogSource{
		client: api, containerID: "container-tty", sourceVersion: "version-tty",
		name: "worker", tty: true, running: false,
	}
	var events []logstream.Event
	err := source.Follow(context.Background(), "", func(event logstream.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != logstream.EventState ||
		events[1].Kind != logstream.EventEntry || events[1].Entry == nil ||
		events[1].Entry.Source != logstream.SourceCombined ||
		events[1].Entry.Severity != logstream.SeverityError ||
		events[2].Kind != logstream.EventComplete || events[2].State != "stopped" {
		t.Fatalf("events = %#v", events)
	}
	if !api.options.Follow || api.options.Tail != "500" || !api.options.Timestamps {
		t.Fatalf("ContainerLogs options = %#v", api.options)
	}
}
