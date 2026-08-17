package web_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/logstream"
	app "scriptboard/internal/web"
)

func TestFileLogHistoryReturnsTheLatestHostTextLines(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(hostRoot, "service.log"),
		[]byte("ready\nworker WARNING\nrequest ERROR\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	logPath := filepath.Join(hostRoot, "service.log")
	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/log/history", logPath))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	var page logstream.Page
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 ||
		page.Entries[1].Severity != logstream.SeverityWarning ||
		page.Entries[2].Severity != logstream.SeverityError {
		t.Fatalf("page = %#v", page)
	}

	response, err = client.Get(hostFileRequestURL(serverURL, "/resources/files/log/history", logPath) + "&before=invalid")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var invalidCursor struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&invalidCursor); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest ||
		invalidCursor.Error.Code != "invalid_log_cursor" ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid cursor status=%d cache=%q payload=%#v",
			response.StatusCode, response.Header.Get("Cache-Control"), invalidCursor)
	}
}

func TestFileLogPageRendersTheSharedLiveViewerShell(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "service.log"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	logPath := filepath.Join(hostRoot, "service.log")
	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/log", logPath))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(response.Body)
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.StatusCode, response.Header.Get("Cache-Control"), body.String())
	}
	for _, fragment := range []string{
		`data-live-log-viewer`,
		`data-log-history-url="` + hostFileHref("/resources/files/log/history", logPath) + `"`,
		`data-log-events-url="` + hostFileHref("/resources/files/log/events", logPath) + `"`,
		`href="` + hostFileHref("/resources/files/log/download", logPath) + `"`,
		`class="run-log-section live-log-stage"`,
		`data-log-output`,
		`service.log`,
	} {
		if !bytes.Contains(body.Bytes(), []byte(fragment)) {
			t.Fatalf("log viewer is missing %q: %s", fragment, body.String())
		}
	}
	if !bytes.Contains(body.Bytes(), []byte(`<h1>Live view</h1>`)) || bytes.Contains(body.Bytes(), []byte(`<h1>`+logPath+`</h1>`)) {
		t.Fatalf("file log viewer should use a page title instead of repeating the file path: %s", body.String())
	}
	response, err = client.Get(hostFileRequestURL(serverURL, "/resources/files/log/download", logPath))
	if err != nil {
		t.Fatal(err)
	}
	download, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Disposition"), ".txt") || !bytes.Contains(download, []byte("ready")) {
		t.Fatalf("file log TXT download status=%d disposition=%q body=%s", response.StatusCode, response.Header.Get("Content-Disposition"), download)
	}

	response, err = client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	listing, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(listing, []byte(hostFileHref("/resources/files/log", logPath))) ||
		!bytes.Contains(listing, []byte(`data-lucide="scroll-text"`)) {
		t.Fatalf("file listing is missing the live log entry: %s", listing)
	}

	response, err = client.Get(hostFileRequestURL(serverURL, "/resources/files/view", logPath))
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(preview, []byte(hostFileHref("/resources/files/log", logPath))) {
		t.Fatalf("text preview is missing the live log entry: %s", preview)
	}

	response, err = client.Get(serverURL + "/assets/app-v2.js")
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, fragment := range []string{
		`function initLiveLog`,
		`new EventSource`,
		`row.dataset.severity`,
		`message.textContent = entry.text || ""`,
		`MAX_LOG_ENTRIES = 20000`,
		`MAX_LOG_BYTES = 16 * 1024 * 1024`,
	} {
		if !bytes.Contains(script, []byte(fragment)) {
			t.Fatalf("live log script is missing %q", fragment)
		}
	}

	response, err = client.Get(serverURL + "/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, fragment := range []string{
		`.live-log-entry[data-severity="error"]`,
		`.live-log-entry[data-severity="warning"]`,
		`.live-log-entry[data-source="stderr"][data-severity="normal"]`,
	} {
		if !bytes.Contains(styles, []byte(fragment)) {
			t.Fatalf("live log styles are missing %q", fragment)
		}
	}
}

func TestFileLogEventsStreamAppendedEntriesWithCursorIDs(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(hostRoot, "service.log")
	if err := os.WriteFile(logPath, []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/log/events", logPath))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") ||
		response.Header.Get("Cache-Control") != "no-store" {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d content-type=%q cache=%q body=%s",
			response.StatusCode, response.Header.Get("Content-Type"),
			response.Header.Get("Cache-Control"), body)
	}

	reader := bufio.NewReader(response.Body)
	first := readSSEEvent(t, reader, 2*time.Second)
	if first.event != "state" || !strings.Contains(first.data, `"state":"live"`) {
		t.Fatalf("first event = %#v", first)
	}
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("worker WARNING\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	entry := readSSEEvent(t, reader, 2*time.Second)
	if entry.event != "entry" || entry.id == "" ||
		!strings.Contains(entry.data, `"severity":"warning"`) ||
		!strings.Contains(entry.data, `"text":"worker WARNING"`) {
		t.Fatalf("entry event = %#v", entry)
	}
}

func TestDockerLogRoutesUseTheSharedViewerAndRejectHostApplications(t *testing.T) {
	root := t.TempDir()
	source := &fixtureLogSource{
		metadata: logstream.Metadata{
			Kind: "docker", Name: "api-prod", Technical: "example/api:latest",
			SourceVersion: "container-v1", Running: true,
		},
		page: logstream.Page{
			Entries: []logstream.Entry{{
				Cursor: "docker-cursor", Source: logstream.SourceStderr,
				Severity: logstream.SeverityError, Text: "request ERROR",
			}},
			SourceVersion: "container-v1",
		},
	}
	probe := fixtureApplicationLogProbe{
		snapshot: appstatus.RawSnapshot{
			CollectedAt: time.Now().UTC(), DockerAvailable: true,
			Processes: []appstatus.RawProcess{{
				PID: 201, CreatedAt: time.Now().Add(-time.Hour), Name: "Host Agent",
				ExecutablePath: "/opt/host-agent",
			}},
			Containers: []appstatus.RawContainer{{
				ID: "container-123", Name: "api-prod", Image: "example/api:latest",
			}},
		},
		source: source,
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot:        filepath.Join(root, "state"),
		ApplicationProbe: probe,
	})

	response, err := client.Get(serverURL + "/monitor/applications/data")
	if err != nil {
		t.Fatal(err)
	}
	var applications appstatus.View
	if err := json.NewDecoder(response.Body).Decode(&applications); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	var hostID string
	for _, application := range applications.Applications {
		if application.Kind == appstatus.KindHost {
			hostID = application.ID
		}
	}
	if hostID == "" {
		t.Fatalf("applications = %#v", applications.Applications)
	}
	response, err = client.Get(serverURL + "/monitor/containers/data")
	if err != nil {
		t.Fatal(err)
	}
	var containers appstatus.ContainerView
	if err := json.NewDecoder(response.Body).Decode(&containers); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	var dockerID string
	if len(containers.Containers) > 0 {
		dockerID = containers.Containers[0].ApplicationID
	}
	if dockerID == "" {
		t.Fatalf("containers = %#v", containers.Containers)
	}

	response, err = client.Get(serverURL + "/monitor/applications/" + dockerID + "/logs")
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!bytes.Contains(pageBody, []byte(`data-live-log-viewer`)) ||
		!bytes.Contains(pageBody, []byte("/monitor/applications/"+dockerID+"/logs/history")) ||
		!bytes.Contains(pageBody, []byte("/monitor/applications/"+dockerID+"/logs/download")) ||
		!bytes.Contains(pageBody, []byte(`class="run-log-section live-log-stage"`)) {
		t.Fatalf("docker log page status=%d body=%s", response.StatusCode, pageBody)
	}
	response, err = client.Get(serverURL + "/monitor/applications/" + dockerID + "/logs/download")
	if err != nil {
		t.Fatal(err)
	}
	dockerDownload, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Disposition"), ".txt") || !bytes.Contains(dockerDownload, []byte("ERROR")) {
		t.Fatalf("Docker log TXT download status=%d disposition=%q body=%s", response.StatusCode, response.Header.Get("Content-Disposition"), dockerDownload)
	}

	response, err = client.Get(serverURL + "/monitor/containers")
	if err != nil {
		t.Fatal(err)
	}
	containersPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(containersPage, []byte(`data-container-application-id="`+dockerID+`"`)) {
		t.Fatalf("containers page is missing Docker application identity: %s", containersPage)
	}

	response, err = client.Get(serverURL + "/monitor/applications/" + dockerID + "/logs/history")
	if err != nil {
		t.Fatal(err)
	}
	var history logstream.Page
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" ||
		len(history.Entries) != 1 || history.Entries[0].Severity != logstream.SeverityError {
		t.Fatalf("docker history status=%d cache=%q page=%#v",
			response.StatusCode, response.Header.Get("Cache-Control"), history)
	}

	response, err = client.Get(serverURL + "/monitor/applications/" + hostID + "/logs")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("host log status=%d, want %d", response.StatusCode, http.StatusUnsupportedMediaType)
	}
	response, err = client.Get(serverURL + "/monitor/applications/missing/logs")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing application log status=%d", response.StatusCode)
	}
}

func TestLogRoutesRequireAnAuthenticatedSession(t *testing.T) {
	root := t.TempDir()
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, route := range []string{
		"/resources/files/log?path=service.log",
		"/resources/files/log/history?path=service.log",
		"/resources/files/log/events?path=service.log",
		"/monitor/applications/example/logs",
		"/monitor/applications/example/logs/history",
		"/monitor/applications/example/logs/events",
	} {
		response, err := client.Get(server.URL + route)
		if err != nil {
			t.Fatalf("GET %s: %v", route, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
			t.Fatalf("GET %s status=%d location=%q",
				route, response.StatusCode, response.Header.Get("Location"))
		}
	}
}

func TestLiveLogStreamLimitRejectsTheNinthConnection(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "service.log"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	responses := make([]*http.Response, 0, 8)
	for index := 0; index < 8; index++ {
		response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/log/events", filepath.Join(hostRoot, "service.log")))
		if err != nil {
			t.Fatalf("open stream %d: %v", index+1, err)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			t.Fatalf("stream %d status=%d", index+1, response.StatusCode)
		}
		responses = append(responses, response)
	}
	defer func() {
		for _, response := range responses {
			_ = response.Body.Close()
		}
	}()

	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/log/events", filepath.Join(hostRoot, "service.log")))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "1" {
		t.Fatalf("ninth stream status=%d retry-after=%q",
			response.StatusCode, response.Header.Get("Retry-After"))
	}
}

func TestLogHistoryLimitRejectsTheFifthConcurrentRead(t *testing.T) {
	root := t.TempDir()
	source := &blockingHistoryLogSource{
		metadata: logstream.Metadata{
			Kind: "docker", Name: "api-prod", SourceVersion: "container-v1", Running: true,
		},
		started: make(chan struct{}, 4),
		release: make(chan struct{}),
	}
	probe := fixtureApplicationLogProbe{
		snapshot: appstatus.RawSnapshot{
			CollectedAt: time.Now().UTC(), DockerAvailable: true,
			Containers: []appstatus.RawContainer{{
				ID: "container-123", Name: "api-prod", Image: "example/api:latest",
			}},
		},
		source: source,
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot:        filepath.Join(root, "state"),
		ApplicationProbe: probe,
	})
	response, err := client.Get(serverURL + "/monitor/containers/data")
	if err != nil {
		t.Fatal(err)
	}
	var containers appstatus.ContainerView
	if err := json.NewDecoder(response.Body).Decode(&containers); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(containers.Containers) != 1 {
		t.Fatalf("containers = %#v", containers.Containers)
	}
	historyURL := serverURL + "/monitor/applications/" + containers.Containers[0].ApplicationID + "/logs/history"
	type result struct {
		response *http.Response
		err      error
	}
	results := make(chan result, 4)
	for index := 0; index < 4; index++ {
		go func() {
			response, err := client.Get(historyURL)
			results <- result{response: response, err: err}
		}()
	}
	for index := 0; index < 4; index++ {
		select {
		case <-source.started:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for history slots to fill")
		}
	}

	response, err = client.Get(historyURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "1" {
		t.Fatalf("fifth history status=%d retry-after=%q",
			response.StatusCode, response.Header.Get("Retry-After"))
	}
	close(source.release)
	for index := 0; index < 4; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("history request: %v", result.err)
		}
		_ = result.response.Body.Close()
		if result.response.StatusCode != http.StatusOK {
			t.Fatalf("history status=%d", result.response.StatusCode)
		}
	}
}

type fixtureApplicationLogProbe struct {
	snapshot appstatus.RawSnapshot
	source   logstream.Source
}

func (probe fixtureApplicationLogProbe) Snapshot(context.Context) appstatus.RawSnapshot {
	return probe.snapshot
}

func (probe fixtureApplicationLogProbe) LogSource(
	context.Context,
	appstatus.LogRequest,
) (logstream.Source, error) {
	return probe.source, nil
}

type fixtureLogSource struct {
	metadata logstream.Metadata
	page     logstream.Page
}

func (source *fixtureLogSource) Metadata() logstream.Metadata {
	return source.metadata
}

func (source *fixtureLogSource) History(context.Context, string) (logstream.Page, error) {
	return source.page, nil
}

func (source *fixtureLogSource) Follow(
	_ context.Context,
	_ string,
	emit func(logstream.Event) error,
) error {
	if err := emit(logstream.Event{Kind: logstream.EventState, State: "live"}); err != nil {
		return err
	}
	return emit(logstream.Event{Kind: logstream.EventComplete, State: "ended"})
}

type blockingHistoryLogSource struct {
	metadata logstream.Metadata
	started  chan struct{}
	release  chan struct{}
}

func (source *blockingHistoryLogSource) Metadata() logstream.Metadata {
	return source.metadata
}

func (source *blockingHistoryLogSource) History(
	ctx context.Context,
	_ string,
) (logstream.Page, error) {
	source.started <- struct{}{}
	select {
	case <-source.release:
		return logstream.Page{SourceVersion: source.metadata.SourceVersion}, nil
	case <-ctx.Done():
		return logstream.Page{}, ctx.Err()
	}
}

func (source *blockingHistoryLogSource) Follow(
	ctx context.Context,
	_ string,
	_ func(logstream.Event) error,
) error {
	<-ctx.Done()
	return ctx.Err()
}

type sseEvent struct {
	event string
	id    string
	data  string
}

func readSSEEvent(t *testing.T, reader *bufio.Reader, timeout time.Duration) sseEvent {
	t.Helper()
	result := make(chan sseEvent, 1)
	failures := make(chan error, 1)
	go func() {
		var event sseEvent
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				failures <- err
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				result <- event
				return
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				event.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "id: "):
				event.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				event.data += strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	select {
	case event := <-result:
		return event
	case err := <-failures:
		t.Fatalf("read SSE event: %v", err)
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE event")
	}
	return sseEvent{}
}
