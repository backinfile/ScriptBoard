package web

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/hostfiles"
)

const maxFileQuickAccessPins = 30

type fileQuickAccessPin struct {
	Path    string `json:"path"`
	Label   string `json:"label"`
	Href    string `json:"href"`
	Kind    string `json:"kind"`
	GroupID string `json:"groupId"`
}

func (a *App) quickAccessPins() ([]fileQuickAccessPin, error) {
	rows, err := a.db.Query(`SELECT p.path, p.label, p.target_kind, COALESCE(p.group_id,'')
		FROM file_quick_access_pins p LEFT JOIN quick_run_groups g ON g.id=p.group_id
		ORDER BY CASE WHEN p.group_id IS NULL THEN 1 ELSE 0 END, g.sort_order, p.sort_order, p.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pins := make([]fileQuickAccessPin, 0, maxFileQuickAccessPins)
	for rows.Next() {
		var pin fileQuickAccessPin
		if err := rows.Scan(&pin.Path, &pin.Label, &pin.Kind, &pin.GroupID); err != nil {
			return nil, err
		}
		pin.Href = fileQuickAccessHref(pin.Path, pin.Kind)
		pins = append(pins, pin)
	}
	return pins, rows.Err()
}

func (a *App) fileQuickAccessPins(response http.ResponseWriter, request *http.Request) {
	pins, err := a.quickAccessPins()
	if err != nil {
		http.Error(response, "Unable to read Quick access", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	groups, _ := a.loadRecordGroups()
	_ = json.NewEncoder(response).Encode(map[string]any{"pins": pins, "groups": groups})
}

func (a *App) updateFileQuickAccessPin(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	action := strings.TrimSpace(request.FormValue("action"))
	if action == "" {
		if request.FormValue("pinned") == "true" {
			action = "pin"
		} else {
			action = "unpin"
		}
	}
	path := strings.TrimSpace(request.FormValue("path"))
	if path == "" && action != "reorder" {
		http.Error(response, "Path is required", http.StatusBadRequest)
		return
	}
	pathKey := hostfiles.ComparisonKey(path)
	var targetKind string
	var renameLabel string
	var renameGroupValue string
	if action == "pin" {
		canonical, err := a.hostCanonicalExisting(request.Context(), path)
		if err != nil {
			writeHostFileError(response, "Unable to pin path", err)
			return
		}
		info, _, err := a.hostInfo(request.Context(), canonical)
		if err != nil {
			writeHostFileError(response, "Unable to pin path", err)
			return
		}
		path = canonical
		pathKey = hostfiles.ComparisonKey(canonical)
		targetKind = "file"
		if info.IsDir() {
			targetKind = "directory"
		}
	} else if action == "rename" {
		renameLabel = strings.TrimSpace(request.FormValue("label"))
		if renameLabel == "" || utf8.RuneCountInString(renameLabel) > 128 {
			http.Error(response, "Display name must be between 1 and 128 characters", http.StatusBadRequest)
			return
		}
		renameGroupValue = request.FormValue("group_id")
	}

	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "Unable to save Quick access", http.StatusInternalServerError)
		return
	}
	defer func() { _ = transaction.Rollback() }()
	switch action {
	case "pin":
		label := filepath.Base(path)
		if volume := filepath.VolumeName(path); volume != "" && filepath.Clean(path) == filepath.Clean(volume+string(filepath.Separator)) {
			label = path
		}
		if label == "." || label == string(filepath.Separator) || label == "" {
			label = path
		}
		_, err = transaction.Exec(`INSERT INTO file_quick_access_pins
			(path, path_key, label, target_kind, group_id, sort_order, created_at)
			VALUES (?, ?, ?, ?, NULL, COALESCE((SELECT MAX(sort_order) + 1 FROM file_quick_access_pins WHERE group_id IS NULL), 1), ?)
			ON CONFLICT(path_key) DO UPDATE SET path = excluded.path, target_kind = excluded.target_kind`,
			path, pathKey, label, targetKind, time.Now().UTC().UnixNano())
		if err == nil {
			_, err = transaction.Exec(`DELETE FROM file_quick_access_pins WHERE path_key IN (
				SELECT path_key FROM file_quick_access_pins ORDER BY sort_order DESC, created_at DESC LIMIT -1 OFFSET ?
			)`, maxFileQuickAccessPins)
		}
	case "unpin":
		_, err = transaction.Exec("DELETE FROM file_quick_access_pins WHERE path_key = ?", pathKey)
	case "rename":
		// Resolve through the transaction so the single-connection SQLite pool
		// keeps validation and membership update atomic without self-waiting.
		renameGroupID, groupErr := resolveRecordGroupIDWith(transaction, renameGroupValue)
		if groupErr != nil {
			http.Error(response, "Quick access group not found", http.StatusBadRequest)
			return
		}
		var currentGroup sql.NullString
		var order int
		if queryErr := transaction.QueryRow("SELECT group_id, sort_order FROM file_quick_access_pins WHERE path_key=?", pathKey).Scan(&currentGroup, &order); queryErr != nil {
			http.Error(response, "Quick access item not found", http.StatusNotFound)
			return
		}
		if currentGroup.String != valueOrEmpty(renameGroupID) {
			if queryErr := transaction.QueryRow("SELECT COALESCE(MAX(sort_order),0)+1 FROM file_quick_access_pins WHERE group_id IS ?", renameGroupID).Scan(&order); queryErr != nil {
				err = queryErr
				break
			}
		}
		result, updateErr := transaction.Exec("UPDATE file_quick_access_pins SET label = ?, group_id=?, sort_order=? WHERE path_key = ?", renameLabel, renameGroupID, order, pathKey)
		err = updateErr
		if err == nil {
			if changed, _ := result.RowsAffected(); changed != 1 {
				http.Error(response, "Quick access item not found", http.StatusNotFound)
				return
			}
		}
	case "reorder":
		var orderedPaths []string
		if decodeErr := json.Unmarshal([]byte(request.FormValue("order")), &orderedPaths); decodeErr != nil {
			http.Error(response, "Invalid Quick access order", http.StatusBadRequest)
			return
		}
		rows, queryErr := transaction.Query("SELECT path_key FROM file_quick_access_pins")
		if queryErr != nil {
			err = queryErr
			break
		}
		existing := map[string]bool{}
		for rows.Next() {
			var key string
			if scanErr := rows.Scan(&key); scanErr != nil {
				err = scanErr
				break
			}
			existing[key] = true
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		seen := map[string]bool{}
		if err == nil {
			for index, orderedPath := range orderedPaths {
				key := hostfiles.ComparisonKey(strings.TrimSpace(orderedPath))
				if !existing[key] || seen[key] {
					err = fmt.Errorf("order does not match saved Quick access items")
					break
				}
				seen[key] = true
				_, err = transaction.Exec("UPDATE file_quick_access_pins SET sort_order = ? WHERE path_key = ?", index+1, key)
				if err != nil {
					break
				}
			}
			if err == nil && len(seen) != len(existing) {
				err = fmt.Errorf("order does not include every saved Quick access item")
			}
		}
	default:
		http.Error(response, "Invalid Quick access action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(response, "Unable to save Quick access", http.StatusInternalServerError)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "Unable to save Quick access", http.StatusInternalServerError)
		return
	}

	pins, err := a.quickAccessPins()
	if err != nil {
		http.Error(response, "Unable to read Quick access", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	groups, _ := a.loadRecordGroups()
	_ = json.NewEncoder(response).Encode(map[string]any{"pins": pins, "groups": groups})
}

func fileQuickAccessHref(path, kind string) string {
	if kind != "file" {
		return filesURL(path)
	}
	parent, ok := hostPathParent(path)
	if !ok {
		parent = ""
	}
	values := url.Values{"focus_path": {path}}
	if parent != "" {
		values.Set("path", parent)
	}
	return "/resources/files?" + values.Encode()
}

type fileCategory string

const (
	fileCategoryDirectory  fileCategory = "directory"
	fileCategoryRestricted fileCategory = "restricted"
	fileCategoryScript     fileCategory = "script"
	fileCategoryImage      fileCategory = "image"
	fileCategoryText       fileCategory = "text"
	fileCategoryOther      fileCategory = "other"
)

type listedFile struct {
	hostfiles.Entry
	Path              string
	Category          fileCategory
	DisplayCategory   fileCategory
	PreviewableText   bool
	ContentClassified bool
}

func (file listedFile) visibleCategory() fileCategory {
	if file.ContentClassified {
		return file.DisplayCategory
	}
	return file.Category
}

type fileNamePart struct {
	Text  string
	Match bool
}

func prepareFileListingWithContent(entries []hostfiles.Entry, query, sortField, direction string, showHidden bool, classifyContent func(listedFile) (fileCategory, bool)) []listedFile {
	result := make([]listedFile, 0, len(entries))
	for _, entry := range entries {
		if !showHidden && entry.Hidden {
			continue
		}
		if query != "" && !fileNameMatches(entry.Name, query) {
			continue
		}
		path := entry.Path
		listed := listedFile{
			Entry:    entry,
			Path:     path,
			Category: classifyFile(entry, path),
		}
		if sortField == "type" && classifyContent != nil {
			listed.DisplayCategory, listed.PreviewableText = classifyContent(listed)
			listed.ContentClassified = true
		}
		result = append(result, listed)
	}
	if sortField == "" {
		return result
	}
	sort.SliceStable(result, func(left, right int) bool {
		comparison := compareListedFiles(result[left], result[right], sortField)
		if direction == "desc" && result[left].visibleCategory() != fileCategoryDirectory && result[right].visibleCategory() != fileCategoryDirectory {
			comparison = -comparison
		}
		return comparison < 0
	})
	return result
}

func normalizeFileSort(field, direction string) (string, string) {
	switch field {
	case "name", "type", "size", "modified":
	default:
		field = ""
	}
	if field == "" || direction != "desc" {
		direction = "asc"
	}
	return field, direction
}

func compareListedFiles(left, right listedFile, field string) int {
	leftCategory, rightCategory := left.visibleCategory(), right.visibleCategory()
	if leftCategory == fileCategoryDirectory && rightCategory != fileCategoryDirectory {
		return -1
	}
	if leftCategory != fileCategoryDirectory && rightCategory == fileCategoryDirectory {
		return 1
	}

	comparison := 0
	switch field {
	case "type":
		comparison = cmp.Compare(fileCategoryRank(leftCategory), fileCategoryRank(rightCategory))
	case "size":
		comparison = cmp.Compare(left.Size, right.Size)
	case "modified":
		comparison = left.ModifiedAt.Compare(right.ModifiedAt)
	}
	if field == "name" || comparison == 0 {
		comparison = naturalNameCompare(left.Name, right.Name)
	}
	return comparison
}

func classifyFile(entry hostfiles.Entry, path string) fileCategory {
	switch entry.Kind {
	case hostfiles.Directory:
		return fileCategoryDirectory
	case hostfiles.Restricted:
		return fileCategoryRestricted
	}
	if isScriptExtension(path) {
		return fileCategoryScript
	}
	if isImagePreviewExtension(path) {
		return fileCategoryImage
	}
	if isTextPreviewExtension(path) {
		return fileCategoryText
	}
	return fileCategoryOther
}

func isImagePreviewExtension(path string) bool {
	switch strings.ToLower(hostfiles.Extension(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func fileCategoryRank(category fileCategory) int {
	switch category {
	case fileCategoryDirectory:
		return 0
	case fileCategoryRestricted:
		return 1
	case fileCategoryScript:
		return 2
	case fileCategoryImage:
		return 3
	case fileCategoryText:
		return 4
	default:
		return 5
	}
}

func fileCategoryIcon(category fileCategory) string {
	switch category {
	case fileCategoryDirectory:
		return "folder"
	case fileCategoryRestricted:
		return "file-lock-2"
	case fileCategoryScript:
		return "file-terminal"
	case fileCategoryImage:
		return "image"
	case fileCategoryText:
		return "file-text"
	default:
		return "file"
	}
}

func fileCategoryLabel(locale webLocale, category fileCategory) string {
	return webText(locale, "files.type."+string(category))
}

func fileSortSummary(locale webLocale, field, direction string) string {
	if field == "" {
		return webText(locale, "files.natural_order")
	}
	fieldLabel := webText(locale, "files.sort."+field)
	directionKey := "files.ascending"
	if direction == "desc" {
		directionKey = "files.descending"
	}
	directionLabel := webText(locale, directionKey)
	return fieldLabel + " · " + directionLabel
}

func filesStateURL(relative, query, sortField, direction string, showHidden bool, page int) string {
	values := url.Values{}
	if relative != "" {
		values.Set("path", relative)
	}
	if query != "" {
		values.Set("q", query)
	}
	if sortField != "" {
		values.Set("sort", sortField)
		values.Set("direction", direction)
	}
	if showHidden {
		values.Set("show_hidden", "1")
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if len(values) == 0 {
		return "/resources/files"
	}
	return "/resources/files?" + values.Encode()
}

func naturalNameCompare(left, right string) int {
	a, b := strings.ToLower(left), strings.ToLower(right)
	for len(a) > 0 && len(b) > 0 {
		if isASCIIDigit(a[0]) && isASCIIDigit(b[0]) {
			aIndex, bIndex := 0, 0
			for aIndex < len(a) && isASCIIDigit(a[aIndex]) {
				aIndex++
			}
			for bIndex < len(b) && isASCIIDigit(b[bIndex]) {
				bIndex++
			}
			an, bn := strings.TrimLeft(a[:aIndex], "0"), strings.TrimLeft(b[:bIndex], "0")
			if an == "" {
				an = "0"
			}
			if bn == "" {
				bn = "0"
			}
			if len(an) != len(bn) {
				return cmp.Compare(len(an), len(bn))
			}
			if an != bn {
				return strings.Compare(an, bn)
			}
			a, b = a[aIndex:], b[bIndex:]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func splitFileNameMatches(name, query string) []fileNamePart {
	nameRunes := []rune(name)
	queryRunes := []rune(query)
	if len(queryRunes) == 0 || len(queryRunes) > len(nameRunes) {
		return []fileNamePart{{Text: name}}
	}

	foldedName := foldRunes(nameRunes)
	foldedQuery := foldRunes(queryRunes)
	parts := make([]fileNamePart, 0, 3)
	start := 0
	for index := 0; index+len(foldedQuery) <= len(foldedName); {
		if !equalRunes(foldedName[index:index+len(foldedQuery)], foldedQuery) {
			index++
			continue
		}
		if start < index {
			parts = append(parts, fileNamePart{Text: string(nameRunes[start:index])})
		}
		parts = append(parts, fileNamePart{Text: string(nameRunes[index : index+len(queryRunes)]), Match: true})
		index += len(queryRunes)
		start = index
	}
	if start < len(nameRunes) {
		parts = append(parts, fileNamePart{Text: string(nameRunes[start:])})
	}
	if len(parts) == 0 {
		return []fileNamePart{{Text: name}}
	}
	return parts
}

func fileNameMatches(name, query string) bool {
	for _, part := range splitFileNameMatches(name, query) {
		if part.Match {
			return true
		}
	}
	return false
}

func foldRunes(values []rune) []rune {
	result := make([]rune, len(values))
	for index, value := range values {
		result[index] = unicode.ToLower(value)
	}
	return result
}

func equalRunes(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
