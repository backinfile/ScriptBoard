package web

import (
	"database/sql"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/identity"
)

// Documents are path references into the host filesystem; membership, grouping
// and ordering mirror the file Quick access pins so both collections behave
// identically under the shared record-group system.
type documentView struct {
	Path, PathKey, Name, GroupID              string
	IconClass, ViewURL, EditURL, DirectoryURL string
	MoveGroupURL                              string
	Size                                      int64
	CreatedAt, ModifiedAt                     time.Time
	Accessible, Editable                      bool
}

type documentGroup struct {
	ID, Name  string
	Items     []documentView
	Ungrouped bool
}

func documentReturnTo(request *http.Request) string {
	value := safeLocalReturnPath(request.FormValue("return_to"))
	if strings.HasPrefix(value, "/resources/documents") || strings.HasPrefix(value, "/resources/files") {
		return value
	}
	return "/resources/documents"
}

func (a *App) documentsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	canWrite := identity.Allows(current.role, identity.PermissionWriteFiles)
	canManageGroups := identity.Allows(current.role, identity.PermissionManageOperations)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = documentsTemplate.Execute(response, struct {
			Groups                     []documentGroup
			Query, CSRFToken, ClearURL string
			Locale                     webLocale
			Total                      int
			Reorder, DeferredData      bool
			CanWrite, CanManageGroups  bool
		}{CSRFToken: current.csrfToken, Locale: locale, DeferredData: true, CanWrite: canWrite, CanManageGroups: canManageGroups})
		return
	}
	shared, err := a.loadRecordGroups()
	if err != nil {
		http.Error(response, "无法读取共享分组", http.StatusInternalServerError)
		return
	}
	rows, err := a.db.Query(`SELECT d.path, d.path_key, COALESCE(d.group_id,'')
		FROM documents d LEFT JOIN quick_run_groups g ON g.id=d.group_id
		ORDER BY CASE WHEN d.group_id IS NULL THEN 1 ELSE 0 END, g.sort_order, d.sort_order, d.created_at`)
	if err != nil {
		http.Error(response, "无法读取文档", http.StatusInternalServerError)
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	foldedQuery := strings.ToLower(query)
	groups := make([]documentGroup, len(shared))
	indexes := make(map[string]int, len(shared))
	for index, group := range shared {
		groups[index] = documentGroup{ID: group.ID, Name: group.Name}
		indexes[group.ID] = index
	}
	var ungrouped []documentView
	for rows.Next() {
		var document documentView
		if err := rows.Scan(&document.Path, &document.PathKey, &document.GroupID); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取文档", http.StatusInternalServerError)
			return
		}
		document.Name = filepath.Base(document.Path)
		if query != "" && !strings.Contains(strings.ToLower(document.Name), foldedQuery) && !strings.Contains(strings.ToLower(document.Path), foldedQuery) {
			continue
		}
		// Saved paths may go missing after collection; keep the row readable and
		// only drop the actions that require a live host file.
		if info, _, statErr := a.hostInfo(request.Context(), document.Path); statErr == nil {
			document.Accessible = true
			document.Size = info.Size()
			document.CreatedAt = hostfiles.CreatedAt(info)
			document.ModifiedAt = info.ModTime()
			entry := hostfiles.Entry{Name: document.Name, Path: document.Path, Kind: hostfiles.Regular, Size: info.Size(), ModifiedAt: info.ModTime()}
			category := classifyFile(entry, document.Path)
			displayCategory, previewableText := a.classifyFileContent(listedFile{Entry: entry, Path: document.Path, Category: category})
			document.IconClass = fileCategoryIcon(displayCategory)
			document.ViewURL = documentFileURL("/resources/files/view", document.Path, request.URL.RequestURI())
			document.Editable = canWrite && previewableText && (displayCategory == fileCategoryText || displayCategory == fileCategoryScript)
			if document.Editable {
				document.EditURL = documentFileURL("/resources/files/edit", document.Path, request.URL.RequestURI())
			}
			document.DirectoryURL = fileQuickAccessHref(document.Path, "file")
		} else {
			document.IconClass = fileCategoryIcon(fileCategoryOther)
		}
		document.MoveGroupURL = "/resources/documents/move-group?path=" + url.QueryEscape(document.Path)
		if index, ok := indexes[document.GroupID]; ok {
			groups[index].Items = append(groups[index].Items, document)
		} else {
			ungrouped = append(ungrouped, document)
		}
	}
	_ = rows.Close()
	if len(ungrouped) > 0 {
		groups = append(groups, documentGroup{ID: "ungrouped", Name: webText(locale, "groups.ungrouped"), Items: ungrouped, Ungrouped: true})
	}
	total := 0
	for _, group := range groups {
		total += len(group.Items)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = documentsTemplate.Execute(response, struct {
		Groups                     []documentGroup
		Query, CSRFToken, ClearURL string
		Locale                     webLocale
		Total                      int
		Reorder, DeferredData      bool
		CanWrite, CanManageGroups  bool
	}{
		Groups: groups, Query: query, CSRFToken: current.csrfToken, ClearURL: "/resources/documents",
		Locale: locale, Total: total, Reorder: request.URL.Query().Get("reorder") == "1",
		CanWrite: canWrite, CanManageGroups: canManageGroups,
	})
}

func (a *App) updateDocument(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	action := strings.TrimSpace(request.FormValue("action"))
	path := strings.TrimSpace(request.FormValue("path"))
	if path == "" {
		http.Error(response, "Path is required", http.StatusBadRequest)
		return
	}
	pathKey := hostfiles.ComparisonKey(path)
	returnTo := documentReturnTo(request)

	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "Unable to save document", http.StatusInternalServerError)
		return
	}
	defer func() { _ = transaction.Rollback() }()
	switch action {
	case "add":
		canonical, canonicalErr := a.hostCanonicalExisting(request.Context(), path)
		if canonicalErr != nil {
			writeHostFileError(response, "Unable to add document", canonicalErr)
			return
		}
		info, _, infoErr := a.hostInfo(request.Context(), canonical)
		if infoErr != nil {
			writeHostFileError(response, "Unable to add document", infoErr)
			return
		}
		if info.IsDir() {
			http.Error(response, "Only files can be added to Documents", http.StatusBadRequest)
			return
		}
		path, pathKey = canonical, hostfiles.ComparisonKey(canonical)
		_, err = transaction.Exec(`INSERT INTO documents (path, path_key, group_id, sort_order, created_at)
			VALUES (?, ?, NULL, COALESCE((SELECT MAX(sort_order) + 1 FROM documents WHERE group_id IS NULL), 1), ?)
			ON CONFLICT(path_key) DO UPDATE SET path = excluded.path`, path, pathKey, time.Now().UTC().Unix())
	case "remove":
		_, err = transaction.Exec("DELETE FROM documents WHERE path_key = ?", pathKey)
	case "move-group":
		// Resolve through the transaction so the single-connection SQLite pool
		// keeps validation and membership update atomic without self-waiting.
		groupID, groupErr := resolveRecordGroupIDWith(transaction, request.FormValue("group_id"))
		if groupErr != nil {
			http.Error(response, "Document group not found", http.StatusBadRequest)
			return
		}
		var currentGroup sql.NullString
		if queryErr := transaction.QueryRow("SELECT group_id FROM documents WHERE path_key = ?", pathKey).Scan(&currentGroup); queryErr != nil {
			http.Error(response, "Document not found", http.StatusNotFound)
			return
		}
		var order int
		if currentGroup.String != valueOrEmpty(groupID) {
			if queryErr := transaction.QueryRow("SELECT COALESCE(MAX(sort_order),0)+1 FROM documents WHERE group_id IS ?", groupID).Scan(&order); queryErr != nil {
				err = queryErr
				break
			}
		}
		_, err = transaction.Exec("UPDATE documents SET group_id = ?, sort_order = ? WHERE path_key = ?", groupID, order, pathKey)
	default:
		http.Error(response, "Invalid document action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(response, "Unable to save document", http.StatusInternalServerError)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "Unable to save document", http.StatusInternalServerError)
		return
	}
	http.Redirect(response, request, returnTo, http.StatusSeeOther)
}

func (a *App) moveDocumentToGroupTask(response http.ResponseWriter, request *http.Request) {
	path := strings.TrimSpace(request.URL.Query().Get("path"))
	var groupID sql.NullString
	if err := a.db.QueryRow("SELECT group_id FROM documents WHERE path_key = ?", hostfiles.ComparisonKey(path)).Scan(&groupID); err != nil {
		http.Error(response, "Document not found", http.StatusNotFound)
		return
	}
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "Unable to read groups", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "document-move-group",
		Title:       webText(resolveWebLocale(request), "task.document_move_group.title"),
		Description: webText(resolveWebLocale(request), "task.document_move_group.description"),
		BackURL:     "/resources/documents",
		Action:      "/resources/documents",
		Path:        path,
		Name:        filepath.Base(path),
		GroupID:     groupID.String,
		Groups:      groups,
	})
}

func (a *App) reorderDocuments(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, "文档顺序无效", http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法调整文档顺序", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()

	pathKeys, err := collectQuickRunOrderIDs(transaction, "SELECT path_key FROM documents", request.Form["document_id"])
	if err != nil {
		http.Error(response, "文档已发生变化，请刷新后重试", http.StatusConflict)
		return
	}

	// 批量校验完整清单后再写入，确保拖动排序不会因并发增删而部分覆盖。
	groupPositions := map[string]int{}
	for _, key := range pathKeys {
		var groupID sql.NullString
		if err = transaction.QueryRow("SELECT group_id FROM documents WHERE path_key = ?", key).Scan(&groupID); err != nil {
			break
		}
		groupPositions[groupID.String]++
		if _, err = transaction.Exec("UPDATE documents SET sort_order = ? WHERE path_key = ?", groupPositions[groupID.String], key); err != nil {
			break
		}
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法调整文档顺序", http.StatusInternalServerError)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
