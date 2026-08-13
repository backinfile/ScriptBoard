package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/logstream"
	"scriptboard/internal/secretredaction"
)

type liveLogPageView struct {
	Metadata               logstream.Metadata
	BackURL, HistoryURL    string
	EventsURL, DownloadURL string
	Title                  string
	Locale                 webLocale
}

func (a *App) fileLogPage(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	source, err := a.openFileLogSource(request)
	if err != nil {
		writeLogSourceError(response, err)
		return
	}
	metadata := source.Metadata()
	relative := metadata.Name
	parent, _ := hostfiles.Parent(relative)
	values := url.Values{"path": {relative}}
	locale := resolveWebLocale(request)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = liveLogTemplate.Execute(response, liveLogPageView{
		Metadata: metadata, Title: webText(locale, "logs.live_view"), BackURL: filesURL(parent),
		HistoryURL:  "/resources/files/log/history?" + values.Encode(),
		EventsURL:   "/resources/files/log/events?" + values.Encode(),
		DownloadURL: routeFileURL("/resources/files/download", relative),
		Locale:      locale,
	})
}

func (a *App) applicationLogPage(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	source, err := a.openApplicationLogSource(request)
	if err != nil {
		writeApplicationLogPageError(response, err)
		return
	}
	baseURL := "/monitor/applications/" + url.PathEscape(request.PathValue("id")) + "/logs"
	metadata := source.Metadata()
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = liveLogTemplate.Execute(response, liveLogPageView{
		Metadata: metadata, Title: metadata.Name, BackURL: "/monitor/applications",
		HistoryURL: baseURL + "/history", EventsURL: baseURL + "/events",
		Locale: resolveWebLocale(request),
	})
}

func (a *App) applicationLogHistory(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if !acquireLogSlot(a.logHistorySlots) {
		response.Header().Set("Retry-After", "1")
		http.Error(response, "too many concurrent log history reads", http.StatusTooManyRequests)
		return
	}
	defer releaseLogSlot(a.logHistorySlots)
	source, err := a.openApplicationLogSource(request)
	if err != nil {
		writeApplicationLogJSONError(response, err)
		return
	}
	page, err := source.History(request.Context(), request.URL.Query().Get("before"))
	if err != nil {
		writeApplicationLogJSONError(response, err)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(page)
}

func (a *App) applicationLogEvents(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if !acquireLogSlot(a.logStreamSlots) {
		response.Header().Set("Retry-After", "1")
		http.Error(response, "too many concurrent live log streams", http.StatusTooManyRequests)
		return
	}
	defer releaseLogSlot(a.logStreamSlots)
	source, err := a.openApplicationLogSource(request)
	if err != nil {
		writeApplicationLogJSONError(response, err)
		return
	}
	streamLogEvents(response, request, source)
}

func (a *App) openApplicationLogSource(request *http.Request) (logstream.Source, error) {
	return a.applicationStatus.LogSource(request.Context(), request.PathValue("id"))
}

func (a *App) fileLogHistory(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if !acquireLogSlot(a.logHistorySlots) {
		response.Header().Set("Retry-After", "1")
		http.Error(response, "实时日志历史读取过于繁忙", http.StatusTooManyRequests)
		return
	}
	defer releaseLogSlot(a.logHistorySlots)
	source, err := a.openFileLogSource(request)
	if err != nil {
		writeFileLogJSONError(response, err)
		return
	}
	page, err := source.History(request.Context(), request.URL.Query().Get("before"))
	if err != nil {
		writeFileLogJSONError(response, err)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(page)
}

func (a *App) fileLogEvents(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if !acquireLogSlot(a.logStreamSlots) {
		response.Header().Set("Retry-After", "1")
		http.Error(response, "too many concurrent live log streams", http.StatusTooManyRequests)
		return
	}
	defer releaseLogSlot(a.logStreamSlots)
	source, err := a.openFileLogSource(request)
	if err != nil {
		writeFileLogJSONError(response, err)
		return
	}
	streamLogEvents(response, request, source)
}

func streamLogEvents(response http.ResponseWriter, request *http.Request, source logstream.Source) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	flusher.Flush()

	events := make(chan logstream.Event)
	followed := make(chan error, 1)
	after := strings.TrimSpace(request.URL.Query().Get("after"))
	if after == "" {
		after = strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	}
	go func() {
		followed <- source.Follow(request.Context(), after, func(event logstream.Event) error {
			select {
			case events <- event:
				return nil
			case <-request.Context().Done():
				return request.Context().Err()
			}
		})
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event := <-events:
			if err := writeLogSSE(response, event); err != nil {
				return
			}
			flusher.Flush()
		case err := <-followed:
			if err != nil && request.Context().Err() == nil {
				_ = writeLogSSE(response, logstream.Event{
					Kind: logstream.EventState, State: "error", Message: secretredaction.String(err.Error()),
				})
				flusher.Flush()
			}
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(response, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-request.Context().Done():
			return
		}
	}
}

func writeLogSSE(response http.ResponseWriter, event logstream.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.Entry != nil && event.Entry.Cursor != "" {
		if _, err := fmt.Fprintf(response, "id: %s\n", event.Entry.Cursor); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event.Kind, payload); err != nil {
		return err
	}
	return nil
}

func (a *App) openFileLogSource(request *http.Request) (logstream.Source, error) {
	relative := strings.TrimSpace(request.URL.Query().Get("path"))
	if relative == "" || !isTextPreviewExtension(relative) {
		return nil, errUnsupportedLogSource
	}
	return a.hostOpenLogSource(request.Context(), relative)
}

var errUnsupportedLogSource = errors.New("unsupported log source")

func writeLogSourceError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, hostfiles.ErrProtected), os.IsPermission(err):
		status = http.StatusForbidden
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
	case errors.Is(err, hostfiles.ErrLogSourceChanged):
		status = http.StatusGone
	case errors.Is(err, logstream.ErrInvalidCursor):
		status = http.StatusBadRequest
	case errors.Is(err, errUnsupportedLogSource):
		status = http.StatusUnsupportedMediaType
	}
	http.Error(response, secretredaction.String(err.Error()), status)
}

func applicationLogError(err error) (int, string, string) {
	status := http.StatusServiceUnavailable
	code := "log_source_unavailable"
	message := "Application logs are temporarily unavailable"
	switch {
	case errors.Is(err, appstatus.ErrApplicationNotFound),
		errors.Is(err, appstatus.ErrDockerLogContainerNotFound):
		status = http.StatusNotFound
		code = "log_source_not_found"
		message = "The Docker log source is no longer available"
	case errors.Is(err, appstatus.ErrApplicationLogsUnsupported):
		status = http.StatusUnsupportedMediaType
		code = "log_source_unsupported"
		message = "Logs are only available for Docker applications"
	case errors.Is(err, logstream.ErrInvalidCursor):
		status = http.StatusBadRequest
		code = "invalid_log_cursor"
		message = "The log cursor is invalid"
	}
	return status, code, message
}

func writeApplicationLogPageError(response http.ResponseWriter, err error) {
	status, _, message := applicationLogError(err)
	http.Error(response, message, status)
}

func writeApplicationLogJSONError(response http.ResponseWriter, err error) {
	status, code, message := applicationLogError(err)
	writeApplicationJSONError(response, status, code, message)
}

func writeFileLogJSONError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "log_source_unavailable"
	message := "The file log source is unavailable"
	switch {
	case errors.Is(err, hostfiles.ErrProtected), os.IsPermission(err):
		status, code, message = http.StatusForbidden, "log_source_forbidden", "The log file is protected or inaccessible"
	case errors.Is(err, os.ErrNotExist):
		status, code, message = http.StatusNotFound, "log_source_not_found", "The log file does not exist"
	case errors.Is(err, hostfiles.ErrLogSourceChanged):
		status, code, message = http.StatusGone, "log_source_changed", "The old log file is no longer available"
	case errors.Is(err, logstream.ErrInvalidCursor):
		status, code, message = http.StatusBadRequest, "invalid_log_cursor", "The log cursor is invalid"
	case errors.Is(err, errUnsupportedLogSource):
		status, code, message = http.StatusUnsupportedMediaType, "log_source_unsupported", "This file type cannot be followed"
	}
	writeApplicationJSONError(response, status, code, message)
}

func acquireLogSlot(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseLogSlot(slots chan struct{}) {
	<-slots
}
