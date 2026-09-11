package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"scriptboard/internal/identity"
	"scriptboard/internal/workflow"
)

var workflowTemplate = mustWebTemplate("workflow")

func (a *App) workflowPage(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(sessionContextKey).(session)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = workflowTemplate.Execute(w, struct {
		CSRFToken             string
		CanManage, CanExecute bool
	}{current.csrfToken, identity.Allows(current.role, identity.PermissionManageExecution), identity.Allows(current.role, identity.PermissionExecute)})
}
func workflowReply(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		status := 500
		switch {
		case errors.Is(err, workflow.ErrConflict), errors.Is(err, workflow.ErrLocked):
			status = 409
		case errors.Is(err, workflow.ErrInvalid):
			status = 400
		case errors.Is(err, sql.ErrNoRows):
			status = 404
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
func workflowDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	if !validSessionCSRFValue(r, r.Header.Get("X-CSRF-Token")) {
		http.Error(w, "Invalid CSRF token", 403)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		http.Error(w, "Invalid workflow request", 400)
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Expected one document", 400)
		return false
	}
	return true
}
func (a *App) workflowState(w http.ResponseWriter, r *http.Request) {
	repository := workflow.Repository{DB: a.db}
	data := map[string]any{}
	current := r.Context().Value(sessionContextKey).(session)
	for _, kind := range []string{"draft", "published", "custom", "entry"} {
		if !identity.Allows(current.role, identity.PermissionManageExecution) && (kind == "draft" || kind == "custom") {
			data[kind] = []any{}
			continue
		}
		list, err := repository.List(r.Context(), kind)
		if err != nil {
			workflowReply(w, nil, err)
			return
		}
		if kind == "published" && !identity.Allows(current.role, identity.PermissionManageExecution) {
			for i, raw := range list {
				var d workflow.Definition
				if err = json.Unmarshal(raw, &d); err != nil {
					workflowReply(w, nil, err)
					return
				}
				d.Graph.Nodes = nil
				list[i], _ = json.Marshal(d)
			}
		}
		data[kind] = list
	}
	runs, err := repository.Runs(r.Context())
	if err != nil {
		workflowReply(w, nil, err)
		return
	}
	if !identity.Allows(current.role, identity.PermissionManageExecution) {
		for i := range runs {
			for j := range runs[i].Snapshot.Workflow.Graph.Nodes {
				runs[i].Snapshot.Workflow.Graph.Nodes[j].Script = nil
			}
		}
	}
	data["runs"] = runs
	workflowReply(w, data, nil)
}
func (a *App) workflowSaveCustom(w http.ResponseWriter, r *http.Request) {
	var d workflow.CustomDefinition
	if !workflowDecode(w, r, &d) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	saved, err := (workflow.Repository{DB: a.db}).SaveCustom(r.Context(), d, current.userID)
	workflowReply(w, saved, err)
}
func (a *App) workflowSave(w http.ResponseWriter, r *http.Request) {
	var d workflow.Definition
	if !workflowDecode(w, r, &d) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	saved, err := (workflow.Repository{DB: a.db}).Save(r.Context(), d, current.userID)
	workflowReply(w, saved, err)
}
func (a *App) workflowSaveDraft(w http.ResponseWriter, r *http.Request) {
	var d workflow.Definition
	if !workflowDecode(w, r, &d) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	saved, err := (workflow.Repository{DB: a.db}).SaveDraft(r.Context(), d, current.userID)
	workflowReply(w, saved, err)
}
func (a *App) workflowPublish(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if !workflowDecode(w, r, &p) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	saved, err := (workflow.Repository{DB: a.db}).Publish(r.Context(), p.ID, p.Revision, current.userID)
	workflowReply(w, saved, err)
}
func (a *App) workflowSaveEntry(w http.ResponseWriter, r *http.Request) {
	var e workflow.Entry
	if !workflowDecode(w, r, &e) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	saved, err := (workflow.Repository{DB: a.db}).SaveEntry(r.Context(), e, current.userID)
	workflowReply(w, saved, err)
}
func (a *App) workflowEntryLock(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
		Locked   bool   `json:"locked"`
	}
	if !workflowDecode(w, r, &p) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	saved, err := (workflow.Repository{DB: a.db}).SetEntryLock(r.Context(), p.ID, p.Revision, p.Locked, current.userID)
	workflowReply(w, saved, err)
}
func (a *App) workflowStart(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ID string `json:"id"`
	}
	if !workflowDecode(w, r, &p) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	saved, err := (workflow.Repository{DB: a.db}).Enqueue(r.Context(), p.ID, current.userID)
	workflowReply(w, saved, err)
}
func (a *App) workflowCancel(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ID string `json:"id"`
	}
	if !workflowDecode(w, r, &p) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	run, err := (workflow.Repository{DB: a.db}).Run(r.Context(), p.ID)
	if err != nil {
		workflowReply(w, nil, err)
		return
	}
	if current.role == identity.RoleOperator && run.Actor != current.userID {
		http.Error(w, "Cannot cancel another user’s workflow", http.StatusForbidden)
		return
	}
	err = (workflow.Repository{DB: a.db}).RequestCancel(r.Context(), p.ID)
	workflowReply(w, map[string]bool{"ok": err == nil}, err)
}
func (a *App) workflowResolve(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ID string `json:"id"`
	}
	if !workflowDecode(w, r, &p) {
		return
	}
	err := (workflow.Repository{DB: a.db}).ResolveAttention(r.Context(), p.ID)
	workflowReply(w, map[string]bool{"ok": err == nil}, err)
}

func (a *App) workflowLogs(w http.ResponseWriter, r *http.Request) {
	page, err := a.runs.EventPage(r.PathValue("id"), 0, 1000)
	if err != nil {
		workflowReply(w, nil, err)
		return
	}
	workflowReply(w, page.Events, nil)
}

func (a *App) workflowDelete(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Kind     string
		ID       string
		Revision int64
	}
	if !workflowDecode(w, r, &p) {
		return
	}
	err := (workflow.Repository{DB: a.db}).Delete(r.Context(), p.Kind, p.ID, p.Revision)
	workflowReply(w, map[string]bool{"ok": err == nil}, err)
}
func (a *App) workflowRun(w http.ResponseWriter, r *http.Request) {
	run, err := (workflow.Repository{DB: a.db}).Run(r.Context(), r.PathValue("id"))
	current := r.Context().Value(sessionContextKey).(session)
	if !identity.Allows(current.role, identity.PermissionManageExecution) {
		for i := range run.Snapshot.Workflow.Graph.Nodes {
			run.Snapshot.Workflow.Graph.Nodes[i].Script = nil
		}
	}
	workflowReply(w, run, err)
}

func (a *App) workflowExport(w http.ResponseWriter, r *http.Request) {
	var d workflow.Definition
	if !workflowDecode(w, r, &d) {
		return
	}
	b, err := (workflow.Repository{DB: a.db}).Export(r.Context(), d)
	workflowReply(w, b, err)
}
func (a *App) workflowImport(w http.ResponseWriter, r *http.Request) {
	var b workflow.Bundle
	if !workflowDecode(w, r, &b) {
		return
	}
	current := r.Context().Value(sessionContextKey).(session)
	d, err := (workflow.Repository{DB: a.db}).Import(r.Context(), b, current.userID)
	workflowReply(w, d, err)
}
func (a *App) workflowGuide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, mustWebAsset("ui/assets/workflow-ai-guide.md"))
}
