package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/customtab"
	"scriptboard/internal/identity"
)

type customTabsPageView struct {
	Locale    webLocale
	CSRFToken string
	Tabs      []customTabView
	Reorder   bool
}

type customTabView struct {
	customtab.Tab
	CanMoveUp, CanMoveDown, Insecure bool
}

type customTabFrameView struct {
	Locale                                                       webLocale
	CSRFToken, ID, Name, TargetURL, Origin                       string
	Sandbox, ChallengeEndpoint, DeliveryEndpoint, CredentialMode string
}

type customTabChallenge struct {
	SessionHash, TabID, Origin, Nonce string
	ExpiresAt                         time.Time
}

func (a *App) customTabsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	tabs, err := a.customTabs.List(request.Context())
	if err != nil {
		http.Error(response, "无法读取自定义页签", http.StatusInternalServerError)
		return
	}
	view := customTabsPageView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Reorder: request.URL.Query().Get("reorder") == "1"}
	for index, tab := range tabs {
		view.Tabs = append(view.Tabs, customTabView{Tab: tab, CanMoveUp: index > 0, CanMoveDown: index < len(tabs)-1, Insecure: strings.HasPrefix(tab.TargetURL, "http://")})
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = customTabsTemplate.Execute(response, view)
}

func customTabInput(request *http.Request, enabled bool) customtab.Input {
	mode := customtab.CredentialMode(request.FormValue("credential_mode"))
	key := request.FormValue("key")
	return customtab.Input{Name: request.FormValue("name"), TargetURL: request.FormValue("target_url"), CredentialMode: mode, KeyName: request.FormValue("key_name"), Key: key, Enabled: enabled, PreserveKey: mode == customtab.ModeKey && key == ""}
}

func (a *App) createCustomTab(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	if _, err := a.customTabs.Create(request.Context(), customTabInput(request, false)); err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(response, request, "/config/custom-tabs", http.StatusSeeOther)
}

func (a *App) updateCustomTab(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	current, err := a.customTabs.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if _, err = a.customTabs.Update(request.Context(), current.ID, customTabInput(request, current.Enabled)); err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(response, request, "/config/custom-tabs", http.StatusSeeOther)
}

func (a *App) toggleCustomTab(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	enabled, err := strconv.ParseBool(request.FormValue("enabled"))
	if err != nil {
		http.Error(response, "无效的启用状态", http.StatusUnprocessableEntity)
		return
	}
	if _, err = a.customTabs.SetEnabled(request.Context(), request.PathValue("id"), enabled); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(response, request, "/config/custom-tabs", http.StatusSeeOther)
}

func (a *App) moveCustomTab(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("reorder") != "1" {
		http.Error(response, "请先进入调整顺序模式", http.StatusForbidden)
		return
	}
	direction := -1
	if request.FormValue("direction") == "down" {
		direction = 1
	} else if request.FormValue("direction") != "up" {
		http.Error(response, "无效的排序方向", http.StatusUnprocessableEntity)
		return
	}
	if _, err := a.customTabs.Move(request.Context(), request.PathValue("id"), direction); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(response, request, "/config/custom-tabs?reorder=1", http.StatusSeeOther)
}

func (a *App) deleteCustomTab(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "请确认删除页签", http.StatusForbidden)
		return
	}
	if err := a.customTabs.Delete(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(response, request, "/config/custom-tabs", http.StatusSeeOther)
}

func (a *App) customTabFramePage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	tab, err := a.customTabs.Get(request.Context(), request.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || err == nil && !tab.Enabled {
		http.NotFound(response, request)
		return
	}
	if err != nil {
		http.Error(response, "无法读取自定义页签", http.StatusInternalServerError)
		return
	}
	if tab.CredentialMode == customtab.ModeKey && !identity.Allows(current.role, identity.PermissionManageOperations) {
		http.NotFound(response, request)
		return
	}
	sandbox := "allow-scripts allow-forms"
	if tab.CredentialMode != customtab.ModeIsolated {
		sandbox += " allow-same-origin allow-storage-access-by-user-activation"
	}
	response.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; frame-src "+tab.Origin)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = customTabFrameTemplate.Execute(response, customTabFrameView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, ID: tab.ID, Name: tab.Name, TargetURL: tab.TargetURL, Origin: tab.Origin, Sandbox: sandbox, CredentialMode: string(tab.CredentialMode), ChallengeEndpoint: "/defined/tabs/" + tab.ID + "/key-challenge", DeliveryEndpoint: "/defined/tabs/" + tab.ID + "/key-delivery"})
}

func (a *App) customTabKeyChallenge(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	tab, err := a.customTabs.Get(request.Context(), request.PathValue("id"))
	if err != nil || !tab.Enabled || tab.CredentialMode != customtab.ModeKey {
		http.NotFound(response, request)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	nonce, err := randomToken(24)
	if err != nil {
		http.Error(response, "无法创建页签凭据挑战", http.StatusInternalServerError)
		return
	}
	challenge := customTabChallenge{SessionHash: current.tokenHash, TabID: tab.ID, Origin: tab.Origin, Nonce: nonce, ExpiresAt: time.Now().UTC().Add(30 * time.Second)}
	a.customTabChallengeMu.Lock()
	for key, stored := range a.customTabChallenges {
		if time.Now().UTC().After(stored.ExpiresAt) {
			delete(a.customTabChallenges, key)
		}
	}
	a.customTabChallenges[current.tokenHash+":"+tab.ID+":"+nonce] = challenge
	a.customTabChallengeMu.Unlock()
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(map[string]any{"type": "scriptboard.custom-tab.init", "version": 1, "tabId": tab.ID, "origin": tab.Origin, "nonce": nonce, "expiresIn": 30})
}

func (a *App) customTabKeyDelivery(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	nonce := strings.TrimSpace(request.FormValue("nonce"))
	challengeKey := current.tokenHash + ":" + request.PathValue("id") + ":" + nonce
	a.customTabChallengeMu.Lock()
	challenge, ok := a.customTabChallenges[challengeKey]
	delete(a.customTabChallenges, challengeKey)
	a.customTabChallengeMu.Unlock()
	if !ok || challenge.SessionHash != current.tokenHash || challenge.TabID != request.PathValue("id") || challenge.Nonce != nonce || time.Now().UTC().After(challenge.ExpiresAt) {
		http.Error(response, "页签凭据挑战无效或已过期", http.StatusForbidden)
		return
	}
	tab, err := a.customTabs.Get(request.Context(), request.PathValue("id"))
	if err != nil || !tab.Enabled || tab.CredentialMode != customtab.ModeKey || tab.Origin != challenge.Origin {
		http.NotFound(response, request)
		return
	}
	name, value, err := a.customTabs.Credential(request.Context(), tab.ID)
	if err != nil {
		a.recordAuditForRequest(request, "deliver_custom_tab_key", tab.ID, "failed")
		http.Error(response, "无法读取页签凭据", http.StatusConflict)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	a.recordAuditForRequest(request, "deliver_custom_tab_key", tab.ID, "succeeded")
	_ = json.NewEncoder(response).Encode(map[string]any{"type": "scriptboard.custom-tab.credential", "version": 1, "tabId": tab.ID, "nonce": nonce, "keyName": name, "key": value})
}
