package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/runmanager"
)

const maxQuickExecutionSourceBytes = 1 << 20

type scriptLanguageOption struct {
	ID        string
	Label     string
	Extension string
}

func platformScriptLanguages() []scriptLanguageOption {
	if runtime.GOOS == "windows" {
		return []scriptLanguageOption{
			{ID: "powershell", Label: "PowerShell", Extension: ".ps1"},
			{ID: "batch", Label: "Batch", Extension: ".cmd"},
			{ID: "python", Label: "Python", Extension: ".py"},
		}
	}
	return []scriptLanguageOption{
		{ID: "shell", Label: "Shell", Extension: ".sh"},
		{ID: "python", Label: "Python", Extension: ".py"},
		{ID: "powershell", Label: "PowerShell", Extension: ".ps1"},
	}
}

func platformScriptLanguage(id string) (scriptLanguageOption, error) {
	for _, language := range platformScriptLanguages() {
		if language.ID == id {
			return language, nil
		}
	}
	return scriptLanguageOption{}, errors.New("script language is not supported on this host")
}

func validateQuickExecutionSource(source string) error {
	if source == "" {
		return errors.New("source is required")
	}
	if len([]byte(source)) > maxQuickExecutionSourceBytes {
		return fmt.Errorf("source exceeds the %d-byte limit", maxQuickExecutionSourceBytes)
	}
	if !utf8.ValidString(source) || strings.ContainsRune(source, 0) {
		return errors.New("source must be valid UTF-8 without NUL bytes")
	}
	return nil
}

func quickExecutionFileName(name, extension string) string {
	if strings.HasSuffix(strings.ToLower(name), strings.ToLower(extension)) {
		return name
	}
	return name + extension
}

func quickExecutionFileStem(name, extension string) string {
	if strings.HasSuffix(strings.ToLower(name), strings.ToLower(extension)) {
		return name[:len(name)-len(extension)]
	}
	return name
}

func parseQuickExecutionTimeout(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 24*60*60 {
		return 0, errors.New("timeout must be from 0 to 86400 seconds")
	}
	return seconds, nil
}

func (a *App) hostDirectories(response http.ResponseWriter, request *http.Request) {
	relative := request.URL.Query().Get("path")
	entries, err := a.hostList(request.Context(), relative)
	if err != nil {
		http.Error(response, "working directory is invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	type directoryView struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	directories := make([]directoryView, 0)
	for _, entry := range entries {
		if entry.Kind == hostfiles.Directory {
			directories = append(directories, directoryView{Name: entry.Name, Path: entry.Path})
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(struct {
		Path        string          `json:"path"`
		Directories []directoryView `json:"directories"`
	}{Path: relative, Directories: directories})
}

func (a *App) defaultHostDirectory(ctx context.Context) string {
	roots, err := a.hostRoots(ctx)
	if err != nil || len(roots) == 0 {
		return ""
	}
	candidates := make([]string, 0, 3)
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		candidates = append(candidates, home)
	}
	if working, workingErr := os.Getwd(); workingErr == nil {
		candidates = append(candidates, working)
	}
	candidates = append(candidates, os.TempDir())
	for _, candidate := range candidates {
		insideRoot := false
		for _, root := range roots {
			if hostfiles.Contains(root.Path, candidate) {
				insideRoot = true
				break
			}
		}
		if !insideRoot {
			continue
		}
		if prepared, prepareErr := a.hostPrepareDirectory(ctx, candidate); prepareErr == nil {
			return prepared.Path
		}
	}
	for _, root := range roots {
		if prepared, prepareErr := a.hostPrepareDirectory(ctx, root.Path); prepareErr == nil {
			return prepared.Path
		}
		entries, listErr := a.hostList(ctx, root.Path)
		if listErr != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Kind != hostfiles.Directory {
				continue
			}
			if prepared, prepareErr := a.hostPrepareDirectory(ctx, entry.Path); prepareErr == nil {
				return prepared.Path
			}
		}
	}
	return ""
}

func (a *App) oneTimeRunTask(response http.ResponseWriter, request *http.Request) {
	a.renderTaskPage(response, request, taskPageData{
		Kind:             "one-time-run",
		Title:            webText(resolveWebLocale(request), "task.one_time.title"),
		Description:      webText(resolveWebLocale(request), "task.one_time.description"),
		BackURL:          "/config/quick-runs",
		Action:           "/config/quick-runs/one-time",
		Languages:        platformScriptLanguages(),
		WorkingDirectory: a.defaultHostDirectory(request.Context()),
	})
}

func (a *App) quickCreateTask(response http.ResponseWriter, request *http.Request) {
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "Unable to read Quick Run groups", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:             "quick-create",
		Title:            webText(resolveWebLocale(request), "task.quick_create.title"),
		Description:      webText(resolveWebLocale(request), "task.quick_create.description"),
		BackURL:          "/config/quick-runs",
		Action:           "/config/quick-runs/from-source",
		Languages:        platformScriptLanguages(),
		Groups:           groups,
		WorkingDirectory: a.defaultHostDirectory(request.Context()),
	})
}

func (a *App) createQuickRunFromSource(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF token is invalid", http.StatusForbidden)
		return
	}
	language, err := platformScriptLanguage(request.FormValue("language"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.runs.ValidateExecutor(language.Extension); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	source := request.FormValue("source")
	if err := validateQuickExecutionSource(source); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	workingDirectory := request.FormValue("working_directory")
	preparedDirectory, err := a.hostPrepareDirectory(request.Context(), workingDirectory)
	if err != nil {
		http.Error(response, "working directory is invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	workingDirectory = preparedDirectory.Path
	fileName := strings.TrimSpace(request.FormValue("file_name"))
	if err := hostfiles.ValidateName(fileName); err != nil {
		http.Error(response, "file name is invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	fileName = quickExecutionFileName(fileName, language.Extension)
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" {
		name = strings.TrimSpace(request.FormValue("file_name"))
	}
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "Quick Run name is invalid", http.StatusBadRequest)
		return
	}
	timeoutSeconds, err := parseQuickExecutionTimeout(request.FormValue("timeout_seconds"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "Unable to read variables", http.StatusInternalServerError)
		return
	}
	argumentsTemplate := request.FormValue("arguments")
	if err := runmanager.ValidateArgumentsTemplate(argumentsTemplate, variables); err != nil {
		http.Error(response, "Arguments are invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "Quick Run group does not exist", http.StatusConflict)
		return
	}

	action := request.FormValue("conflict_action")
	if !validConflictAction(action) || action == conflictActionSkip {
		http.Error(response, "conflict action is invalid", http.StatusBadRequest)
		return
	}
	targetPath, err := a.hostDestination(request.Context(), workingDirectory, fileName)
	if err != nil {
		http.Error(response, "script destination is invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	targetInfo, _, targetErr := a.hostInfo(request.Context(), targetPath)
	targetExists := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		http.Error(response, "Unable to inspect target: "+targetErr.Error(), http.StatusBadRequest)
		return
	}
	if targetExists && action == "" {
		suggested, suggestErr := a.hostAvailableName(request.Context(), workingDirectory, fileName)
		if suggestErr != nil {
			http.Error(response, "Unable to suggest a file name", http.StatusInternalServerError)
			return
		}
		a.renderQuickCreateConflict(response, request, quickCreateValues{
			WorkingDirectory: workingDirectory, Language: language.ID, FileName: quickExecutionFileStem(fileName, language.Extension),
			Source: source, Name: name, Arguments: argumentsTemplate, TimeoutSeconds: timeoutSeconds, GroupID: request.FormValue("group_id"),
		}, targetPath, quickExecutionFileStem(suggested, language.Extension), targetInfo.Mode().IsRegular() && !a.runs.ConflictsPath(targetPath))
		return
	}
	if targetExists && action == conflictActionOverwrite && (!targetInfo.Mode().IsRegular() || a.runs.ConflictsPath(targetPath)) {
		http.Error(response, "the existing target cannot be overwritten while it is active or non-regular", http.StatusConflict)
		return
	}
	if targetExists && action == conflictActionRename {
		renamedStem := strings.TrimSpace(request.FormValue("rename_file_name"))
		if err := hostfiles.ValidateName(renamedStem); err != nil {
			http.Error(response, "renamed file name is invalid: "+err.Error(), http.StatusBadRequest)
			return
		}
		fileName = renamedStem + language.Extension
		targetPath, err = a.hostDestination(request.Context(), workingDirectory, fileName)
		if err != nil {
			http.Error(response, "renamed script destination is invalid: "+err.Error(), http.StatusBadRequest)
			return
		}
		if _, _, err := a.hostInfo(request.Context(), targetPath); err == nil {
			http.Error(response, "renamed target already exists", http.StatusConflict)
			return
		} else if !os.IsNotExist(err) {
			http.Error(response, "Unable to inspect renamed target: "+err.Error(), http.StatusBadRequest)
			return
		}
		targetExists = false
	}
	release, err := a.acquireFileMutationLease(targetPath)
	if err != nil {
		http.Error(response, "script path is in use: "+err.Error(), http.StatusConflict)
		return
	}
	defer release()

	var trashID string
	if targetExists {
		trashID, err = randomToken(18)
		if err != nil {
			http.Error(response, "Unable to prepare overwrite", http.StatusInternalServerError)
			return
		}
	}
	trashed, err := a.hostUpload(request.Context(), workingDirectory, fileName, bytes.NewBufferString(source), maxQuickExecutionSourceBytes, targetExists, trashID)
	if err != nil {
		http.Error(response, "Unable to create script: "+err.Error(), http.StatusConflict)
		return
	}
	targetPath, err = a.hostCanonicalExisting(request.Context(), targetPath)
	if err != nil {
		_ = a.hostRemoveRegular(request.Context(), targetPath)
		if trashed != nil {
			_ = a.hostRestoreFromTrash(request.Context(), trashed.StoredPath, trashed.OriginalPath)
		}
		http.Error(response, "Unable to resolve created script: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rollbackFile := func() {
		_ = a.hostRemoveRegular(request.Context(), targetPath)
		if trashed != nil {
			_ = a.hostRestoreFromTrash(request.Context(), trashed.StoredPath, trashed.OriginalPath)
		}
	}
	prepared, err := a.hostPrepareScript(request.Context(), targetPath)
	if err != nil {
		rollbackFile()
		http.Error(response, "Unable to publish created script: "+err.Error(), http.StatusInternalServerError)
		return
	}

	transaction, err := a.db.Begin()
	if err != nil {
		rollbackFile()
		http.Error(response, "Unable to create Quick Run", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	if trashed != nil {
		if _, err = transaction.Exec(
			`INSERT INTO trash_entries
				(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			trashID, trashed.OriginalPath, hostfiles.ComparisonKey(trashed.OriginalPath), trashed.StoredPath,
			hostfiles.ComparisonKey(trashed.StoredPath), time.Now().UTC().Unix(), trashed.Size, trashed.Directory,
		); err != nil {
			rollbackFile()
			http.Error(response, "Unable to record overwritten script", http.StatusInternalServerError)
			return
		}
	}
	id, err := randomToken(18)
	var sortOrder int
	if err == nil {
		err = transaction.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM quick_runs WHERE group_id IS ?", groupID).Scan(&sortOrder)
	}
	now := time.Now().UTC().Unix()
	if err == nil {
		_, err = transaction.Exec(`INSERT INTO quick_runs
			(id, name, script_path, script_path_key, arguments_template, timeout_seconds, source_run_id, sort_order, created_at, group_id, script_sha256, revision, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, 1, ?)`,
			id, name, prepared.Path, hostfiles.ComparisonKey(prepared.Path), argumentsTemplate, timeoutSeconds, sortOrder, now, groupID, prepared.Digest, now)
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		rollbackFile()
		http.Error(response, "Unable to create Quick Run", http.StatusInternalServerError)
		return
	}
	a.recordQuickRunAuditForRequest(request, "create_quick_run_from_source", id, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

type quickCreateValues struct {
	WorkingDirectory string
	Language         string
	FileName         string
	Source           string
	Name             string
	Arguments        string
	TimeoutSeconds   int
	GroupID          string
}

func (a *App) renderQuickCreateConflict(response http.ResponseWriter, request *http.Request, values quickCreateValues, targetPath, suggestedName string, canOverwrite bool) {
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "Unable to read Quick Run groups", http.StatusInternalServerError)
		return
	}
	var quickReferences, scheduleReferences int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM quick_runs WHERE script_path = ?", targetPath).Scan(&quickReferences); err != nil {
		http.Error(response, "Unable to inspect Quick Run references", http.StatusInternalServerError)
		return
	}
	if err := a.db.QueryRow("SELECT COUNT(*) FROM schedules WHERE deleted = 0 AND script_path = ?", targetPath).Scan(&scheduleReferences); err != nil {
		http.Error(response, "Unable to inspect schedule references", http.StatusInternalServerError)
		return
	}
	a.renderTaskPageStatus(response, request, http.StatusConflict, taskPageData{
		Kind: "quick-create", Title: webText(resolveWebLocale(request), "task.quick_create.title"),
		Description: webText(resolveWebLocale(request), "task.quick_create.description"),
		BackURL:     "/config/quick-runs", Action: "/config/quick-runs/from-source", Languages: platformScriptLanguages(),
		WorkingDirectory: values.WorkingDirectory, FileName: values.FileName, Source: values.Source, Name: values.Name,
		Arguments: values.Arguments, TimeoutSeconds: values.TimeoutSeconds, GroupID: values.GroupID, Groups: groups, Language: values.Language,
		Conflict: true, ConflictPath: targetPath, SuggestedName: suggestedName, CanOverwrite: canOverwrite,
		QuickReferences: quickReferences, ScheduleReferences: scheduleReferences,
	})
}

func (a *App) startOneTimeRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF token is invalid", http.StatusForbidden)
		return
	}
	language, err := platformScriptLanguage(request.FormValue("language"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	source := request.FormValue("source")
	if err := validateQuickExecutionSource(source); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	workingDirectory := request.FormValue("working_directory")
	preparedDirectory, err := a.hostPrepareDirectory(request.Context(), workingDirectory)
	if err != nil {
		http.Error(response, "working directory is invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	workingDirectory = preparedDirectory.Path
	timeoutSeconds, err := parseQuickExecutionTimeout(request.FormValue("timeout_seconds"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "Unable to read variables", http.StatusInternalServerError)
		return
	}
	argumentsTemplate := request.FormValue("arguments")
	if err := runmanager.ValidateArgumentsTemplate(argumentsTemplate, variables); err != nil {
		http.Error(response, "Arguments are invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	id, err := a.runs.StartOneTime(runmanager.OneTimeStartRequest{
		WorkingDirectory:  workingDirectory,
		Extension:         language.Extension,
		Source:            source,
		ArgumentsTemplate: argumentsTemplate,
		TimeoutSeconds:    timeoutSeconds,
		Variables:         variables,
		AuditSource:       request.RemoteAddr,
		InitiatorUserID:   current.userID,
		InitiatorUsername: current.username,
		InitiatorRole:     string(current.role),
	})
	if err != nil {
		http.Error(response, "Unable to start one-time Run: "+err.Error(), http.StatusBadRequest)
		return
	}
	response.Header().Set(assistantResourceIDHeader, id)
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) runSource(response http.ResponseWriter, request *http.Request) {
	source, err := a.runs.ReadSource(request.PathValue("id"))
	switch {
	case errors.Is(err, runmanager.ErrSourceExpired):
		http.Error(response, "one-time source has expired", http.StatusGone)
		return
	case err != nil:
		http.Error(response, "one-time source is unavailable", http.StatusNotFound)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write(source)
}
