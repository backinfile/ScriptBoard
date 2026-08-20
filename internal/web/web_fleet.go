package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"scriptboard/internal/fleetstatus"
)

type fleetNodeSettingsView struct {
	Locale     webLocale
	Navigation settingsNavigationData
	CSRFToken  string
	Tokens     []fleetstatus.AccessToken
}

type fleetNodeFormView struct {
	Locale                           webLocale
	CSRFToken, Name, Endpoint, Error string
}

type fleetTokenFormView struct {
	Locale                               webLocale
	CSRFToken, Label, IssuedToken, Error string
}

func (a *App) fleetNodeSettingsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	tokens, err := a.fleetStatus.ListAccessTokens(request.Context())
	if err != nil {
		http.Error(response, webText(locale, "fleet.load_failed"), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = fleetNodeSettingsTemplate.Execute(response, fleetNodeSettingsView{
		Locale: locale, Navigation: newSettingsNavigation(current, locale, "nodes"), CSRFToken: current.csrfToken, Tokens: tokens,
	})
}

func (a *App) createFleetAccessToken(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "error.csrf"), http.StatusForbidden)
		return
	}
	label := request.FormValue("label")
	_, secret, err := a.fleetStatus.CreateAccessToken(request.Context(), label)
	view := fleetTokenFormView{Locale: locale, CSRFToken: current.csrfToken, Label: label, IssuedToken: secret}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		view.Error = err.Error()
		response.WriteHeader(http.StatusUnprocessableEntity)
	} else {
		a.recordAuditForRequest(request, "fleet_access_token_created", strings.TrimSpace(label), "succeeded")
	}
	_ = fleetTokenFormTemplate.Execute(response, view)
}

func (a *App) newFleetAccessTokenTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = fleetTokenFormTemplate.Execute(response, fleetTokenFormView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken})
}

func (a *App) revokeFleetAccessToken(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "error.csrf"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.fleetStatus.RevokeAccessToken(request.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(response, request)
			return
		}
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "fleet_access_token_revoked", id, "succeeded")
	http.Redirect(response, request, "/settings/nodes", http.StatusSeeOther)
}

func (a *App) newFleetNodeTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = fleetNodeFormTemplate.Execute(response, fleetNodeFormView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken})
}

func (a *App) createFleetNode(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "error.csrf"), http.StatusForbidden)
		return
	}
	input := fleetstatus.AddPeerInput{Name: request.FormValue("name"), Endpoint: request.FormValue("endpoint"), AccessToken: request.FormValue("access_token")}
	peer, err := a.fleetStatus.AddPeer(request.Context(), input)
	if err != nil {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusUnprocessableEntity)
		_ = fleetNodeFormTemplate.Execute(response, fleetNodeFormView{Locale: locale, CSRFToken: current.csrfToken, Name: input.Name, Endpoint: input.Endpoint, Error: err.Error()})
		return
	}
	a.recordAuditForRequest(request, "fleet_node_added", peer.ID+" "+peer.Name, "succeeded")
	http.Redirect(response, request, "/monitor", http.StatusSeeOther)
}

func (a *App) deleteFleetNode(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "error.csrf"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.fleetStatus.DeletePeer(request.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(response, request)
			return
		}
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "fleet_node_removed", id, "succeeded")
	http.Redirect(response, request, "/monitor", http.StatusSeeOther)
}

func (a *App) fleetStatusExport(response http.ResponseWriter, request *http.Request) {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if len(authorization) < len("Bearer ")+1 || !strings.EqualFold(authorization[:len("Bearer ")], "Bearer ") || !a.fleetStatus.AuthenticateAccessToken(request.Context(), strings.TrimSpace(authorization[len("Bearer "):])) {
		response.Header().Set("WWW-Authenticate", `Bearer realm="ScriptBoard fleet"`)
		response.Header().Set("Cache-Control", "no-store")
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	overview := a.hostStatus.Current()
	// The fleet interface exposes only bounded resource facts. It never exports
	// paths, interface addresses, collection errors, Runs, logs, or credentials.
	overview.Series = nil
	overview.Errors = nil
	overview.Current.Errors = nil
	overview.Current.CriticalStorage = nil
	overview.Current.Filesystems = nil
	overview.Current.Disks = nil
	overview.Current.Interfaces = nil
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(fleetstatus.Export{ProtocolVersion: fleetstatus.ProtocolVersion, Overview: overview})
}
