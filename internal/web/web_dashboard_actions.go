package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"scriptboard/internal/customdashboard"
	"scriptboard/internal/identity"
	"scriptboard/internal/runcontrol"
)

// 面板操作触发：登录端走会话 CSRF + PermissionExecute；公开端走访问 Key，
// 任一校验失败统一 404 防枚举。browser_http 动作不经过服务端。

func writeDashboardActionJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func dashboardActionError(response http.ResponseWriter, status int, message string) {
	writeDashboardActionJSON(response, status, map[string]string{"error": message})
}

func (a *App) startDashboardActionRun(request *http.Request, quickRunID string, actor runcontrol.Actor, confirmOverlap bool) (runcontrol.StartResult, error) {
	return a.runControl.Start(request.Context(), runcontrol.StartRequest{QuickRunID: quickRunID, ConfirmOverlap: confirmOverlap, Actor: actor})
}

// triggerCustomDashboardCardAction 是登录态触发端点（监控页按钮）。
func (a *App) triggerCustomDashboardCardAction(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		dashboardActionError(response, http.StatusForbidden, "页面已过期，请重试")
		return
	}
	card, err := a.customDashboards.GetCard(request.Context(), request.PathValue("id"))
	if err != nil {
		dashboardActionError(response, http.StatusNotFound, "卡片不存在")
		return
	}
	action, ok := customdashboard.FindCardAction(card, request.PathValue("actionId"))
	// browser_http 由访客浏览器直连，服务端不代理。
	if !ok || action.Kind != string(customdashboard.ActionQuickRun) {
		dashboardActionError(response, http.StatusNotFound, "操作不存在")
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	started, startErr := a.startDashboardActionRun(request, action.QuickRunID, runcontrol.Actor{UserID: current.userID, Username: current.username, Role: current.role}, request.FormValue("confirm_overlap") == "yes")
	target := card.DashboardID + "/" + card.ID + "/" + action.ID
	if errors.Is(startErr, runcontrol.ErrNotFound) {
		a.recordAuditForRequest(request, "trigger_dashboard_action", target, "failed")
		dashboardActionError(response, http.StatusNotFound, "快捷执行不存在")
		return
	}
	if errors.Is(startErr, runcontrol.ErrPublicationChanged) {
		a.recordAuditForRequest(request, "trigger_dashboard_action", target, "failed")
		dashboardActionError(response, http.StatusConflict, "快捷执行脚本已变化，请由管理员重新发布")
		return
	}
	if startErr != nil {
		a.recordAuditForRequest(request, "trigger_dashboard_action", target, "failed")
		dashboardActionError(response, http.StatusBadRequest, "无法启动操作："+startErr.Error())
		return
	}
	if started.Conflict != "" {
		writeDashboardActionJSON(response, http.StatusConflict, started)
		return
	}
	a.recordAuditForRequest(request, "trigger_dashboard_action", target, "accepted")
	writeDashboardActionJSON(response, http.StatusOK, map[string]string{"runId": started.RunID, "runUrl": "/history/runs/" + started.RunID})
}

// triggerPublicDashboardCardAction 是 public_operate 面板的 Key 门控触发端点。
func (a *App) triggerPublicDashboardCardAction(response http.ResponseWriter, request *http.Request) {
	release, allowed := a.externalAuthLimit.AcquireSource(externalLimitSource(request.RemoteAddr))
	if !allowed {
		dashboardActionError(response, http.StatusTooManyRequests, "请求过于频繁")
		return
	}
	defer release()
	slug := request.PathValue("slug")
	dashboard, err := a.customDashboards.GetPublicDashboard(request.Context(), slug)
	if err != nil || dashboard.Visibility != customdashboard.VisibilityPublicOperate {
		http.NotFound(response, request)
		return
	}
	key := strings.TrimSpace(request.FormValue("key"))
	verified, verifyErr := a.customDashboards.VerifyAccessKey(request.Context(), dashboard.ID, key)
	if errors.Is(verifyErr, customdashboard.ErrAccessKeyLocked) {
		// 锁定期间同样返回 404，但记录审计以便追踪暴力尝试来源。
		a.recordAuditWithRequestActor(request, "trigger_dashboard_action", dashboard.ID, "blocked", request.RemoteAddr, "", "dashboard-key:"+dashboard.AccessKeyHint, identity.Role("external"))
		http.NotFound(response, request)
		return
	}
	if verifyErr != nil || !verified {
		http.NotFound(response, request)
		return
	}
	_, action, resolveErr := a.customDashboards.ResolvePublicAction(request.Context(), slug, request.PathValue("id"), request.PathValue("actionId"))
	if resolveErr != nil {
		http.NotFound(response, request)
		return
	}
	actor := runcontrol.Actor{Username: "dashboard-key:" + dashboard.AccessKeyHint, Role: identity.Role("external")}
	started, startErr := a.startDashboardActionRun(request, action.QuickRunID, actor, request.FormValue("confirm_overlap") == "yes")
	target := dashboard.ID + "/" + request.PathValue("id") + "/" + action.ID
	if startErr != nil {
		a.recordAuditWithRequestActor(request, "trigger_dashboard_action", target, "failed", request.RemoteAddr, "", actor.Username, actor.Role)
		status := http.StatusBadRequest
		if errors.Is(startErr, runcontrol.ErrNotFound) || errors.Is(startErr, runcontrol.ErrPublicationChanged) {
			status = http.StatusConflict
		}
		dashboardActionError(response, status, "无法启动操作")
		return
	}
	if started.Conflict != "" {
		writeDashboardActionJSON(response, http.StatusConflict, started)
		return
	}
	a.recordAuditWithRequestActor(request, "trigger_dashboard_action", target, "accepted", request.RemoteAddr, "", actor.Username, actor.Role)
	writeDashboardActionJSON(response, http.StatusOK, map[string]string{"runId": started.RunID, "status": started.Status, "statusUrl": strings.TrimSuffix(request.URL.Path, "/trigger") + "/status"})
}

// updateCustomDashboardVisibility 切换四档可见性；升为公开可操作且首次生成
// Key 时直接渲染一次性横幅（不经重定向，避免 Key 进入 URL）。
func (a *App) updateCustomDashboardVisibility(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	dashboard, key, err := a.customDashboards.SetVisibility(request.Context(), id, customdashboard.Visibility(request.FormValue("visibility")))
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		http.Error(response, err.Error(), status)
		return
	}
	a.recordAuditForRequest(request, "update_dashboard_visibility", id, "succeeded")
	if key != "" {
		a.renderCustomDashboardPage(response, request, dashboard.ID, key)
		return
	}
	http.Redirect(response, request, "/config/dashboards?dashboard="+id, http.StatusSeeOther)
}

func (a *App) rotateCustomDashboardAccessKey(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	key, _, err := a.customDashboards.RotateAccessKey(request.Context(), id)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "rotate_dashboard_access_key", id, "succeeded")
	a.renderCustomDashboardPage(response, request, id, key)
}

func (a *App) revokeCustomDashboardAccessKey(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "请确认撤销访问 Key", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.customDashboards.RevokeAccessKey(request.Context(), id); err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "revoke_dashboard_access_key", id, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+id, http.StatusSeeOther)
}

// Status exposes only the outcome of runs triggered with this dashboard's current key.
func (a *App) publicDashboardActionStatus(response http.ResponseWriter, request *http.Request) {
	release, allowed := a.externalAuthLimit.AcquireSource(externalLimitSource(request.RemoteAddr))
	if !allowed {
		http.Error(response, "Too many requests", 429)
		return
	}
	defer release()
	dashboard, err := a.customDashboards.GetPublicDashboard(request.Context(), request.PathValue("slug"))
	if err != nil || dashboard.Visibility != customdashboard.VisibilityPublicOperate {
		http.NotFound(response, request)
		return
	}
	verified, err := a.customDashboards.VerifyAccessKey(request.Context(), dashboard.ID, strings.TrimSpace(request.FormValue("key")))
	if err != nil || !verified {
		http.NotFound(response, request)
		return
	}
	_, action, err := a.customDashboards.ResolvePublicAction(request.Context(), request.PathValue("slug"), request.PathValue("id"), request.PathValue("actionId"))
	if err != nil {
		http.NotFound(response, request)
		return
	}
	run, err := a.runs.GetMetadata(request.FormValue("run_id"))
	if err != nil || run.SourceID != action.QuickRunID || run.InitiatorUsername != "dashboard-key:"+dashboard.AccessKeyHint {
		http.NotFound(response, request)
		return
	}
	writeDashboardActionJSON(response, http.StatusOK, map[string]string{"status": run.Status})
}
