package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/resourcelimits"
	"scriptboard/internal/store/memorysettings"
	"strings"
)

type memorySettingField struct{ Name, Label, Value, Active, Timing string }
type memorySettingsData struct {
	Windows                    bool
	Locale                     webLocale
	CSRFToken, Revision, Error string
	Saved, Pending, Available  bool
	Fields                     []memorySettingField
	SettingsNavigation         settingsNavigationData
}

func (a *App) memorySettingsPage(w http.ResponseWriter, r *http.Request) {
	snapshot, err := memorysettings.Read(a.stateRoot)
	if err != nil {
		a.renderMemorySettings(w, r, http.StatusOK, snapshot, resourcelimits.Memory{}, "memory_settings.unavailable")
		return
	}
	a.renderMemorySettings(w, r, http.StatusOK, snapshot, snapshot.Memory, "")
}
func (a *App) updateMemorySettings(w http.ResponseWriter, r *http.Request) {
	if !validSessionCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// Serialize persistence and publication so concurrent saves cannot publish an older default.
	a.memorySettingsMu.Lock()
	defer a.memorySettingsMu.Unlock()
	snapshot, err := memorysettings.Read(a.stateRoot)
	if err != nil {
		a.renderMemorySettings(w, r, http.StatusServiceUnavailable, snapshot, resourcelimits.Memory{}, "memory_settings.unavailable")
		return
	}
	memory := snapshot.Memory
	memory.Total = strings.TrimSpace(r.FormValue("runner_memory_limit"))
	memory.PerRun = strings.TrimSpace(r.FormValue("run_memory_limit"))
	if runtime.GOOS == "windows" {
		memory.Process = strings.TrimSpace(r.FormValue("runner_process_memory_limit"))
	} else {
		memory.Swap = strings.TrimSpace(r.FormValue("runner_swap_limit"))
	}
	if memory.Total == "" || memory.PerRun == "" || memory.Process == "" || memory.Swap == "" || memory.Validate() != nil {
		a.renderMemorySettings(w, r, http.StatusUnprocessableEntity, snapshot, memory, "memory_settings.invalid")
		return
	}
	revision := r.FormValue("revision")
	if revision != snapshot.Revision {
		a.renderMemorySettings(w, r, http.StatusConflict, snapshot, snapshot.Memory, "memory_settings.conflict")
		return
	}
	if a.memoryBroker != nil {
		parameters, _ := json.Marshal(memory)
		err = a.memoryBroker.Invoke(r.Context(), privilegebroker.ActionMemorySettings, "runner-memory", revision, parameters)
	} else {
		err = memorysettings.Save(a.stateRoot, revision, memory)
	}
	if err != nil {
		key := "memory_settings.save_failed"
		status := http.StatusInternalServerError
		// Broker errors cross process boundaries; compare revisions to keep concurrent saves actionable.
		latest, readErr := memorysettings.Read(a.stateRoot)
		if errors.Is(err, memorysettings.ErrConflict) || (readErr == nil && latest.Revision != revision) {
			key = "memory_settings.conflict"
			status = http.StatusConflict
		}
		a.renderMemorySettings(w, r, status, snapshot, memory, key)
		return
	}
	// Publish only after persistence succeeds; active runs already hold their resolved quota.
	a.runs.SetDefaultMemory(memory.PerRun)
	values, _ := json.Marshal(memory)
	a.recordAuditForRequest(r, "update_memory_settings", string(values), "succeeded")
	http.Redirect(w, r, "/settings/memory?saved=1", http.StatusSeeOther)
}
func (a *App) renderMemorySettings(w http.ResponseWriter, r *http.Request, status int, snapshot memorysettings.Snapshot, memory resourcelimits.Memory, errorKey string) {
	current := r.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(r)
	active := a.activeMemory.Resolved()
	active.PerRun = a.runs.DefaultMemory()
	fields := []memorySettingField{{"runner_memory_limit", "memory_settings.total", memory.Total, active.Total, "memory_settings.timing_restart"}, {"run_memory_limit", "memory_settings.per_run", memory.PerRun, active.PerRun, "memory_settings.timing_live"}}
	if runtime.GOOS == "windows" {
		fields = append(fields, memorySettingField{"runner_process_memory_limit", "memory_settings.process", memory.Process, active.Process, "memory_settings.timing_restart"})
	} else {
		fields = append(fields, memorySettingField{"runner_swap_limit", "memory_settings.swap", memory.Swap, active.Swap, "memory_settings.timing_restart"})
	}
	data := memorySettingsData{Windows: runtime.GOOS == "windows", Locale: locale, CSRFToken: current.csrfToken, Revision: snapshot.Revision, Fields: fields, Available: snapshot.Revision != "", Saved: r.URL.Query().Get("saved") == "1", Pending: snapshot.Revision != "" && (snapshot.Memory.Total != active.Total || snapshot.Memory.Process != active.Process || snapshot.Memory.Swap != active.Swap), SettingsNavigation: newSettingsNavigation(current, locale, "memory")}
	if errorKey != "" {
		data.Error = webText(locale, errorKey)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = memorySettingsTemplate.Execute(w, data)
}
