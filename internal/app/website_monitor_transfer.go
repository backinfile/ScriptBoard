package app

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/secretredaction"
	"scriptboard/internal/websitemonitor"
)

const (
	websiteMonitorConfigFormat  = "scriptboard.website-monitors"
	websiteMonitorConfigVersion = 1
	websiteMonitorImportMaxSize = 128 << 20
)

type websiteMonitorConfigFile struct {
	Format     string                       `json:"format"`
	Version    int                          `json:"version"`
	ExportedAt time.Time                    `json:"exported_at"`
	Monitors   []websiteMonitorConfigRecord `json:"monitors"`
}

type websiteMonitorConfigRecord struct {
	Name                 string                           `json:"name"`
	Scope                websitemonitor.Scope             `json:"scope"`
	Kind                 websitemonitor.Kind              `json:"kind"`
	URL                  string                           `json:"url"`
	FrequencySeconds     int64                            `json:"frequency_seconds"`
	TimeoutSeconds       int64                            `json:"timeout_seconds"`
	HTTPMethod           string                           `json:"http_method,omitempty"`
	HTTPContentType      string                           `json:"http_content_type,omitempty"`
	HTTPBody             string                           `json:"http_body,omitempty"`
	RequestHeaders       []websitemonitor.RequestHeader   `json:"request_headers,omitempty"`
	HTTPSuccessMode      websitemonitor.HTTPSuccessMode   `json:"http_success_mode,omitempty"`
	ExpectedStatusRanges []websitemonitor.HTTPStatusRange `json:"expected_status_ranges,omitempty"`
	ResponseKeyword      string                           `json:"response_keyword,omitempty"`
	FollowRedirects      bool                             `json:"follow_redirects,omitempty"`
	VerifyTLS            bool                             `json:"verify_tls"`
	WebSocketSuccess     websitemonitor.WebSocketSuccess  `json:"websocket_success,omitempty"`
	SendType             websitemonitor.MessageType       `json:"send_type,omitempty"`
	SendPayload          string                           `json:"send_payload,omitempty"`
	ReceiveType          websitemonitor.MessageType       `json:"receive_type,omitempty"`
	ExpectedMessage      string                           `json:"expected_message,omitempty"`
	PingPayloadFormat    websitemonitor.PayloadFormat     `json:"ping_payload_format,omitempty"`
	PingPayload          string                           `json:"ping_payload,omitempty"`
}

type websiteMonitorTransferCandidate struct {
	Selection  string
	Name       string
	URL        string
	KindLabel  string
	ScopeLabel string
	Duplicate  bool
}

type websiteMonitorTransferView struct {
	Locale        webLocale
	CSRFToken     string
	Mode          string
	Preview       bool
	PreviewToken  string
	Candidates    []websiteMonitorTransferCandidate
	HasSelectable bool
	Error         string
	ImportedCount int
}

type websiteMonitorImportPreviewFile struct {
	SessionHash string                   `json:"session_hash"`
	CreatedAt   time.Time                `json:"created_at"`
	Bundle      websiteMonitorConfigFile `json:"bundle"`
}

func websiteMonitorConfigRecordFromConfig(config websitemonitor.Config) websiteMonitorConfigRecord {
	return websiteMonitorConfigRecord{
		Name: config.Name, Scope: config.Scope, Kind: config.Kind, URL: config.URL,
		FrequencySeconds: int64(config.Frequency / time.Second),
		TimeoutSeconds:   int64(config.Timeout / time.Second),
		HTTPMethod:       config.HTTPMethod, HTTPContentType: config.HTTPContentType, HTTPBody: config.HTTPBody,
		RequestHeaders:       config.RequestHeaders,
		HTTPSuccessMode:      config.HTTPSuccessMode,
		ExpectedStatusRanges: websitemonitor.ExpectedHTTPStatusRanges(config),
		ResponseKeyword:      config.ResponseKeyword, FollowRedirects: !config.DisableRedirects,
		VerifyTLS:        !config.SkipTLSVerification,
		WebSocketSuccess: config.WebSocketSuccess, SendType: config.SendType, SendPayload: config.SendPayload,
		ReceiveType: config.ReceiveType, ExpectedMessage: config.ExpectedMessage,
		PingPayloadFormat: config.PingPayloadFormat, PingPayload: config.PingPayload,
	}
}

func (record websiteMonitorConfigRecord) config() websitemonitor.Config {
	return websitemonitor.Config{
		Name: record.Name, Scope: record.Scope, Kind: record.Kind, URL: record.URL,
		Frequency:  time.Duration(record.FrequencySeconds) * time.Second,
		Timeout:    time.Duration(record.TimeoutSeconds) * time.Second,
		HTTPMethod: record.HTTPMethod, HTTPContentType: record.HTTPContentType, HTTPBody: record.HTTPBody,
		RequestHeaders:  record.RequestHeaders,
		HTTPSuccessMode: record.HTTPSuccessMode, ExpectedStatusRanges: record.ExpectedStatusRanges,
		ResponseKeyword: record.ResponseKeyword, DisableRedirects: !record.FollowRedirects,
		SkipTLSVerification: !record.VerifyTLS,
		WebSocketSuccess:    record.WebSocketSuccess, SendType: record.SendType, SendPayload: record.SendPayload,
		ReceiveType: record.ReceiveType, ExpectedMessage: record.ExpectedMessage,
		PingPayloadFormat: record.PingPayloadFormat, PingPayload: record.PingPayload,
		Source: "manual",
	}
}

func (a *App) websiteMonitorExportTask(response http.ResponseWriter, request *http.Request) {
	monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "website.error.read_monitors"), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	view := websiteMonitorTransferView{Locale: locale, CSRFToken: current.csrfToken, Mode: "export"}
	for _, monitor := range monitors {
		view.Candidates = append(view.Candidates, websiteMonitorTransferCandidate{
			Selection: monitor.ID, Name: monitor.Config.Name, URL: monitor.Config.URL,
			KindLabel:  websiteKindLabel(locale, monitor.Config.Kind),
			ScopeLabel: websiteScopeLabel(locale, monitor.Config.Scope),
		})
	}
	view.HasSelectable = len(view.Candidates) > 0
	renderWebsiteMonitorTransfer(response, http.StatusOK, view)
}

func (a *App) exportWebsiteMonitors(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	_ = request.ParseForm()
	selected := request.Form["selection"]
	if len(selected) == 0 {
		a.renderWebsiteMonitorExportError(response, request, webText(locale, "website.transfer.selection_required"))
		return
	}
	requested := make(map[string]struct{}, len(selected))
	for _, id := range selected {
		requested[id] = struct{}{}
	}
	monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		http.Error(response, webText(locale, "website.error.read_monitors"), http.StatusInternalServerError)
		return
	}
	bundle := websiteMonitorConfigFile{
		Format: websiteMonitorConfigFormat, Version: websiteMonitorConfigVersion,
		ExportedAt: time.Now().UTC(),
	}
	for _, monitor := range monitors {
		if _, ok := requested[monitor.ID]; ok {
			bundle.Monitors = append(bundle.Monitors, websiteMonitorConfigRecordFromConfig(monitor.Config))
			delete(requested, monitor.ID)
		}
	}
	if len(requested) != 0 || len(bundle.Monitors) == 0 {
		a.renderWebsiteMonitorExportError(response, request, webText(locale, "website.transfer.invalid_selection"))
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scriptboard-website-monitors-%s.json"`, time.Now().Format("20060102-150405")))
	encoded, err := secretredaction.MarshalJSON(bundle)
	if err != nil {
		return
	}
	if _, err := response.Write(append(encoded, '\n')); err != nil {
		return
	}
	a.recordAuditForRequest(request, "export_website_monitors", fmt.Sprintf("%d monitors", len(bundle.Monitors)), "succeeded")
}

func (a *App) renderWebsiteMonitorExportError(response http.ResponseWriter, request *http.Request, message string) {
	monitors, _ := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	view := websiteMonitorTransferView{Locale: locale, CSRFToken: current.csrfToken, Mode: "export", Error: message}
	for _, monitor := range monitors {
		view.Candidates = append(view.Candidates, websiteMonitorTransferCandidate{
			Selection: monitor.ID, Name: monitor.Config.Name, URL: monitor.Config.URL,
			KindLabel: websiteKindLabel(locale, monitor.Config.Kind), ScopeLabel: websiteScopeLabel(locale, monitor.Config.Scope),
		})
	}
	view.HasSelectable = len(view.Candidates) > 0
	renderWebsiteMonitorTransfer(response, http.StatusUnprocessableEntity, view)
}

func (a *App) websiteMonitorImportTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	view := websiteMonitorTransferView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Mode: "import"}
	if imported, err := strconv.Atoi(request.URL.Query().Get("imported")); err == nil && imported > 0 && imported <= 100 {
		view.ImportedCount = imported
	}
	renderWebsiteMonitorTransfer(response, http.StatusOK, view)
}

func (a *App) previewWebsiteMonitorImport(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, websiteMonitorImportMaxSize+(1<<20))
	locale := resolveWebLocale(request)
	current := request.Context().Value(sessionContextKey).(session)
	base := websiteMonitorTransferView{Locale: locale, CSRFToken: current.csrfToken, Mode: "import"}
	if err := request.ParseMultipartForm(8 << 20); err != nil {
		status := http.StatusUnprocessableEntity
		base.Error = webText(locale, "website.transfer.invalid_file")
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
			base.Error = webText(locale, "website.transfer.file_too_large")
		}
		renderWebsiteMonitorTransfer(response, status, base)
		return
	}
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	file, _, err := request.FormFile("config_file")
	if err != nil {
		base.Error = webText(locale, "website.transfer.file_required")
		renderWebsiteMonitorTransfer(response, http.StatusUnprocessableEntity, base)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, websiteMonitorImportMaxSize+1))
	if err != nil || len(raw) > websiteMonitorImportMaxSize {
		base.Error = webText(locale, "website.transfer.file_too_large")
		renderWebsiteMonitorTransfer(response, http.StatusRequestEntityTooLarge, base)
		return
	}
	bundle, err := decodeWebsiteMonitorConfigFile(raw)
	if err != nil {
		base.Error = fmt.Sprintf("%s: %v", webText(locale, "website.transfer.invalid_file"), err)
		renderWebsiteMonitorTransfer(response, http.StatusUnprocessableEntity, base)
		return
	}
	previewToken, err := a.storeWebsiteMonitorImportPreview(current, bundle)
	if err != nil {
		http.Error(response, webText(locale, "website.transfer.preview_store_failed"), http.StatusInternalServerError)
		return
	}
	existing, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		http.Error(response, webText(locale, "website.error.read_monitors"), http.StatusInternalServerError)
		return
	}
	existingNames := make(map[string]struct{}, len(existing))
	for _, monitor := range existing {
		existingNames[strings.ToLower(monitor.Config.Name)] = struct{}{}
	}
	base.Preview = true
	base.PreviewToken = previewToken
	for index, record := range bundle.Monitors {
		_, duplicate := existingNames[strings.ToLower(record.Name)]
		base.Candidates = append(base.Candidates, websiteMonitorTransferCandidate{
			Selection: strconv.Itoa(index), Name: record.Name, URL: record.URL,
			KindLabel: websiteKindLabel(locale, record.Kind), ScopeLabel: websiteScopeLabel(locale, record.Scope),
			Duplicate: duplicate,
		})
		base.HasSelectable = base.HasSelectable || !duplicate
	}
	renderWebsiteMonitorTransfer(response, http.StatusOK, base)
}

func (a *App) importWebsiteMonitors(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	_ = request.ParseForm()
	current := request.Context().Value(sessionContextKey).(session)
	previewToken := request.FormValue("preview_token")
	bundle, err := a.loadWebsiteMonitorImportPreview(current, previewToken)
	if err != nil {
		http.Error(response, webText(locale, "website.transfer.preview_expired"), http.StatusUnprocessableEntity)
		return
	}
	selected := request.Form["selection"]
	if len(selected) == 0 {
		a.renderWebsiteMonitorImportPreviewError(response, request, bundle, previewToken, webText(locale, "website.transfer.selection_required"))
		return
	}
	seen := make(map[int]struct{}, len(selected))
	configs := make([]websitemonitor.Config, 0, len(selected))
	for _, value := range selected {
		index, parseErr := strconv.Atoi(value)
		if parseErr != nil || index < 0 || index >= len(bundle.Monitors) {
			a.renderWebsiteMonitorImportPreviewError(response, request, bundle, previewToken, webText(locale, "website.transfer.invalid_selection"))
			return
		}
		if _, duplicate := seen[index]; duplicate {
			continue
		}
		seen[index] = struct{}{}
		configs = append(configs, bundle.Monitors[index].config())
	}
	imported, err := a.websiteMonitor.CreateMany(request.Context(), configs)
	if err != nil {
		a.renderWebsiteMonitorImportPreviewError(response, request, bundle, previewToken, err.Error())
		return
	}
	a.removeWebsiteMonitorImportPreview(previewToken)
	a.recordAuditForRequest(request, "import_website_monitors", fmt.Sprintf("%d monitors", len(imported)), "succeeded")
	http.Redirect(response, request, fmt.Sprintf("/monitor/websites/import?imported=%d", len(imported)), http.StatusSeeOther)
}

func (a *App) renderWebsiteMonitorImportPreviewError(response http.ResponseWriter, request *http.Request, bundle websiteMonitorConfigFile, previewToken, message string) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	view := websiteMonitorTransferView{Locale: locale, CSRFToken: current.csrfToken, Mode: "import", Preview: true, PreviewToken: previewToken, Error: message}
	existing, _ := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	existingNames := make(map[string]struct{}, len(existing))
	for _, monitor := range existing {
		existingNames[strings.ToLower(monitor.Config.Name)] = struct{}{}
	}
	for index, record := range bundle.Monitors {
		_, duplicate := existingNames[strings.ToLower(record.Name)]
		view.Candidates = append(view.Candidates, websiteMonitorTransferCandidate{
			Selection: strconv.Itoa(index), Name: record.Name, URL: record.URL,
			KindLabel: websiteKindLabel(locale, record.Kind), ScopeLabel: websiteScopeLabel(locale, record.Scope), Duplicate: duplicate,
		})
		view.HasSelectable = view.HasSelectable || !duplicate
	}
	renderWebsiteMonitorTransfer(response, http.StatusUnprocessableEntity, view)
}

func (a *App) storeWebsiteMonitorImportPreview(current session, bundle websiteMonitorConfigFile) (string, error) {
	root := filepath.Join(a.stateRoot, "website-monitor-imports")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	entries, _ := os.ReadDir(root)
	cutoff := time.Now().UTC().Add(-time.Hour)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(root, entry.Name()))
		}
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	payload, err := json.Marshal(websiteMonitorImportPreviewFile{
		SessionHash: current.tokenHash, CreatedAt: time.Now().UTC(), Bundle: bundle,
	})
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(filepath.Join(root, token+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(filepath.Join(root, token+".json"))
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(filepath.Join(root, token+".json"))
		return "", err
	}
	return token, nil
}

func (a *App) loadWebsiteMonitorImportPreview(current session, token string) (websiteMonitorConfigFile, error) {
	var empty websiteMonitorConfigFile
	if len(token) != 64 {
		return empty, errors.New("invalid preview token")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return empty, errors.New("invalid preview token")
	}
	path := filepath.Join(a.stateRoot, "website-monitor-imports", token+".json")
	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer file.Close()
	var preview websiteMonitorImportPreviewFile
	if err := json.NewDecoder(io.LimitReader(file, websiteMonitorImportMaxSize+(1<<20))).Decode(&preview); err != nil {
		return empty, err
	}
	if time.Since(preview.CreatedAt) > time.Hour || preview.CreatedAt.After(time.Now().UTC().Add(time.Minute)) {
		return empty, errors.New("expired preview")
	}
	if subtle.ConstantTimeCompare([]byte(preview.SessionHash), []byte(current.tokenHash)) != 1 {
		return empty, errors.New("preview belongs to another session")
	}
	return preview.Bundle, nil
}

func (a *App) removeWebsiteMonitorImportPreview(token string) {
	if len(token) != 64 {
		return
	}
	if _, err := hex.DecodeString(token); err != nil {
		return
	}
	_ = os.Remove(filepath.Join(a.stateRoot, "website-monitor-imports", token+".json"))
}

func decodeWebsiteMonitorConfigFile(raw []byte) (websiteMonitorConfigFile, error) {
	var bundle websiteMonitorConfigFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return bundle, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return bundle, err
	}
	if bundle.Format != websiteMonitorConfigFormat || bundle.Version != websiteMonitorConfigVersion {
		return bundle, errors.New("unsupported format or version")
	}
	if len(bundle.Monitors) == 0 || len(bundle.Monitors) > 100 {
		return bundle, errors.New("the file must contain between 1 and 100 monitors")
	}
	names := make(map[string]struct{}, len(bundle.Monitors))
	for index := range bundle.Monitors {
		normalized, err := websitemonitor.ValidateConfig(bundle.Monitors[index].config())
		if err != nil {
			return bundle, fmt.Errorf("monitor %d: %w", index+1, err)
		}
		bundle.Monitors[index] = websiteMonitorConfigRecordFromConfig(normalized)
		key := strings.ToLower(normalized.Name)
		if _, duplicate := names[key]; duplicate {
			return bundle, fmt.Errorf("duplicate monitor name %q", normalized.Name)
		}
		names[key] = struct{}{}
	}
	return bundle, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("unexpected data after the configuration file")
}

func renderWebsiteMonitorTransfer(response http.ResponseWriter, status int, view websiteMonitorTransferView) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = websiteTransferTemplate.Execute(response, view)
}
