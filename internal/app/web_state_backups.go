package app

import (
	"net/http"
	"path/filepath"
	"strings"

	"scriptboard/internal/statebackup"
)

const maximumStateBackupPassphraseBytes = 4096

type stateBackupsPageData struct {
	Locale             webLocale
	CSRFToken          string
	SettingsNavigation settingsNavigationData
	Available          bool
	Stages             []statebackup.Stage
	Artifact           *statebackup.Artifact
	Manifest           *statebackup.Manifest
	Stage              *statebackup.Stage
	Notice             string
	Error              string
}

func (a *App) stateBackupsPage(response http.ResponseWriter, request *http.Request) {
	a.renderStateBackupsPage(response, request, stateBackupsPageData{}, http.StatusOK)
}

func (a *App) createStateBackup(response http.ResponseWriter, request *http.Request) {
	data := stateBackupsPageData{}
	if !validSessionCSRF(request) {
		data.Error = webText(resolveWebLocale(request), "state_backups.csrf_error")
		a.renderStateBackupsPage(response, request, data, http.StatusForbidden)
		return
	}
	destination := strings.TrimSpace(request.FormValue("destination"))
	passphrase, ok := stateBackupPassphrase(request.FormValue("passphrase"))
	if !ok || request.FormValue("passphrase") != request.FormValue("passphrase_confirm") || !validStateBackupPath(destination) {
		clearStateBackupSecret(passphrase)
		data.Error = webText(resolveWebLocale(request), "state_backups.input_error")
		a.renderStateBackupsPage(response, request, data, http.StatusBadRequest)
		return
	}
	defer clearStateBackupSecret(passphrase)
	if a.stateBackups == nil {
		data.Error = webText(resolveWebLocale(request), "state_backups.unavailable")
		a.renderStateBackupsPage(response, request, data, http.StatusServiceUnavailable)
		return
	}
	artifact, err := a.stateBackups.Create(request.Context(), destination, passphrase)
	if err != nil {
		data.Error = webText(resolveWebLocale(request), "state_backups.operation_failed")
		a.renderStateBackupsPage(response, request, data, http.StatusBadGateway)
		return
	}
	data.Artifact = &artifact
	data.Manifest = &artifact.Manifest
	data.Notice = webText(resolveWebLocale(request), "state_backups.created")
	a.renderStateBackupsPage(response, request, data, http.StatusOK)
}

func (a *App) inspectStateBackup(response http.ResponseWriter, request *http.Request) {
	a.withStateBackupArchive(response, request, func(path string, passphrase []byte, data *stateBackupsPageData) (int, error) {
		manifest, err := a.stateBackups.Inspect(request.Context(), path, passphrase)
		if err == nil {
			data.Manifest = &manifest
			data.Notice = webText(resolveWebLocale(request), "state_backups.inspected")
		}
		return http.StatusOK, err
	})
}

func (a *App) stageStateBackup(response http.ResponseWriter, request *http.Request) {
	a.withStateBackupArchive(response, request, func(path string, passphrase []byte, data *stateBackupsPageData) (int, error) {
		confirmation := strings.TrimSpace(request.FormValue("backup_id"))
		if len(confirmation) != 24 {
			return http.StatusBadRequest, errStateBackupInput
		}
		stage, err := a.stateBackups.Stage(request.Context(), path, passphrase, confirmation)
		if err == nil {
			data.Stage = &stage
			data.Manifest = &stage.Manifest
			data.Notice = webText(resolveWebLocale(request), "state_backups.staged")
		}
		return http.StatusOK, err
	})
}

var errStateBackupInput = &stateBackupInputError{}

type stateBackupInputError struct{}

func (*stateBackupInputError) Error() string { return "state backup input is invalid" }

func (a *App) withStateBackupArchive(response http.ResponseWriter, request *http.Request, operation func(string, []byte, *stateBackupsPageData) (int, error)) {
	data := stateBackupsPageData{}
	if !validSessionCSRF(request) {
		data.Error = webText(resolveWebLocale(request), "state_backups.csrf_error")
		a.renderStateBackupsPage(response, request, data, http.StatusForbidden)
		return
	}
	archivePath := strings.TrimSpace(request.FormValue("archive_path"))
	passphrase, ok := stateBackupPassphrase(request.FormValue("passphrase"))
	if !ok || !validStateBackupPath(archivePath) {
		clearStateBackupSecret(passphrase)
		data.Error = webText(resolveWebLocale(request), "state_backups.input_error")
		a.renderStateBackupsPage(response, request, data, http.StatusBadRequest)
		return
	}
	defer clearStateBackupSecret(passphrase)
	if a.stateBackups == nil {
		data.Error = webText(resolveWebLocale(request), "state_backups.unavailable")
		a.renderStateBackupsPage(response, request, data, http.StatusServiceUnavailable)
		return
	}
	status, err := operation(archivePath, passphrase, &data)
	if err != nil {
		if err == errStateBackupInput {
			data.Error = webText(resolveWebLocale(request), "state_backups.input_error")
		} else {
			data.Error = webText(resolveWebLocale(request), "state_backups.operation_failed")
			status = http.StatusBadGateway
		}
	}
	a.renderStateBackupsPage(response, request, data, status)
}

func (a *App) discardStateBackupStage(response http.ResponseWriter, request *http.Request) {
	data := stateBackupsPageData{}
	stageID := strings.TrimSpace(request.PathValue("id"))
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" || len(stageID) != 24 {
		data.Error = webText(resolveWebLocale(request), "state_backups.input_error")
		a.renderStateBackupsPage(response, request, data, http.StatusForbidden)
		return
	}
	if a.stateBackups == nil {
		data.Error = webText(resolveWebLocale(request), "state_backups.unavailable")
		a.renderStateBackupsPage(response, request, data, http.StatusServiceUnavailable)
		return
	}
	if err := a.stateBackups.Discard(request.Context(), stageID); err != nil {
		data.Error = webText(resolveWebLocale(request), "state_backups.operation_failed")
		a.renderStateBackupsPage(response, request, data, http.StatusBadGateway)
		return
	}
	data.Notice = webText(resolveWebLocale(request), "state_backups.discarded")
	a.renderStateBackupsPage(response, request, data, http.StatusOK)
}

func (a *App) renderStateBackupsPage(response http.ResponseWriter, request *http.Request, data stateBackupsPageData, status int) {
	current := request.Context().Value(sessionContextKey).(session)
	data.Locale = resolveWebLocale(request)
	data.CSRFToken = current.csrfToken
	data.SettingsNavigation = newSettingsNavigation(current, data.Locale, "state-backups")
	data.Available = a.stateBackups != nil
	if a.stateBackups != nil {
		stages, err := a.stateBackups.List(request.Context())
		if err != nil && data.Error == "" {
			data.Error = webText(data.Locale, "state_backups.list_failed")
			status = http.StatusBadGateway
		} else if err == nil {
			data.Stages = stages
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	if err := stateBackupsTemplate.Execute(response, data); err != nil {
		return
	}
}

func stateBackupPassphrase(value string) ([]byte, bool) {
	passphrase := []byte(value)
	return passphrase, len(passphrase) >= 16 && len(passphrase) <= maximumStateBackupPassphraseBytes
}

func validStateBackupPath(value string) bool {
	return value != "" && len(value) <= 32<<10 && filepath.IsAbs(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func clearStateBackupSecret(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
