package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/redismanager"
	"scriptboard/internal/secretredaction"
)

type redisDatabasesPageData struct {
	Locale    webLocale
	CSRFToken string
	Instances []redismanager.Instance
	Selected  *redismanager.Instance
	Overview  *redismanager.Overview
	Scan      *redismanager.ScanPage
	Pattern   string
	ActiveTab string
	LoadError string
}

func (a *App) databasesPage(response http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("engine") == "redis" {
		a.redisDatabasesPage(response, request)
		return
	}
	a.mysqlDatabasesPage(response, request)
}

func (a *App) redisDatabasesPage(response http.ResponseWriter, request *http.Request) {
	instances, err := a.redis.Instances(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	data := redisDatabasesPageData{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Instances: instances, ActiveTab: "overview", Pattern: strings.TrimSpace(request.URL.Query().Get("pattern"))}
	if request.URL.Query().Get("tab") == "keys" || request.URL.Query().Get("tab") == "diagnostics" {
		data.ActiveTab = request.URL.Query().Get("tab")
	}
	id := strings.TrimSpace(request.URL.Query().Get("instance"))
	if id == "" && len(instances) > 0 {
		id = instances[0].ID
	}
	for index := range instances {
		if instances[index].ID == id {
			selected := instances[index]
			data.Selected = &selected
			break
		}
	}
	if id != "" && data.Selected == nil {
		http.Error(response, "Redis instance not found", http.StatusNotFound)
		return
	}
	if data.Selected != nil {
		ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
		defer cancel()
		switch data.ActiveTab {
		case "keys":
			scan, scanErr := a.redis.Scan(ctx, id, redismanager.ScanRequest{Pattern: data.Pattern, Count: 200})
			if scanErr != nil {
				data.LoadError = secretredaction.String(scanErr.Error())
			} else {
				data.Scan = &scan
			}
		case "overview":
			overview, overviewErr := a.redis.Overview(ctx, id)
			if overviewErr != nil {
				data.LoadError = secretredaction.String(overviewErr.Error())
			} else {
				data.Overview = &overview
			}
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = redisDatabasesTemplate.Execute(response, data)
}

func (a *App) saveRedisInstance(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(request.FormValue("port"))
	if err != nil {
		http.Error(response, "invalid Redis port", http.StatusBadRequest)
		return
	}
	database, err := strconv.Atoi(request.FormValue("database"))
	if err != nil {
		http.Error(response, "invalid Redis database", http.StatusBadRequest)
		return
	}
	instance, err := a.redis.SaveInstance(request.Context(), redismanager.InstanceInput{ID: request.FormValue("id"), Name: request.FormValue("name"), Environment: redismanager.Environment(request.FormValue("environment")), Host: request.FormValue("host"), Port: port, Username: request.FormValue("username"), Password: request.FormValue("password"), Database: database, TLSMode: redismanager.TLSMode(request.FormValue("tls_mode")), CAPath: request.FormValue("ca_path")})
	if err != nil {
		http.Error(response, secretredaction.String(err.Error()), http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, "/resources/databases?engine=redis&instance="+url.QueryEscape(instance.ID), http.StatusSeeOther)
}

func (a *App) testRedisInstance(response http.ResponseWriter, request *http.Request) {
	result, err := a.redis.TestInstance(request.Context(), request.PathValue("id"))
	response.Header().Set("Content-Type", "application/json")
	if err != nil {
		http.Error(response, secretredaction.String(err.Error()), http.StatusBadGateway)
		return
	}
	_ = json.NewEncoder(response).Encode(struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}{OK: result.OK, Version: result.Version})
}
func (a *App) deleteRedisInstance(response http.ResponseWriter, request *http.Request) {
	if err := a.redis.DeleteInstance(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, secretredaction.String(err.Error()), http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, "/resources/databases?engine=redis", http.StatusSeeOther)
}
