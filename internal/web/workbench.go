package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"scriptboard/internal/workbench"
)

var workbenchTemplate = mustWebTemplate("workbench")

func wbText(locale webLocale, zh, en string) string {
	if locale == localeEnglishUS {
		return en
	}
	return zh
}
func (a *App) workbenchPage(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(sessionContextKey).(session)
	state, err := workbench.Load(r.Context(), a.db)
	if err != nil {
		http.Error(w, "Unable to load workspace", 500)
		return
	}
	data, _ := json.Marshal(state)
	locale := resolveWebLocale(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = workbenchTemplate.Execute(w, struct {
		Locale                                                                                                                            webLocale
		CSRFToken, JSON, Title, NewBoard, Save, Delete, NewItem, Name, Content, Note, Links, Todo, Timer, Draw, Add, URL, ItemTitle, NoJS string
		State                                                                                                                             workbench.State
	}{Locale: locale, CSRFToken: current.csrfToken, JSON: string(data), State: state,
		Title: wbText(locale, "灵感空间", "Inspiration space"), NewBoard: wbText(locale, "新建面板", "New board"), Save: wbText(locale, "保存", "Save"), Delete: wbText(locale, "删除", "Delete"), NewItem: wbText(locale, "添加模块", "Add block"), Name: wbText(locale, "面板名称", "Board name"), Content: wbText(locale, "内容", "Content"), Note: wbText(locale, "笔记", "Note"), Links: wbText(locale, "链接", "Links"), Todo: wbText(locale, "待办", "Tasks"), Timer: wbText(locale, "计时", "Timer"), Draw: wbText(locale, "画板", "Canvas"), Add: wbText(locale, "添加", "Add"), URL: wbText(locale, "链接地址", "Link URL"), ItemTitle: wbText(locale, "模块标题", "Block title"), NoJS: wbText(locale, "基础编辑模式。启用 JavaScript 可使用自由绘画、画布平移和实时计时。", "Basic editor. Enable JavaScript for drawing, canvas panning, and live timers.")})
}
func (a *App) workbenchState(w http.ResponseWriter, r *http.Request) {
	state, err := workbench.Load(r.Context(), a.db)
	if err != nil {
		http.Error(w, "Unable to load workspace", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(state)
}
func (a *App) saveWorkbench(w http.ResponseWriter, r *http.Request) {
	if !validSessionCSRFValue(r, r.Header.Get("X-CSRF-Token")) {
		http.Error(w, "Invalid CSRF token", 403)
		return
	}
	capacity, err := workbench.ReadCapacity(r.Context(), a.db)
	if err != nil {
		http.Error(w, "Unable to load space capacity", 500)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(capacity.Bytes)+65536)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var state workbench.State
	if err := decoder.Decode(&state); err != nil {
		http.Error(w, "Invalid workspace document or storage capacity exceeded", 400)
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Expected one document", 400)
		return
	}
	a.persistWorkbench(w, r, state, true)
}
func (a *App) persistWorkbench(w http.ResponseWriter, r *http.Request, s workbench.State, api bool) {
	revision, err := workbench.Save(r.Context(), a.db, s)
	if errors.Is(err, workbench.ErrInvalid) {
		http.Error(w, err.Error(), 400)
		return
	}
	if errors.Is(err, workbench.ErrConflict) {
		http.Error(w, "Workspace changed in another session. Reload before editing.", 409)
		return
	}
	if err != nil {
		http.Error(w, "Unable to save workspace", 500)
		return
	}
	a.workbenchUpdates.Publish()
	w.Header().Set("Cache-Control", "no-store")
	if api {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"revision": revision})
	} else {
		http.Redirect(w, r, "/resources/workbench", http.StatusSeeOther)
	}
}

// Native forms preserve access to stored notes, links and tasks without the enhanced editor.
func (a *App) workbenchAction(w http.ResponseWriter, r *http.Request) {
	if !validSessionCSRF(r) {
		http.Error(w, "Invalid CSRF token", 403)
		return
	}
	s, err := workbench.Load(r.Context(), a.db)
	if err != nil {
		http.Error(w, "Unable to load workspace", 500)
		return
	}
	revision, e := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if e != nil || revision != s.Revision {
		http.Error(w, "Workspace changed. Reload before editing.", 409)
		return
	}
	action := r.FormValue("action")
	if action == "new-board" {
		id, e := randomToken(16)
		if e != nil {
			http.Error(w, "Unable to create board", 500)
			return
		}
		s.Boards = append(s.Boards, workbench.Board{ID: id, Name: r.FormValue("name"), Items: []workbench.Item{}})
		a.persistWorkbench(w, r, s, false)
		return
	}
	for bi := range s.Boards {
		b := &s.Boards[bi]
		if b.ID != r.FormValue("board") {
			continue
		}
		switch action {
		case "rename-board":
			b.Name = r.FormValue("name")
		case "delete-board":
			s.Boards = append(s.Boards[:bi], s.Boards[bi+1:]...)
		case "new-item":
			id, e := randomToken(16)
			if e != nil {
				http.Error(w, "Unable to create block", 500)
				return
			}
			kind := r.FormValue("type")
			b.Items = append(b.Items, workbench.Item{ID: id, Type: kind, Title: r.FormValue("title"), Mode: "25", Remaining: 1500, Size: "medium"})
		default:
			found := false
			for i := range b.Items {
				item := &b.Items[i]
				if item.ID != r.FormValue("item") {
					continue
				}
				found = true
				switch action {
				case "delete-item":
					b.Items = append(b.Items[:i], b.Items[i+1:]...)
				case "edit-item":
					item.Title = r.FormValue("title")
					item.Text = r.FormValue("text")
				case "add-task":
					item.Tasks = append(item.Tasks, workbench.Task{Text: r.FormValue("text")})
				case "add-link":
					item.Links = append(item.Links, workbench.Link{Title: r.FormValue("title"), URL: r.FormValue("url")})
				case "edit-task", "delete-task", "edit-link", "delete-link":
					index, e := strconv.Atoi(r.FormValue("index"))
					if e != nil || index < 0 {
						http.Error(w, "Invalid item", 400)
						return
					}
					switch action {
					case "edit-task":
						if index >= len(item.Tasks) {
							http.NotFound(w, r)
							return
						}
						item.Tasks[index] = workbench.Task{Text: r.FormValue("text"), Done: r.FormValue("done") == "on"}
					case "delete-task":
						if index >= len(item.Tasks) {
							http.NotFound(w, r)
							return
						}
						item.Tasks = append(item.Tasks[:index], item.Tasks[index+1:]...)
					case "edit-link":
						if index >= len(item.Links) {
							http.NotFound(w, r)
							return
						}
						item.Links[index] = workbench.Link{Title: r.FormValue("title"), URL: r.FormValue("url")}
					case "delete-link":
						if index >= len(item.Links) {
							http.NotFound(w, r)
							return
						}
						item.Links = append(item.Links[:index], item.Links[index+1:]...)
					}
				case "timer":
					if item.Type != "timer" {
						http.Error(w, "Invalid timer", 400)
						return
					}
					now := time.Now().UnixMilli()
					if item.Running {
						elapsed := (now - item.Started) / 1000
						if item.Mode == "0" {
							item.Remaining += elapsed
						} else {
							item.Remaining = max(0, item.Remaining-elapsed)
						}
					}
					item.Running = !item.Running
					item.Started = now
				default:
					http.Error(w, "Invalid action", 400)
					return
				}
				break
			}
			if !found {
				http.NotFound(w, r)
				return
			}
		}
		a.persistWorkbench(w, r, s, false)
		return
	}
	http.NotFound(w, r)
}

func (a *App) patchWorkbench(w http.ResponseWriter, r *http.Request) {
	if !validSessionCSRFValue(r, r.Header.Get("X-CSRF-Token")) {
		http.Error(w, "Invalid CSRF token", 403)
		return
	}
	capacity, err := workbench.ReadCapacity(r.Context(), a.db)
	if err != nil {
		http.Error(w, "Unable to load space", 500)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(capacity.Bytes)*2+131072)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var patch workbench.Patch
	if decoder.Decode(&patch) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid patch", 400)
		return
	}
	result, err := workbench.ApplyPatch(r.Context(), a.db, patch)
	if errors.Is(err, workbench.ErrInvalid) {
		http.Error(w, err.Error(), 400)
		return
	}
	if errors.Is(err, workbench.ErrConflict) {
		http.Error(w, "Space is busy. Retry saving.", 409)
		return
	}
	if err != nil {
		http.Error(w, "Unable to save space", 500)
		return
	}
	a.workbenchUpdates.Publish()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
func (a *App) workbenchEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unavailable", 500)
		return
	}
	updates, unsubscribe := a.workbenchUpdates.Subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func() bool {
		_, err := fmt.Fprint(w, "event: update\ndata: {}\n\n")
		flusher.Flush()
		return err == nil
	}
	if !send() {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	// Periodic reconnect rechecks the login session and always fetches current data.
	expiry := time.NewTimer(45 * time.Second)
	defer expiry.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-expiry.C:
			return
		case <-updates:
			if !send() {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
