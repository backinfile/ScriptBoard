package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/ai"
)

type aiWorkspaceView struct {
	CSRFToken      string
	Search         string
	ContextType    string
	ContextID      string
	ContextSummary string
	InitialMessage string
	Profiles       []ai.ModelProfile
	Conversations  []ai.Conversation
	Settings       ai.Settings
}

type aiConversationView struct {
	CSRFToken    string
	Conversation ai.Conversation
	Profile      ai.ModelProfile
	Profiles     []ai.ModelProfile
	Messages     []ai.StoredMessage
	Batches      []ai.Batch
	Attachments  []ai.Attachment
	InputLocked  bool
	CanRetry     bool
}

type aiSettingsView struct {
	CSRFToken string
	Settings  ai.Settings
	Profiles  []ai.ModelProfile
}

type aiBatchView struct {
	CSRFToken  string
	Batch      ai.Batch
	Actions    []ai.BatchAction
	EventAfter int64
}

func (a *App) aiWorkspace(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	search := strings.TrimSpace(request.URL.Query().Get("q"))
	contextType := strings.TrimSpace(request.URL.Query().Get("context_type"))
	contextID := strings.TrimSpace(request.URL.Query().Get("context_id"))
	contextSummary := strings.TrimSpace(request.URL.Query().Get("context_summary"))
	initialMessage := ""
	if contextType != "" || contextID != "" {
		initialMessage = "请先查询这个 " + contextType + " 的最新状态并分析：" + contextID
	}
	profiles, err := a.aiStore.ListProfiles(request.Context(), false)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	conversations, err := a.aiStore.ListConversations(request.Context(), search, 100, 0)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	settings, err := a.aiStore.GetSettings(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = aiWorkspaceTemplate.Execute(response, aiWorkspaceView{
		CSRFToken: current.csrfToken, Search: search, ContextType: contextType, ContextID: contextID,
		ContextSummary: contextSummary, InitialMessage: initialMessage,
		Profiles: profiles, Conversations: conversations, Settings: settings,
	})
}

func (a *App) createAIConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	profileID := strings.TrimSpace(request.FormValue("profile_id"))
	if profileID == "" {
		settings, err := a.aiStore.GetSettings(request.Context())
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
		profileID = settings.DefaultProfileID
	}
	if profileID == "" {
		http.Error(response, "请先配置 AI 模型", http.StatusBadRequest)
		return
	}
	permission := aiPermissionPreset(request.FormValue("permission"))
	conversation, err := a.aiStore.CreateConversation(request.Context(), profileID, permission, request.FormValue("message"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	contextType := strings.TrimSpace(request.FormValue("context_type"))
	contextID := strings.TrimSpace(request.FormValue("context_id"))
	contextSummary := strings.TrimSpace(request.FormValue("context_summary"))
	if contextType != "" || contextID != "" {
		if err := a.aiStore.UpdateConversation(request.Context(), conversation.ID, conversation.Title, contextType, contextID, contextSummary); err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	a.recordAudit("ai_conversation_create", conversation.ID, "succeeded", request.RemoteAddr)
	message := strings.TrimSpace(request.FormValue("message"))
	if message != "" {
		a.runAITurn(conversation.ID, message)
	}
	http.Redirect(response, request, "/ai/conversations/"+conversation.ID, http.StatusSeeOther)
}

func aiPermissionPreset(value string) ai.Permission {
	switch value {
	case "readonly":
		return ai.Permission{Query: true}
	case "execute":
		return ai.Permission{Query: true, Execute: true}
	case "editor":
		return ai.Permission{Query: true, Modify: true}
	default:
		return ai.Permission{Query: true, Execute: true, Modify: true}
	}
}

func (a *App) aiConversationPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	conversation, err := a.aiStore.GetConversation(request.Context(), request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(response, request)
			return
		}
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	profile, _ := a.aiStore.GetProfile(request.Context(), conversation.ProfileID)
	profiles, _ := a.aiStore.ListProfiles(request.Context(), false)
	messages, err := a.aiStore.ListMessages(request.Context(), conversation.ID)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	batches, err := a.aiStore.ListBatches(request.Context(), conversation.ID, 20)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	attachments, err := a.aiStore.ListAttachments(request.Context(), conversation.ID)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	inputLocked := false
	for _, batch := range batches {
		if batch.Status == ai.BatchPending || batch.Status == ai.BatchRunning {
			inputLocked = true
			break
		}
	}
	canRetry := false
	if latestTurn, turnErr := a.aiStore.LatestTurn(request.Context(), conversation.ID); turnErr == nil &&
		(latestTurn.Status == ai.TurnFailed || latestTurn.Status == ai.TurnInterrupted) {
		hasBatch, _ := a.aiStore.TurnHasBatch(request.Context(), latestTurn.ID)
		canRetry = !hasBatch && !inputLocked
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = aiConversationTemplate.Execute(response, aiConversationView{
		CSRFToken: current.csrfToken, Conversation: conversation, Profile: profile, Profiles: profiles,
		Messages: messages, Batches: batches, Attachments: attachments, InputLocked: inputLocked, CanRetry: canRetry,
	})
}

func (a *App) retryAIMessage(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	conversationID := request.PathValue("id")
	turn, err := a.aiStore.LatestTurn(request.Context(), conversationID)
	if err != nil || (turn.Status != ai.TurnFailed && turn.Status != ai.TurnInterrupted) {
		http.Error(response, "最近回合不可重试", http.StatusConflict)
		return
	}
	hasBatch, err := a.aiStore.TurnHasBatch(request.Context(), turn.ID)
	if err != nil || hasBatch {
		http.Error(response, "已提交过副作用批次的回合不可一键重试", http.StatusConflict)
		return
	}
	messages, err := a.aiStore.ListMessages(request.Context(), conversationID)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	text := ""
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Message.Role == "user" {
			text = messages[index].Message.Text
			break
		}
	}
	if text == "" {
		http.Error(response, "找不到可重试的用户消息", http.StatusConflict)
		return
	}
	a.runAITurn(conversationID, text)
	a.recordAudit("ai_turn_retry", conversationID, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/ai/conversations/"+conversationID, http.StatusSeeOther)
}

func (a *App) switchAIConversationProfile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	if err := a.aiStore.SwitchConversationProfile(request.Context(), request.PathValue("id"), request.FormValue("profile_id")); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("ai_conversation_model_switch", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/ai/conversations/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) uploadAIAttachment(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, (100<<20)+(1<<20))
	if err := request.ParseMultipartForm(100 << 20); err != nil {
		http.Error(response, "附件超过 100 MiB 或表单无效", http.StatusBadRequest)
		return
	}
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	file, header, err := request.FormFile("attachment")
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	if _, err := a.aiStore.CreateAttachment(request.Context(), request.PathValue("id"), header.Filename, header.Header.Get("Content-Type"), file); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("ai_attachment_upload", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/ai/conversations/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) sendAIMessage(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	conversationID := request.PathValue("id")
	if _, err := a.aiStore.GetConversation(request.Context(), conversationID); err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	message := strings.TrimSpace(request.FormValue("message"))
	if message == "" {
		http.Error(response, "消息不能为空", http.StatusBadRequest)
		return
	}
	a.runAITurn(conversationID, message)
	a.recordAudit("ai_turn_start", conversationID, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/ai/conversations/"+conversationID, http.StatusSeeOther)
}

func (a *App) runAITurn(conversationID, message string) {
	a.aiTurnWG.Add(1)
	go func() {
		defer a.aiTurnWG.Done()
		ctx := a.aiContext
		emit := func(event ai.ModelEvent) {
			payload, err := json.Marshal(event)
			if err == nil {
				_, _ = a.aiStore.AddEvent(ctx, conversationID, "", "", "model_event", payload)
			}
		}
		result, err := a.aiCoordinator.Send(ctx, conversationID, message, emit)
		payload, _ := json.Marshal(map[string]any{
			"turn_id": result.TurnID, "batch_id": result.BatchID, "error": errorString(err),
		})
		_, _ = a.aiStore.AddEvent(ctx, conversationID, result.TurnID, result.BatchID, "turn_finished", payload)
	}()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *App) stopAIConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	a.aiCoordinator.StopConversation(id)
	a.recordAudit("ai_turn_stop", id, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/ai/conversations/"+id, http.StatusSeeOther)
}

func (a *App) renameAIConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	conversation, err := a.aiStore.GetConversation(request.Context(), request.PathValue("id"))
	if err == nil {
		err = a.aiStore.UpdateConversation(request.Context(), conversation.ID, request.FormValue("title"),
			conversation.ContextType, conversation.ContextID, conversation.ContextSummary)
	}
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+conversation.ID, http.StatusSeeOther)
}

func (a *App) deleteAIConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	a.aiCoordinator.StopConversation(id)
	if err := a.aiStore.DeleteConversation(request.Context(), id); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("ai_conversation_delete", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/ai", http.StatusSeeOther)
}

func (a *App) aiEvents(response http.ResponseWriter, request *http.Request) {
	if _, err := a.aiStore.GetConversation(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	after, _ := strconv.ParseInt(request.Header.Get("Last-Event-ID"), 10, 64)
	if value, err := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64); err == nil && value > after {
		after = value
	}
	if after == 0 && request.Header.Get("Last-Event-ID") == "" && request.URL.Query().Get("after") == "" {
		after, _ = a.aiStore.LatestTerminalEventID(request.Context(), request.PathValue("id"))
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		events, err := a.aiStore.ListEvents(request.Context(), request.PathValue("id"), after, 100)
		if err != nil {
			return
		}
		for _, event := range events {
			after = event.ID
			fmt.Fprintf(response, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Payload)
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		select {
		case <-request.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(response, ": keepalive\n\n")
			flusher.Flush()
		case <-ticker.C:
		}
	}
}

func (a *App) aiBatchPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	batch, err := a.aiStore.GetBatch(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	actions, err := a.aiStore.ListBatchActions(request.Context(), batch.ID)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	eventAfter, _ := a.aiStore.LatestEventID(request.Context(), batch.ConversationID)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = aiBatchTemplate.Execute(response, aiBatchView{CSRFToken: current.csrfToken, Batch: batch, Actions: actions, EventAfter: eventAfter})
}

func (a *App) approveAIBatch(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	batch, err := a.aiStore.GetBatch(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	a.aiTurnWG.Add(1)
	go func() {
		defer a.aiTurnWG.Done()
		err := a.aiCoordinator.ApproveBatch(a.aiContext, batch.ID)
		payload, _ := json.Marshal(map[string]string{"batch_id": batch.ID, "error": errorString(err)})
		_, _ = a.aiStore.AddEvent(a.aiContext, batch.ConversationID, batch.TurnID, batch.ID, "batch_finished", payload)
		if err == nil {
			_ = a.aiCoordinator.ContinueBatchSummary(a.aiContext, batch.ID)
		}
	}()
	a.recordAudit("ai_batch_approve", batch.ID, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/ai/batches/"+batch.ID, http.StatusSeeOther)
}

func (a *App) rejectAIBatch(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	batch, err := a.aiStore.GetBatch(request.Context(), request.PathValue("id"))
	if err == nil {
		err = a.aiCoordinator.RejectBatch(request.Context(), batch.ID)
	}
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	payload, _ := json.Marshal(map[string]string{"batch_id": batch.ID, "status": "rejected"})
	_, _ = a.aiStore.AddEvent(request.Context(), batch.ConversationID, batch.TurnID, batch.ID, "batch_finished", payload)
	a.aiTurnWG.Add(1)
	go func() {
		defer a.aiTurnWG.Done()
		_ = a.aiCoordinator.ContinueBatchSummary(a.aiContext, batch.ID)
	}()
	a.recordAudit("ai_batch_reject", batch.ID, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/ai/conversations/"+batch.ConversationID, http.StatusSeeOther)
}

func (a *App) aiSettingsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	settings, err := a.aiStore.GetSettings(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	profiles, err := a.aiStore.ListProfiles(request.Context(), true)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = aiSettingsTemplate.Execute(response, aiSettingsView{CSRFToken: current.csrfToken, Settings: settings, Profiles: profiles})
}

func (a *App) saveAISettings(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	concurrency, err := strconv.Atoi(request.FormValue("max_concurrent_turns"))
	if err != nil {
		http.Error(response, "无效并发数", http.StatusBadRequest)
		return
	}
	settings := ai.Settings{
		DefaultProfileID:   request.FormValue("default_profile_id"),
		MaxConcurrentTurns: concurrency,
		KillSwitch:         request.FormValue("kill_switch") == "1",
	}
	if err := a.aiStore.SaveSettings(request.Context(), settings); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if settings.KillSwitch {
		a.aiCoordinator.StopAll()
	}
	a.recordAudit("ai_settings_update", "global", "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) createAIProfile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	if request.FormValue("risk_confirmed") != "1" {
		http.Error(response, "必须确认 AI 完全控制与自动审批风险", http.StatusBadRequest)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	contextWindow, _ := strconv.Atoi(request.FormValue("context_window"))
	maxOutput, _ := strconv.Atoi(request.FormValue("max_output_tokens"))
	runTimeout, _ := strconv.Atoi(request.FormValue("default_run_timeout_seconds"))
	profile := ai.ModelProfile{
		ID: id, Name: strings.TrimSpace(request.FormValue("name")),
		Protocol: ai.Protocol(request.FormValue("protocol")), BaseURL: strings.TrimSpace(request.FormValue("base_url")),
		Model: strings.TrimSpace(request.FormValue("model")), AuthMode: ai.AuthMode(request.FormValue("auth_mode")),
		ContextWindow: contextWindow, MaxOutputTokens: maxOutput,
		Permission:          ai.Permission{Query: true, Execute: request.FormValue("execute") == "1", Modify: request.FormValue("modify") == "1"},
		AllowSensitiveReads: request.FormValue("allow_sensitive_reads") == "1",
		AutoApprove:         request.FormValue("auto_approve") == "1", DefaultRunTimeoutSec: runTimeout,
	}
	if profile.AuthMode != ai.AuthNone {
		profile.APIKeyRef, err = a.aiVault.Write(request.Context(), request.FormValue("api_key"))
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	profile.ExtraHeaderRefs = make(map[string]string)
	for _, line := range strings.Split(request.FormValue("extra_headers"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			err = errors.New("附加头必须使用 Name: Value 格式")
			break
		}
		name = strings.TrimSpace(name)
		if validateErr := ai.ValidateExtraHeaders(map[string]string{name: strings.TrimSpace(value)}); validateErr != nil {
			err = validateErr
			break
		}
		profile.ExtraHeaderRefs[name], err = a.aiVault.Write(request.Context(), strings.TrimSpace(value))
		if err != nil {
			break
		}
	}
	if err == nil {
		err = a.aiStore.SaveProfile(request.Context(), profile)
	}
	if err != nil {
		if profile.APIKeyRef != "" {
			_ = a.aiVault.Delete(profile.APIKeyRef)
		}
		for _, reference := range profile.ExtraHeaderRefs {
			_ = a.aiVault.Delete(reference)
		}
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	settings, _ := a.aiStore.GetSettings(request.Context())
	if settings.DefaultProfileID == "" {
		settings.DefaultProfileID = profile.ID
		_ = a.aiStore.SaveSettings(request.Context(), settings)
	}
	a.recordAudit("ai_profile_create", profile.ID, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) updateAIProfile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	if request.FormValue("risk_confirmed") != "1" {
		http.Error(response, "必须确认模型权限变更风险", http.StatusBadRequest)
		return
	}
	profile, err := a.aiStore.GetProfile(request.Context(), request.PathValue("id"))
	if err != nil || profile.Disabled {
		http.Error(response, "模型配置不存在或已删除", http.StatusNotFound)
		return
	}
	oldKey := profile.APIKeyRef
	profile.Name = strings.TrimSpace(request.FormValue("name"))
	profile.Protocol = ai.Protocol(request.FormValue("protocol"))
	profile.BaseURL = strings.TrimSpace(request.FormValue("base_url"))
	profile.Model = strings.TrimSpace(request.FormValue("model"))
	profile.AuthMode = ai.AuthMode(request.FormValue("auth_mode"))
	profile.ContextWindow, _ = strconv.Atoi(request.FormValue("context_window"))
	profile.MaxOutputTokens, _ = strconv.Atoi(request.FormValue("max_output_tokens"))
	profile.DefaultRunTimeoutSec, _ = strconv.Atoi(request.FormValue("default_run_timeout_seconds"))
	profile.Permission = ai.Permission{Query: true, Execute: request.FormValue("execute") == "1", Modify: request.FormValue("modify") == "1"}
	profile.AllowSensitiveReads = request.FormValue("allow_sensitive_reads") == "1"
	profile.AutoApprove = request.FormValue("auto_approve") == "1"
	newKey := ""
	if value := request.FormValue("api_key"); value != "" {
		newKey, err = a.aiVault.Write(request.Context(), value)
		if err == nil {
			profile.APIKeyRef = newKey
		}
	} else if profile.AuthMode == ai.AuthNone {
		profile.APIKeyRef = ""
	}
	if err == nil {
		err = a.aiStore.SaveProfile(request.Context(), profile)
	}
	if err != nil {
		if newKey != "" {
			_ = a.aiVault.Delete(newKey)
		}
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if oldKey != "" && oldKey != profile.APIKeyRef {
		_ = a.aiVault.Delete(oldKey)
	}
	a.recordAudit("ai_profile_update", profile.ID, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) deleteAIProfile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	profile, err := a.aiStore.GetProfile(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	if err := a.aiStore.DisableProfile(request.Context(), profile.ID); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if profile.APIKeyRef != "" {
		_ = a.aiVault.Delete(profile.APIKeyRef)
	}
	for _, reference := range profile.ExtraHeaderRefs {
		_ = a.aiVault.Delete(reference)
	}
	settings, _ := a.aiStore.GetSettings(request.Context())
	if settings.DefaultProfileID == profile.ID {
		settings.DefaultProfileID = ""
		_ = a.aiStore.SaveSettings(request.Context(), settings)
	}
	a.recordAudit("ai_profile_delete", profile.ID, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) testAIProfile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	profile, err := a.aiStore.GetProfile(request.Context(), request.PathValue("id"))
	if err == nil {
		var client ai.ModelClient
		client, err = ai.NewModelClient(profile, a.aiVault.Read)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			first, callErr := client.Complete(ctx, ai.ModelRequest{
				System:   "This is a transport test. Call scriptboard_probe exactly once.",
				Messages: []ai.ModelMessage{{Role: "user", Text: "Run the harmless transport probe."}},
				Tools: []ai.ToolDefinition{{
					Name: "scriptboard_probe", Description: "A harmless connectivity probe.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				}},
				ToolChoice: "required", MaxTokens: 128,
			}, func(ai.ModelEvent) {})
			err = callErr
			if err == nil && len(first.Message.ToolCalls) == 0 {
				err = errors.New("模型未返回强制 Tool Call")
			}
			if err == nil {
				call := first.Message.ToolCalls[0]
				_, err = client.Complete(ctx, ai.ModelRequest{
					System: "This is a transport test. Acknowledge the successful probe briefly.",
					Messages: []ai.ModelMessage{
						{Role: "user", Text: "Run the harmless transport probe."},
						first.Message,
						{Role: "tool", ToolResult: &ai.ToolResult{CallID: call.ID, Content: `{"ok":true}`}},
					},
					MaxTokens: 64,
				}, func(ai.ModelEvent) {})
			}
		}
	}
	_ = a.aiStore.RecordProfileDiagnostic(context.Background(), request.PathValue("id"), err)
	if err != nil {
		a.recordAudit("ai_profile_test", request.PathValue("id"), "failed", request.RemoteAddr)
		http.Error(response, err.Error(), http.StatusBadGateway)
		return
	}
	a.recordAudit("ai_profile_test", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}
