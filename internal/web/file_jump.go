package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/hostfiles"
)

const fileJumpResultLimit = 100
const fileJumpScanLimit = 10000

type fileJumpEntry struct {
	Name  string         `json:"name"`
	Path  string         `json:"path"`
	Kind  hostfiles.Kind `json:"kind"`
	URL   string         `json:"url"`
	Parts []fileNamePart `json:"parts"`
}
type fileJumpView struct {
	Locale                                                                   webLocale
	CurrentPath, Target, Query, Mode, SortField, Direction, ReturnURL, Error string
	ShowHidden, Recursive, All, Partial                                      bool
	Entries                                                                  []fileJumpEntry
	URL                                                                      string `json:"url,omitempty"`
}

var fileJumpTemplate = mustWebTemplate("file-jump")

// Resolve relative input against the visible directory, never the service's working directory.
func fileJumpPath(base, input string) (string, error) {
	if strings.TrimSpace(input) == "" || strings.ContainsRune(input, 0) || strings.Contains(input, "://") {
		return "", os.ErrInvalid
	}
	if !filepath.IsAbs(input) {
		if base == "" || filepath.VolumeName(input) != "" || strings.HasPrefix(input, `\`) {
			return "", os.ErrInvalid
		}
		input = filepath.Join(base, input)
	}
	return filepath.Clean(input), nil
}

func (a *App) fileJumpDestination(ctx context.Context, base, target, sortField, direction string, showHidden bool) (string, error) {
	path, err := fileJumpPath(base, target)
	if err != nil {
		return "", err
	}
	path, err = a.hostCanonicalExisting(ctx, path)
	if err != nil {
		return "", err
	}
	info, _, err := a.hostInfo(ctx, path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if _, err = a.hostList(ctx, path); err != nil {
			return "", err
		}
		return filesStateURL(path, "", sortField, direction, showHidden, 0), nil
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrPermission
	}
	parent := filepath.Dir(path)
	entries, err := a.hostList(ctx, parent)
	if err != nil {
		return "", err
	}
	found := false
	for _, entry := range entries {
		if hostfiles.ComparisonKey(entry.Path) == hostfiles.ComparisonKey(path) {
			showHidden = showHidden || entry.Hidden
			found = true
			break
		}
	}
	if !found {
		return "", os.ErrNotExist
	}
	destination, _ := url.Parse(filesStateURL(parent, "", sortField, direction, showHidden, 0))
	values := destination.Query()
	values.Set("focus_path", path)
	destination.RawQuery = values.Encode()
	return destination.String(), nil
}

func fileJumpError(locale webLocale, err error) string {
	key := "jump.failed"
	switch {
	case errors.Is(err, os.ErrNotExist):
		key = "jump.missing"
	case errors.Is(err, os.ErrPermission), errors.Is(err, hostfiles.ErrProtected):
		key = "jump.denied"
	case errors.Is(err, os.ErrInvalid):
		key = "jump.invalid"
	}
	return webText(locale, key)
}

// Search through the same host-file seam as browsing, including broker deployments.
// Budgets are checked between directory reads; filesystem reads retain their own transport limits.
func (a *App) searchFileJump(ctx context.Context, view *fileJumpView) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	queue := []string{view.CurrentPath}
	if view.All {
		roots, err := a.hostRoots(ctx)
		if err != nil {
			return err
		}
		queue = nil
		for _, root := range roots {
			if root.Kind == hostfiles.Directory {
				queue = append(queue, root.Path)
			}
		}
	}
	visited := make(map[string]bool)
	scanned := 0
	for len(queue) > 0 {
		if ctx.Err() != nil {
			view.Partial = true
			break
		}
		directory := queue[0]
		queue = queue[1:]
		key := hostfiles.ComparisonKey(directory)
		if visited[key] {
			continue
		}
		visited[key] = true
		entries, err := a.hostList(ctx, directory)
		if err != nil {
			if !view.All && len(visited) == 1 && ctx.Err() == nil {
				return err
			}
			view.Partial = true
			continue
		}
		for _, entry := range entries {
			scanned++
			if scanned > fileJumpScanLimit || ctx.Err() != nil {
				view.Partial = true
				return nil
			}
			if entry.Kind == hostfiles.Restricted || (!view.ShowHidden && entry.Hidden) {
				continue
			}
			if (view.Recursive || view.All) && entry.Kind == hostfiles.Directory {
				queue = append(queue, entry.Path)
			}
			if !fileNameMatches(entry.Name, view.Query) {
				continue
			}
			if len(view.Entries) == fileJumpResultLimit {
				view.Partial = true
				return nil
			}
			values := url.Values{"path": {view.CurrentPath}, "target": {entry.Path}, "sort": {view.SortField}, "direction": {view.Direction}}
			if view.ShowHidden {
				values.Set("show_hidden", "1")
			}
			view.Entries = append(view.Entries, fileJumpEntry{Name: entry.Name, Path: entry.Path, Kind: entry.Kind, Parts: splitFileNameMatches(entry.Name, view.Query), URL: "/resources/files/jump?" + values.Encode()})
		}
	}
	return nil
}

func (a *App) fileJumpPage(response http.ResponseWriter, request *http.Request) {
	values := request.URL.Query()
	sortField, direction := normalizeFileSort(values.Get("sort"), values.Get("direction"))
	base := values.Get("path")
	if base == "" {
		base = a.files.InitialBrowsePath()
	}
	view := fileJumpView{Locale: resolveWebLocale(request), CurrentPath: base, Target: base, Query: strings.TrimSpace(values.Get("q")), Mode: "address", SortField: sortField, Direction: direction, ShowHidden: values.Get("show_hidden") == "1", Recursive: values.Get("scope") != "current", All: values.Get("scope") == "all", Entries: []fileJumpEntry{}}
	view.ReturnURL = filesStateURL(base, "", sortField, direction, view.ShowHidden, 0)
	if values.Get("mode") == "search" {
		view.Mode = "search"
	}
	var err error
	if base != "" {
		view.CurrentPath, err = a.hostCanonicalDirectory(request.Context(), base)
	}
	if err == nil {
		if values.Has("target") {
			view.Target = values.Get("target")
			view.URL, err = a.fileJumpDestination(request.Context(), view.CurrentPath, view.Target, sortField, direction, view.ShowHidden)
		} else if view.Mode == "search" && view.Query != "" {
			if len([]rune(view.Query)) > 200 {
				err = os.ErrInvalid
			} else {
				err = a.searchFileJump(request.Context(), &view)
			}
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	if err != nil {
		view.Error = fileJumpError(view.Locale, err)
	}
	if values.Get("format") == "json" {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err != nil {
			response.WriteHeader(http.StatusBadRequest)
		}
		_ = json.NewEncoder(response).Encode(view)
		return
	}
	if view.URL != "" {
		http.Redirect(response, request, view.URL, http.StatusSeeOther)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = fileJumpTemplate.Execute(response, view)
}

// All file-location links share this normalization so the URL and visible page agree.
func fileFocusRequest(request *http.Request, listing []listedFile) *http.Request {
	focus := request.URL.Query().Get("focus_path")
	if focus == "" {
		return request
	}
	for index, entry := range listing {
		if hostfiles.ComparisonKey(entry.Path) != hostfiles.ComparisonKey(focus) {
			continue
		}
		cloned := request.Clone(request.Context())
		copiedURL := *request.URL
		cloned.URL = &copiedURL
		values := cloned.URL.Query()
		values.Set("page", strconv.Itoa(index/listPageSize+1))
		values.Del("q")
		values.Set("focus_path", entry.Path)
		if entry.Hidden {
			values.Set("show_hidden", "1")
		}
		cloned.URL.RawQuery = values.Encode()
		return cloned
	}
	return request
}
