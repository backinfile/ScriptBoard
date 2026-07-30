package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"scriptboard/internal/appstatus"
)

type applicationsPageView struct {
	appstatus.View
	Locale    webLocale
	CSRFToken string
	Query     appstatus.Query
}

func parseApplicationsQuery(request *http.Request) (appstatus.Query, error) {
	query := appstatus.Query{
		Search:    strings.TrimSpace(request.URL.Query().Get("query")),
		Sort:      request.URL.Query().Get("sort"),
		Direction: request.URL.Query().Get("direction"),
		Limit:     100,
	}
	switch request.URL.Query().Get("kind") {
	case "", "all":
	case "host":
		query.Kind = appstatus.KindHost
	case "docker":
		query.Kind = appstatus.KindDocker
	default:
		return appstatus.Query{}, errors.New("invalid application kind")
	}
	switch query.Sort {
	case "":
		query.Sort = "cpu"
	case "pinned", "name", "cpu", "memory", "read", "write", "processes":
	default:
		return appstatus.Query{}, errors.New("invalid application sort field")
	}
	switch query.Direction {
	case "":
		query.Direction = "desc"
	case "asc", "desc":
	default:
		return appstatus.Query{}, errors.New("invalid application sort direction")
	}
	return query, nil
}

func (a *App) loadApplications(request *http.Request, query appstatus.Query) (appstatus.View, error) {
	view, err := a.applicationStatus.View(request.Context(), query)
	if err != nil {
		return appstatus.View{}, err
	}
	if view.CollectedAt.IsZero() {
		if err := a.applicationStatus.Refresh(request.Context()); err != nil {
			return appstatus.View{}, err
		}
		return a.applicationStatus.View(request.Context(), query)
	}
	return view, nil
}

func (a *App) applicationsPage(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	query, err := parseApplicationsQuery(request)
	if err != nil {
		query = appstatus.Query{Sort: "cpu", Direction: "desc", Limit: 100}
	}
	view, err := a.loadApplications(request, query)
	if err != nil {
		http.Error(response, "Unable to read application status", http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	page := applicationsPageView{
		View: view, Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Query: query,
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = applicationsTemplate.Execute(response, page)
}

func (a *App) applicationsData(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	query, err := parseApplicationsQuery(request)
	if err != nil {
		http.Error(response, "Invalid application query", http.StatusBadRequest)
		return
	}
	view, err := a.loadApplications(request, query)
	if err != nil {
		http.Error(response, "Unable to read application status", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(view)
}

func (a *App) pinApplication(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.applicationStatus.Pin(request.Context(), id); err != nil {
		http.Error(response, "Unable to Pin application", http.StatusBadRequest)
		return
	}
	a.recordAudit("pin_application", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/applications", http.StatusSeeOther)
}

func (a *App) unpinApplication(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.applicationStatus.Unpin(request.Context(), id); err != nil {
		http.Error(response, "Unable to unpin application", http.StatusBadRequest)
		return
	}
	a.recordAudit("unpin_application", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/applications", http.StatusSeeOther)
}

func (a *App) applicationDetails(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if _, err := a.loadApplications(request, appstatus.Query{Limit: 1}); err != nil {
		writeApplicationJSONError(response, http.StatusInternalServerError, "snapshot_unavailable", "Unable to read application status")
		return
	}
	details, err := a.applicationStatus.Details(
		request.Context(),
		request.PathValue("id"),
		request.URL.Query().Get("range"),
	)
	if errors.Is(err, appstatus.ErrApplicationNotFound) {
		writeApplicationJSONError(response, http.StatusNotFound, "application_not_found", "Application not found")
		return
	}
	if err != nil {
		if errors.Is(err, appstatus.ErrUnsupportedHistoryRange) {
			writeApplicationJSONError(response, http.StatusBadRequest, "invalid_history_range", "Invalid application history range")
			return
		}
		writeApplicationJSONError(response, http.StatusInternalServerError, "details_unavailable", "Unable to read application details")
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(details)
}

func writeApplicationJSONError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func (a *App) movePinnedApplication(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	direction := request.FormValue("direction")
	if err := a.applicationStatus.MovePin(request.Context(), id, direction); err != nil {
		http.Error(response, "Unable to move pinned application", http.StatusBadRequest)
		return
	}
	a.recordAudit("move_pinned_application", id, "succeeded", request.RemoteAddr)
	if acceptsJSON(request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(map[string]string{
			"id": id, "direction": strings.ToLower(strings.TrimSpace(direction)),
		})
		return
	}
	http.Redirect(response, request, "/monitor/applications", http.StatusSeeOther)
}
