package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"scriptboard/internal/customdashboard"
)

// flow 卡片运行端点：一期仅登录监控页（PermissionExecute）可触发；
// 状态经 SSE 推送，快照只存内存，页面刷新后重建最近一次运行。

func (a *App) runCustomDashboardCardFlow(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		dashboardActionError(response, http.StatusForbidden, "页面已过期，请重试")
		return
	}
	card, err := a.customDashboards.GetCard(request.Context(), request.PathValue("id"))
	if err != nil || card.Type != customdashboard.CardFlow {
		dashboardActionError(response, http.StatusNotFound, "流程卡片不存在")
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	view, startErr := a.dashboardFlowRunner.Start(card, customdashboard.FlowActor{UserID: current.userID, Username: current.username, Role: string(current.role)})
	if errors.Is(startErr, customdashboard.ErrFlowRunConflict) {
		dashboardActionError(response, http.StatusConflict, startErr.Error())
		return
	}
	if startErr != nil {
		dashboardActionError(response, http.StatusBadRequest, startErr.Error())
		return
	}
	a.recordAuditForRequest(request, "run_dashboard_flow", card.DashboardID+"/"+card.ID, "accepted")
	writeDashboardActionJSON(response, http.StatusOK, map[string]string{"flowRunId": view.ID})
}

// customDashboardCardFlowHistory 返回卡片最近的流程运行历史（新条目在前），供历史晴雨表展示。
func (a *App) customDashboardCardFlowHistory(response http.ResponseWriter, request *http.Request) {
	card, err := a.customDashboards.GetCard(request.Context(), request.PathValue("id"))
	if err != nil || card.Type != customdashboard.CardFlow {
		http.NotFound(response, request)
		return
	}
	writeDashboardActionJSON(response, http.StatusOK, map[string]any{"entries": a.dashboardFlowRunner.History(card.ID)})
}

func (a *App) customDashboardCardFlowEvents(response http.ResponseWriter, request *http.Request) {
	card, err := a.customDashboards.GetCard(request.Context(), request.PathValue("id"))
	if err != nil || card.Type != customdashboard.CardFlow {
		http.NotFound(response, request)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "当前连接不支持 SSE", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	emit := func(payload any) bool {
		encoded, _ := json.Marshal(payload)
		if _, err := fmt.Fprintf(response, "event: flow\ndata: %s\n\n", encoded); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	var lastVersion int64 = -1
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, present := a.dashboardFlowRunner.Snapshot(card.ID)
		if !present {
			// 尚无运行记录：回放一次 idle 快照供页面初始化，随后结束流。
			emit(map[string]any{"cardId": card.ID, "status": "idle", "finished": true})
			return
		}
		if view.Version != lastVersion {
			if !emit(view) {
				return
			}
			lastVersion = view.Version
		}
		if view.Finished {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
