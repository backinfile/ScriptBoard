package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"scriptboard/internal/appstatus"
)

type containersPageView struct {
	appstatus.ContainerView
	Locale    webLocale
	CSRFToken string
	Query     appstatus.ContainerQuery
	CanManage bool
}

type containerOperationConfirmView struct {
	Locale                            webLocale
	CSRFToken, Name, Action, ReturnTo string
}

func containerSortURL(query appstatus.ContainerQuery, field string) string {
	direction := "asc"
	if query.Sort == field && query.Direction == "asc" {
		direction = "desc"
	}
	query.Sort, query.Direction = field, direction
	return containerQueryURL(query)
}

func containerStatusURL(query appstatus.ContainerQuery, status string) string {
	query.Status = status
	return containerQueryURL(query)
}

func containerQueryURL(query appstatus.ContainerQuery) string {
	values := url.Values{}
	if query.Search != "" {
		values.Set("query", query.Search)
	}
	if query.Status != "" && query.Status != "all" {
		values.Set("status", query.Status)
	}
	if query.Sort != "" {
		values.Set("sort", query.Sort)
	}
	if query.Direction != "" {
		values.Set("direction", query.Direction)
	}
	if encoded := values.Encode(); encoded != "" {
		return "/monitor/containers?" + encoded
	}
	return "/monitor/containers"
}

func parseContainerQuery(request *http.Request) (appstatus.ContainerQuery, error) {
	query := appstatus.ContainerQuery{
		Search: strings.TrimSpace(request.URL.Query().Get("query")), Status: request.URL.Query().Get("status"),
		Sort: request.URL.Query().Get("sort"), Direction: request.URL.Query().Get("direction"), Limit: 100,
	}
	if query.Status == "" {
		query.Status = "all"
	}
	if query.Sort == "" {
		query.Sort = "state"
	}
	if query.Direction == "" {
		query.Direction = "asc"
	}
	switch query.Status {
	case "all", "running", "stopped", "attention":
	default:
		return appstatus.ContainerQuery{}, errors.New("invalid container status")
	}
	switch query.Sort {
	case "name", "state", "cpu", "memory", "read", "write", "ports":
	default:
		return appstatus.ContainerQuery{}, errors.New("invalid container sort field")
	}
	if query.Direction != "asc" && query.Direction != "desc" {
		return appstatus.ContainerQuery{}, errors.New("invalid container sort direction")
	}
	return query, nil
}

func (a *App) loadContainers(request *http.Request, query appstatus.ContainerQuery) (appstatus.ContainerView, error) {
	view, err := a.applicationStatus.ContainerView(request.Context(), query)
	if err != nil {
		return appstatus.ContainerView{}, err
	}
	if view.CollectedAt.IsZero() {
		if err := a.applicationStatus.Refresh(request.Context()); err != nil {
			return appstatus.ContainerView{}, err
		}
		return a.applicationStatus.ContainerView(request.Context(), query)
	}
	return view, nil
}

func (a *App) containersPage(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	query, err := parseContainerQuery(request)
	if err != nil {
		query = appstatus.ContainerQuery{Status: "all", Sort: "state", Direction: "asc", Limit: 100}
	}
	view, err := a.loadContainers(request, query)
	if err != nil {
		http.Error(response, "Unable to read container status", http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = containersTemplate.Execute(response, containersPageView{
		ContainerView: view, Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Query: query,
		CanManage: roleAllows(current.role, permissionManageOperations),
	})
}

func (a *App) containersData(response http.ResponseWriter, request *http.Request) {
	query, err := parseContainerQuery(request)
	if err != nil {
		http.Error(response, "Invalid container query", http.StatusBadRequest)
		return
	}
	view, err := a.loadContainers(request, query)
	if err != nil {
		http.Error(response, "Unable to read container status", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(view)
}

func (a *App) containerDetails(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if _, err := a.loadContainers(request, appstatus.ContainerQuery{Limit: 1}); err != nil {
		writeApplicationJSONError(response, http.StatusInternalServerError, "snapshot_unavailable", "Unable to read container status")
		return
	}
	details, err := a.applicationStatus.ContainerDetails(request.Context(), request.PathValue("name"), request.URL.Query().Get("range"))
	if errors.Is(err, appstatus.ErrApplicationNotFound) {
		writeApplicationJSONError(response, http.StatusNotFound, "container_not_found", "Container not found")
		return
	}
	if err != nil {
		writeApplicationJSONError(response, http.StatusInternalServerError, "details_unavailable", "Unable to read container details")
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(details)
}

func (a *App) pinContainer(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	name := request.PathValue("name")
	if err := a.applicationStatus.PinContainer(request.Context(), name); err != nil {
		http.Error(response, "Unable to pin container", http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "pin_container", name, "succeeded")
	http.Redirect(response, request, containerReturnTo(request), http.StatusSeeOther)
}

func (a *App) unpinContainer(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	name := request.PathValue("name")
	if err := a.applicationStatus.UnpinContainer(request.Context(), name); err != nil {
		http.Error(response, "Unable to unpin container", http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "unpin_container", name, "succeeded")
	http.Redirect(response, request, containerReturnTo(request), http.StatusSeeOther)
}

func (a *App) movePinnedContainer(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	name := request.PathValue("name")
	if err := a.applicationStatus.MovePinnedContainer(request.Context(), name, request.FormValue("direction")); err != nil {
		http.Error(response, "Unable to move pinned container", http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "move_pinned_container", name, "succeeded")
	http.Redirect(response, request, containerReturnTo(request), http.StatusSeeOther)
}

func (a *App) operateContainer(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	name := request.PathValue("name")
	action := appstatus.ContainerAction(request.FormValue("action"))
	if action != appstatus.ContainerStart && action != appstatus.ContainerStop && action != appstatus.ContainerRestart {
		http.Error(response, "Unsupported container operation", http.StatusBadRequest)
		return
	}
	returnTo := containerReturnTo(request)
	if request.FormValue("confirmed") != "yes" {
		current := request.Context().Value(sessionContextKey).(session)
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = containerOperationConfirmTemplate.Execute(response, containerOperationConfirmView{
			Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Name: name, Action: string(action), ReturnTo: returnTo,
		})
		return
	}
	if err := a.applicationStatus.OperateContainer(request.Context(), name, action); err != nil {
		http.Error(response, "Container operation failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	a.recordAuditForRequest(request, "operate_container", name+":"+string(action), "succeeded")
	http.Redirect(response, request, returnTo, http.StatusSeeOther)
}

func containerReturnTo(request *http.Request) string {
	value := request.FormValue("return_to")
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path != "/monitor/containers" {
		return "/monitor/containers"
	}
	return parsed.RequestURI()
}
