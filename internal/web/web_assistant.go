package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/assistant"
	"scriptboard/internal/assistant/runtimeinstall"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/websitemonitor"
)

type assistantModelView struct {
	assistant.ModelConfig
	ProviderLabel, EndpointDisplay string
}

type assistantResourceView struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Label     string `json:"label"`
	Detail    string `json:"detail"`
	Icon      string `json:"icon"`
	Category  string `json:"category"`
	Selected  bool   `json:"selected"`
	ImageHint bool   `json:"imageHint"`
}

type assistantMessageView struct {
	assistant.Message
	ToolCalls      []assistant.ToolCall
	LatestToolCall *assistant.ToolCall
	Parts          []assistantMessagePartView
}

type assistantMessagePartView struct {
	Locale          webLocale
	Kind            string
	Body            string
	BodyOffset      int
	ToolCalls       []assistant.ToolCall
	LatestToolCall  *assistant.ToolCall
	Title           string
	AggregateStatus string
	ResultSummary   string
}

type assistantUsageView struct {
	Available, ContextAvailable                             bool
	ContextPercent                                          float64
	ContextTokens, ContextWindow, InputTokens, OutputTokens string
}

type assistantInspectorReferenceView struct {
	Kind, StableID, Label, Icon, Meta string
}

type assistantOperationView struct {
	ID, Name, Label, Status, State, Icon, TimeLabel string
	StartedAt                                       time.Time
}

type assistantPageData struct {
	Locale              webLocale
	CSRFToken           string
	Models              []assistantModelView
	Conversations       []assistant.Conversation
	Archived            []assistant.Conversation
	Current             *assistant.Conversation
	SelectedModelID     string
	SelectedProfile     string
	DefaultAutoApproval bool
	RuntimeAvailable    bool
	RuntimeVersion      string
	CanManageAI         bool
	Resources           []assistantResourceView
	ContextReferences   []assistant.ContextRef
	InspectorReferences []assistantInspectorReferenceView
	Usage               assistantUsageView
	Messages            []assistant.Message
	Timeline            []assistantMessageView
	ToolCalls           []assistant.ToolCall
	Operations          []assistantOperationView
	PendingApproval     *assistant.Approval
	PendingApprovalCall *assistant.ToolCall
	AssistantEnabled    bool
}

type assistantSettingsPageData struct {
	Locale                  webLocale
	CSRFToken               string
	SettingsNavigation      settingsNavigationData
	Settings                assistant.Settings
	Models                  []assistantModelView
	DefaultModel            *assistantModelView
	RuntimeAvailable        bool
	RuntimeVersion          string
	Runtime                 runtimeinstall.Snapshot
	ActiveProcesses         int
	ProviderTestPassed      bool
	RuntimeOfflineInstalled bool
}

func assistantActor(request *http.Request) assistant.Actor {
	current := request.Context().Value(sessionContextKey).(session)
	return assistant.Actor{UserID: current.userID, Username: current.username}
}

func (a *App) assistantPage(response http.ResponseWriter, request *http.Request) {
	a.renderAssistantPage(response, request, "")
}

func (a *App) assistantConversationPage(response http.ResponseWriter, request *http.Request) {
	a.renderAssistantPage(response, request, strings.TrimSpace(request.PathValue("id")))
}

func (a *App) renderAssistantPage(response http.ResponseWriter, request *http.Request, conversationID string) {
	currentSession := request.Context().Value(sessionContextKey).(session)
	actor := assistantActor(request)
	models, err := a.assistant.ListModels(request.Context(), actor)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	query := request.URL.Query().Get("q")
	conversations, err := a.assistant.ListConversations(request.Context(), actor, assistant.ConversationFilter{Query: query})
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	archived, err := a.assistant.ListConversations(request.Context(), actor, assistant.ConversationFilter{Archived: true, Query: query})
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	var current *assistant.Conversation
	var contextReferences []assistant.ContextRef
	var messages []assistant.Message
	var toolCalls []assistant.ToolCall
	var pendingApproval *assistant.Approval
	var pendingApprovalCall *assistant.ToolCall
	selectedModelID := ""
	selectedProfile := assistant.ProfileGeneral
	if conversationID != "" {
		conversation, err := a.assistant.Conversation(request.Context(), actor, conversationID)
		if errors.Is(err, assistant.ErrNotFound) {
			http.NotFound(response, request)
			return
		}
		if err != nil {
			http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
			return
		}
		current = &conversation
		selectedModelID = conversation.ModelID
		selectedProfile = conversation.CapabilityProfile
		contextReferences, err = a.assistant.ContextReferences(request.Context(), actor, conversationID)
		if err != nil {
			http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
			return
		}
		messages, err = a.assistant.Messages(request.Context(), actor, conversationID)
		if err != nil {
			http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
			return
		}
		toolCalls, err = a.assistant.ToolCalls(request.Context(), actor, conversationID)
		if err != nil {
			http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
			return
		}
		if approval, approvalErr := a.assistant.PendingApproval(request.Context(), actor, conversationID); approvalErr == nil && approval.Status == "pending" {
			pendingApproval = &approval
			if call, callErr := a.assistant.ToolCallByID(request.Context(), actor, conversationID, approval.ToolCallID); callErr == nil {
				pendingApprovalCall = &call
			}
		} else if !errors.Is(approvalErr, assistant.ErrNotFound) {
			http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
			return
		}
	}
	settings, err := a.assistant.Settings(request.Context())
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	if selectedModelID == "" {
		for _, model := range models {
			if model.Default && model.CredentialConfigured {
				selectedModelID = model.ID
				break
			}
		}
	}
	if selectedModelID == "" {
		for _, model := range models {
			if model.CredentialConfigured {
				selectedModelID = model.ID
				break
			}
		}
	}
	if conversationID == "" {
		switch requested := strings.TrimSpace(request.URL.Query().Get("profile")); requested {
		case assistant.ProfileDiagnoseFailedRun, assistant.ProfileInvestigateWebsiteIncident, assistant.ProfileTriageHostPressure, assistant.ProfileReviewScriptSafety, assistant.ProfileDesignSchedule:
			selectedProfile = requested
		}
	}
	locale := resolveWebLocale(request)
	resources := markAssistantResourcesSelected(a.assistantResourceCatalog(request, currentSession.role), contextReferences)
	usage := assistantUsageView{}
	var inspectorReferences []assistantInspectorReferenceView
	if current != nil {
		usage = assistantConversationUsage(*current)
		inspectorReferences = assistantInspectorReferences(resources, contextReferences, locale)
	}
	managedRuntime, runtimeErr := a.assistantRuntime.Runtime()
	runtimeAvailable := runtimeErr == nil
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = assistantTemplate.Execute(response, assistantPageData{
		Locale: locale, CSRFToken: currentSession.csrfToken, Models: assistantModelViews(models),
		Conversations: conversations, Archived: archived, Current: current, SelectedModelID: selectedModelID, SelectedProfile: selectedProfile,
		DefaultAutoApproval: settings.DefaultAutoApproval, RuntimeAvailable: runtimeAvailable, RuntimeVersion: managedRuntime.Version,
		CanManageAI: roleAllows(currentSession.role, permissionManageSystem),
		Resources:   resources, ContextReferences: contextReferences,
		InspectorReferences: inspectorReferences, Usage: usage,
		Messages: messages, Timeline: assistantMessageTimeline(messages, toolCalls, locale), ToolCalls: toolCalls,
		Operations:      assistantOperationViews(toolCalls, locale),
		PendingApproval: pendingApproval, PendingApprovalCall: pendingApprovalCall, AssistantEnabled: settings.Enabled,
	})
}

func assistantConversationUsage(conversation assistant.Conversation) assistantUsageView {
	telemetry := conversation.Telemetry
	view := assistantUsageView{
		Available:     !telemetry.UpdatedAt.IsZero(),
		ContextTokens: assistantTokenCount(telemetry.ContextTokens),
		ContextWindow: assistantTokenCount(telemetry.ContextWindow),
		InputTokens:   assistantTokenCount(telemetry.InputTokens),
		OutputTokens:  assistantTokenCount(telemetry.OutputTokens),
	}
	if telemetry.ContextPercent != nil && telemetry.ContextWindow > 0 {
		view.ContextAvailable = true
		view.ContextPercent = math.Max(0, math.Min(100, *telemetry.ContextPercent))
	}
	return view
}

func assistantTokenCount(value int64) string {
	digits := strconv.FormatInt(value, 10)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	return digits
}

func assistantInspectorReferences(resources []assistantResourceView, references []assistant.ContextRef, locale webLocale) []assistantInspectorReferenceView {
	views := make([]assistantInspectorReferenceView, 0, len(references)+1)
	views = append(views, assistantInspectorReferenceView{
		Kind: "host_overview", StableID: "current", Icon: "activity",
		Label: webText(locale, "assistant.host_overview"), Meta: webText(locale, "assistant.automatic_reference"),
	})
	byKey := make(map[string]assistantResourceView, len(resources))
	for _, resource := range resources {
		byKey[resource.Kind+"\x00"+resource.ID] = resource
	}
	for _, reference := range references {
		resource, exists := byKey[reference.Kind+"\x00"+reference.StableID]
		icon, meta := assistantReferencePresentation(reference.Kind, locale)
		if exists {
			icon = resource.Icon
		}
		views = append(views, assistantInspectorReferenceView{
			Kind: reference.Kind, StableID: reference.StableID, Label: reference.Label, Icon: icon, Meta: meta,
		})
	}
	return views
}

func assistantReferencePresentation(kind string, locale webLocale) (string, string) {
	switch kind {
	case "directory":
		return "folder", webText(locale, "assistant.reference_kind.directory")
	case "file":
		return "file", webText(locale, "assistant.reference_kind.file")
	case "application":
		return "app-window", webText(locale, "assistant.reference_kind.application")
	case "website":
		return "network", webText(locale, "assistant.reference_kind.website")
	case "run":
		return "square-terminal", webText(locale, "assistant.reference_kind.run")
	case "quick_run":
		return "play", webText(locale, "assistant.reference_kind.quick_run")
	case "schedule":
		return "calendar-clock", webText(locale, "assistant.reference_kind.schedule")
	default:
		return "file", kind
	}
}

func assistantOperationViews(calls []assistant.ToolCall, locale webLocale) []assistantOperationView {
	views := make([]assistantOperationView, 0, len(calls))
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if !assistantExecutedStatefulCall(call) {
			continue
		}
		state, icon := "failed", "triangle-alert"
		if call.Status == "complete" {
			state, icon = "success", "check"
		} else if call.Status == "running" {
			state, icon = "running", "loader-circle"
		}
		label := webText(locale, "assistant.operation."+call.Name)
		if label == "assistant.operation."+call.Name {
			label = call.Name
		}
		target := strings.TrimSpace(call.TargetSummary)
		if target == "" {
			target = strings.TrimSpace(call.ParameterSummary)
		}
		if target != "" {
			label += " · " + target
		}
		views = append(views, assistantOperationView{
			ID: call.ID, Name: call.Name, Label: label, Status: webText(locale, "assistant.tool_status."+call.Status),
			State: state, Icon: icon, StartedAt: call.StartedAt, TimeLabel: call.StartedAt.Local().Format("15:04"),
		})
	}
	return views
}

func assistantExecutedStatefulCall(call assistant.ToolCall) bool {
	if !planStatefulTool(call.Name) || call.Status == "waiting_approval" || call.Status == "rejected" {
		return false
	}
	if call.Status == "cancelled" && call.ErrorCode == "approval_cancelled" {
		return false
	}
	if call.Status == "error" {
		switch call.ErrorCode {
		case "approval_invalid", "approval_expired", "approval_rejected", "approval_cancelled", "tool_target_changed", "tool_forbidden":
			return false
		}
	}
	return call.Status == "running" || call.Status == "complete" || call.Status == "error" || call.Status == "cancelled" || call.Status == "interrupted"
}

func assistantMessageTimeline(messages []assistant.Message, calls []assistant.ToolCall, locale webLocale) []assistantMessageView {
	byMessage := make(map[string][]assistant.ToolCall, len(messages))
	for _, call := range calls {
		byMessage[call.MessageID] = append(byMessage[call.MessageID], call)
	}
	timeline := make([]assistantMessageView, 0, len(messages))
	for _, message := range messages {
		messageCalls := byMessage[message.ID]
		view := assistantMessageView{Message: message, ToolCalls: messageCalls, Parts: assistantMessageParts(message, messageCalls, locale)}
		if len(messageCalls) > 0 {
			latest := messageCalls[len(messageCalls)-1]
			view.LatestToolCall = &latest
		}
		timeline = append(timeline, view)
	}
	return timeline
}

func assistantMessageParts(message assistant.Message, calls []assistant.ToolCall, locale webLocale) []assistantMessagePartView {
	if message.Role != "assistant" || len(calls) == 0 {
		return []assistantMessagePartView{{Locale: locale, Kind: "text", Body: message.Body}}
	}
	body := []rune(message.Body)
	ordered := append([]assistant.ToolCall(nil), calls...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].BodyOffset < ordered[right].BodyOffset
	})
	parts := make([]assistantMessagePartView, 0, len(ordered)*2+1)
	cursor := 0
	for index := 0; index < len(ordered); {
		offset := ordered[index].BodyOffset
		if offset < cursor {
			offset = cursor
		}
		if offset > len(body) {
			offset = len(body)
		}
		if offset > cursor {
			parts = append(parts, assistantMessagePartView{Locale: locale, Kind: "text", Body: string(body[cursor:offset])})
		}
		end := index + 1
		for end < len(ordered) {
			nextOffset := ordered[end].BodyOffset
			if nextOffset < cursor {
				nextOffset = cursor
			}
			if nextOffset > len(body) {
				nextOffset = len(body)
			}
			if nextOffset != offset {
				break
			}
			end++
		}
		group := append([]assistant.ToolCall(nil), ordered[index:end]...)
		latest := group[len(group)-1]
		aggregateStatus, resultSummary := assistantToolGroupPresentation(group, locale)
		parts = append(parts, assistantMessagePartView{
			Locale: locale, Kind: "tool", BodyOffset: offset, ToolCalls: group, LatestToolCall: &latest,
			Title: assistantToolGroupTitle(locale, len(group)), AggregateStatus: aggregateStatus, ResultSummary: resultSummary,
		})
		cursor = offset
		index = end
	}
	if cursor < len(body) || len(parts) == 0 || parts[len(parts)-1].Kind == "tool" {
		parts = append(parts, assistantMessagePartView{Locale: locale, Kind: "text", Body: string(body[cursor:])})
	}
	return parts
}

func assistantToolGroupPresentation(calls []assistant.ToolCall, locale webLocale) (string, string) {
	succeeded, failed, active := 0, 0, 0
	hasRunning, hasWaiting := false, false
	failureStatus := ""
	for _, call := range calls {
		switch call.Status {
		case "complete":
			succeeded++
		case "running":
			active++
			hasRunning = true
		case "waiting_approval":
			active++
			hasWaiting = true
		default:
			failed++
			if failureStatus == "" {
				failureStatus = call.Status
			}
		}
	}
	aggregateStatus := "complete"
	if hasRunning {
		aggregateStatus = "running"
	} else if hasWaiting {
		aggregateStatus = "waiting_approval"
	} else if failureStatus != "" {
		aggregateStatus = failureStatus
	}
	if active > 0 {
		return aggregateStatus, fmt.Sprintf(webText(locale, "assistant.tools_summary_active"), succeeded, failed, active)
	}
	return aggregateStatus, fmt.Sprintf(webText(locale, "assistant.tools_summary"), succeeded, failed)
}

func assistantToolGroupTitle(locale webLocale, count int) string {
	if count == 1 {
		return webText(locale, "assistant.tools_called_one")
	}
	return fmt.Sprintf(webText(locale, "assistant.tools_called_many"), count)
}

func (a *App) createAssistantConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.invalid_input"), http.StatusUnprocessableEntity)
		return
	}
	message := strings.TrimSpace(request.PostFormValue("message"))
	if message == "" {
		http.Error(response, webText(resolveWebLocale(request), "assistant.message_required"), http.StatusUnprocessableEntity)
		return
	}
	currentSession := request.Context().Value(sessionContextKey).(session)
	contextReferences, err := a.assistantContextReferencesFromForm(request, currentSession.role, nil)
	if err != nil {
		http.Error(response, assistantWebError(resolveWebLocale(request), err), http.StatusUnprocessableEntity)
		return
	}
	preparedPrompt, err := a.assistantPreparedPromptWithReferences(request.Context(), currentSession.role, message, contextReferences)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.image_invalid"), http.StatusUnprocessableEntity)
		return
	}
	var autoApproval *bool
	if value, exists := request.PostForm["auto_approval"]; exists {
		if len(value) == 0 {
			http.Error(response, webText(resolveWebLocale(request), "assistant.invalid_input"), http.StatusUnprocessableEntity)
			return
		}
		parsed, parseErr := strconv.ParseBool(value[len(value)-1])
		if parseErr != nil {
			http.Error(response, webText(resolveWebLocale(request), "assistant.invalid_input"), http.StatusUnprocessableEntity)
			return
		}
		autoApproval = &parsed
	}
	conversation, err := a.assistant.CreateConversation(request.Context(), assistantActor(request), assistant.ConversationInput{
		Title: request.PostFormValue("title"), ModelID: request.PostFormValue("model_id"), InitialMessage: message,
		Context: contextReferences, AutoApproval: autoApproval, CapabilityProfile: request.PostFormValue("profile"),
	})
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, assistant.ErrDisabled) {
			status = http.StatusConflict
		} else if !errors.Is(err, assistant.ErrModelRequired) && !errors.Is(err, assistant.ErrModelUnavailable) && !errors.Is(err, assistant.ErrInvalidInput) {
			status = http.StatusInternalServerError
		}
		http.Error(response, assistantWebError(resolveWebLocale(request), err), status)
		return
	}
	if managedRuntime, runtimeErr := a.assistantRuntime.Runtime(); runtimeErr == nil {
		reply, replyErr := a.assistant.BeginAssistantReply(request.Context(), assistantActor(request), conversation.ID)
		if replyErr == nil {
			if executeErr := a.assistantRuntime.ExecuteWithImages(request.Context(), assistantActor(request), conversation, preparedPrompt.Text, preparedPrompt.Images, reply); executeErr != nil {
				_, key := assistantRuntimeWebError(executeErr)
				_ = a.assistant.AppendAssistantText(request.Context(), assistantActor(request), conversation.ID, reply.ID, webText(resolveWebLocale(request), key))
				_ = a.assistant.FinishTurn(request.Context(), assistantActor(request), conversation.ID, reply.ID, "error", managedRuntime.Version)
			}
		}
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(conversation.ID), http.StatusSeeOther)
}

func (a *App) postAssistantMessage(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	message := strings.TrimSpace(request.FormValue("message"))
	if message == "" {
		http.Error(response, webText(resolveWebLocale(request), "assistant.message_required"), http.StatusUnprocessableEntity)
		return
	}
	actor := assistantActor(request)
	id := strings.TrimSpace(request.PathValue("id"))
	conversation, err := a.assistant.Conversation(request.Context(), actor, id)
	if errors.Is(err, assistant.ErrNotFound) {
		http.NotFound(response, request)
		return
	}
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	settings, err := a.assistant.Settings(request.Context())
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	if !settings.Enabled {
		status, key := assistantRuntimeWebError(assistant.ErrDisabled)
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	managedRuntime, err := a.assistantRuntime.Runtime()
	if err != nil {
		status, key := assistantRuntimeWebError(err)
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	references, err := a.assistant.ContextReferences(request.Context(), actor, id)
	if err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	currentSession := request.Context().Value(sessionContextKey).(session)
	references, err = a.assistantContextReferencesFromForm(request, currentSession.role, references)
	if err != nil {
		http.Error(response, assistantWebError(resolveWebLocale(request), err), http.StatusUnprocessableEntity)
		return
	}
	preparedPrompt, err := a.assistantPreparedPromptWithReferences(request.Context(), currentSession.role, message, references)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.image_invalid"), http.StatusUnprocessableEntity)
		return
	}
	turn, err := a.assistant.BeginTurnWithContext(request.Context(), actor, id, message, references)
	if err != nil {
		status, key := assistantRuntimeWebError(err)
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	conversation, err = a.assistant.Conversation(request.Context(), actor, id)
	if err != nil {
		_ = a.assistant.FinishTurn(request.Context(), actor, id, turn.Assistant.ID, "error", managedRuntime.Version)
		a.writeAssistantMutationError(response, request, err)
		return
	}
	if err := a.assistantRuntime.ExecuteWithImages(request.Context(), actor, conversation, preparedPrompt.Text, preparedPrompt.Images, turn.User, turn.Assistant); err != nil {
		status, key := assistantRuntimeWebError(err)
		_ = a.assistant.AppendAssistantText(request.Context(), actor, id, turn.Assistant.ID, webText(resolveWebLocale(request), key))
		_ = a.assistant.FinishTurn(request.Context(), actor, id, turn.Assistant.ID, "error", managedRuntime.Version)
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	if strings.Contains(request.Header.Get("Accept"), "application/json") {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte(`{"accepted":true}`))
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) abortAssistantTurn(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if _, err := a.assistant.Conversation(request.Context(), assistantActor(request), id); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	stopContext, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	err := a.assistantRuntime.Abort(stopContext, id)
	cancel()
	if err != nil {
		status, key := assistantRuntimeWebError(err)
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	if strings.Contains(request.Header.Get("Accept"), "application/json") {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = response.Write([]byte(`{"stopped":true}`))
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) assistantConversationEvents(response http.ResponseWriter, request *http.Request) {
	actor := assistantActor(request)
	id := strings.TrimSpace(request.PathValue("id"))
	if _, err := a.assistant.Conversation(request.Context(), actor, id); errors.Is(err, assistant.ErrNotFound) {
		http.NotFound(response, request)
		return
	} else if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	if !a.assistantRuntime.AcquireBrowserStream() {
		http.Error(response, webText(resolveWebLocale(request), "assistant.stream_capacity"), http.StatusTooManyRequests)
		return
	}
	defer a.assistantRuntime.ReleaseBrowserStream()
	after, _ := strconv.ParseUint(strings.TrimSpace(request.Header.Get("Last-Event-ID")), 10, 64)
	subscription := a.assistantRuntime.Subscribe(id, after)
	defer subscription.unsubscribe()
	defer func() {
		cancelContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.assistantRuntime.CancelApprovals(cancelContext, actor, id, "stream_disconnected")
		cancel()
	}()
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache, no-store")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	if after == 0 || subscription.reset {
		messages, err := a.assistant.Messages(request.Context(), actor, id)
		if err != nil {
			return
		}
		toolCalls, err := a.assistant.ToolCalls(request.Context(), actor, id)
		if err != nil {
			return
		}
		var pendingApproval *assistant.Approval
		if approval, approvalErr := a.assistant.PendingApproval(request.Context(), actor, id); approvalErr == nil && approval.Status == "pending" {
			pendingApproval = &approval
		}
		if !writeAssistantSSE(response, subscription.watermark, "snapshot", map[string]any{"messages": messages, "toolCalls": toolCalls, "approval": pendingApproval}) {
			return
		}
	}
	for _, event := range subscription.replay {
		if !writeAssistantSSE(response, event.ID, event.Type, event) {
			return
		}
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-subscription.events:
			if !open || !writeAssistantSSE(response, event.ID, event.Type, event) {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := response.Write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeAssistantSSE(response http.ResponseWriter, id uint64, eventType string, value any) bool {
	payload, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(response, "id: %d\nevent: %s\ndata: %s\n\n", id, eventType, payload)
	return err == nil
}

func (a *App) setAssistantConversationModel(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if err := a.assistant.SetConversationModel(request.Context(), assistantActor(request), id, request.FormValue("model_id")); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	_ = a.assistantRuntime.Abort(stopContext, id)
	cancel()
	if strings.Contains(request.Header.Get("Accept"), "application/json") {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprintf(response, `{"model_id":%q}`, request.FormValue("model_id"))
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) setAssistantApprovalMode(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	enabled, err := strconv.ParseBool(request.FormValue("auto_approval"))
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.invalid_input"), http.StatusUnprocessableEntity)
		return
	}
	if err := a.assistant.SetAutoApproval(request.Context(), assistantActor(request), id, enabled); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	if strings.Contains(request.Header.Get("Accept"), "application/json") {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprintf(response, `{"auto_approval":%t}`, enabled)
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) setAssistantCapabilityProfile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	profile := strings.TrimSpace(request.FormValue("profile"))
	if err := a.assistant.SetCapabilityProfile(request.Context(), assistantActor(request), id, profile); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	_ = a.assistantRuntime.Abort(stopContext, id)
	cancel()
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) setAssistantThinkingLevel(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if err := a.assistant.SetThinkingLevel(request.Context(), assistantActor(request), id, request.FormValue("thinking_level")); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	_ = a.assistantRuntime.Abort(stopContext, id)
	cancel()
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) compactAssistantConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	actor := assistantActor(request)
	if _, err := a.assistant.Conversation(request.Context(), actor, id); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	if _, err := a.assistantRuntime.Compact(request.Context(), actor, id); err != nil {
		status := http.StatusConflict
		if !errors.Is(err, assistant.ErrConversationBusy) && !errors.Is(err, errAssistantSessionUnavailable) {
			status = http.StatusBadGateway
		}
		http.Error(response, webText(resolveWebLocale(request), "assistant.compact_unavailable"), status)
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) resolveAssistantApproval(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	conversationID := strings.TrimSpace(request.PathValue("id"))
	approvalID := strings.TrimSpace(request.PathValue("approval_id"))
	decision := strings.TrimSpace(request.FormValue("decision"))
	if approvalID == "" || (decision != "approve" && decision != "reject") {
		http.Error(response, webText(resolveWebLocale(request), "assistant.invalid_input"), http.StatusUnprocessableEntity)
		return
	}
	actor := assistantActor(request)
	if _, err := a.assistant.Conversation(request.Context(), actor, conversationID); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	approval, err := a.assistantRuntime.ResolveApproval(request.Context(), actor, conversationID, approvalID, decision == "approve")
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, assistant.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, assistant.ErrApprovalExpired) {
			status = http.StatusGone
		}
		http.Error(response, webText(resolveWebLocale(request), "assistant.approval_invalid"), status)
		return
	}
	if strings.Contains(request.Header.Get("Accept"), "application/json") {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(map[string]any{"approvalId": approval.ID, "status": approval.Status})
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(conversationID), http.StatusSeeOther)
}

func (a *App) archiveAssistantConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if _, err := a.assistant.Conversation(request.Context(), assistantActor(request), id); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	stopContext, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	if err := a.assistantRuntime.Abort(stopContext, id); err != nil {
		cancel()
		status, key := assistantRuntimeWebError(err)
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	cancel()
	if err := a.assistant.ArchiveConversation(request.Context(), assistantActor(request), id); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	http.Redirect(response, request, "/ai", http.StatusSeeOther)
}

func (a *App) restoreAssistantConversation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if err := a.assistant.RestoreConversation(request.Context(), assistantActor(request), id); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	http.Redirect(response, request, "/ai/conversations/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) writeAssistantMutationError(response http.ResponseWriter, request *http.Request, err error) {
	status := http.StatusUnprocessableEntity
	if errors.Is(err, assistant.ErrNotFound) {
		status = http.StatusNotFound
	} else if errors.Is(err, assistant.ErrDisabled) {
		status = http.StatusConflict
	} else if errors.Is(err, assistant.ErrConversationBusy) {
		status = http.StatusConflict
	} else if !errors.Is(err, assistant.ErrInvalidInput) && !errors.Is(err, assistant.ErrModelUnavailable) {
		status = http.StatusInternalServerError
	}
	http.Error(response, assistantWebError(resolveWebLocale(request), err), status)
}

func assistantWebError(locale webLocale, err error) string {
	switch {
	case errors.Is(err, assistant.ErrModelRequired):
		return webText(locale, "assistant.model_required")
	case errors.Is(err, assistant.ErrModelUnavailable):
		return webText(locale, "assistant.model_unavailable")
	case errors.Is(err, assistant.ErrDisabled):
		return webText(locale, "assistant.disabled")
	case errors.Is(err, assistant.ErrConversationBusy):
		return webText(locale, "assistant.conversation_busy")
	case errors.Is(err, assistant.ErrNotFound):
		return webText(locale, "error.not_found")
	case errors.Is(err, assistant.ErrInvalidInput):
		return webText(locale, "assistant.invalid_input")
	default:
		return webText(locale, "assistant.save_failed")
	}
}

func (a *App) assistantSettingsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	models, err := a.assistant.ListModels(request.Context(), assistantActor(request))
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	settings, err := a.assistant.Settings(request.Context())
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.load_failed"), http.StatusInternalServerError)
		return
	}
	views := assistantModelViews(models)
	var defaultModel *assistantModelView
	for index := range views {
		if views[index].Default {
			defaultModel = &views[index]
			break
		}
	}
	locale := resolveWebLocale(request)
	managedRuntime, runtimeErr := a.assistantRuntime.Runtime()
	runtimeSnapshot := a.assistantRuntimes.Snapshot()
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = assistantSettingsTemplate.Execute(response, assistantSettingsPageData{
		Locale: locale, CSRFToken: current.csrfToken, SettingsNavigation: newSettingsNavigation(current, locale, "ai"),
		Settings: settings, Models: views, DefaultModel: defaultModel,
		RuntimeAvailable: runtimeErr == nil, RuntimeVersion: managedRuntime.Version,
		Runtime: runtimeSnapshot, ActiveProcesses: a.assistantRuntime.ActiveProcesses(),
		ProviderTestPassed:      request.URL.Query().Get("provider_test") == "passed",
		RuntimeOfflineInstalled: request.URL.Query().Get("runtime_install") == "offline",
	})
}

func (a *App) testAssistantModel(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	modelID := strings.TrimSpace(request.PathValue("id"))
	ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
	defer cancel()
	if err := a.assistantRuntime.TestModel(ctx, assistantActor(request), modelID); err != nil {
		statusContext, statusCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = a.assistant.SetModelConnectionOK(statusContext, modelID, false)
		statusCancel()
		a.recordAuditForRequest(request, "assistant_provider_test", modelID, "failed")
		status, _ := assistantRuntimeWebError(err)
		if status == http.StatusInternalServerError {
			status = http.StatusBadGateway
		}
		message := webText(locale, "assistant.provider_test_failed")
		if acceptsJSON(request) {
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(status)
			_ = json.NewEncoder(response).Encode(map[string]any{"ok": false, "message": message})
			return
		}
		http.Error(response, message, status)
		return
	}
	statusContext, statusCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer statusCancel()
	if err := a.assistant.SetModelConnectionOK(statusContext, modelID, true); err != nil {
		a.recordAuditForRequest(request, "assistant_provider_test", modelID, "failed")
		http.Error(response, webText(resolveWebLocale(request), "assistant.provider_test_failed"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "assistant_provider_test", modelID, "succeeded")
	if acceptsJSON(request) {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(map[string]any{"ok": true, "message": webText(locale, "assistant.provider_test_passed")})
		return
	}
	http.Redirect(response, request, "/settings/ai?provider_test=passed", http.StatusSeeOther)
}

func (a *App) checkAssistantRuntime(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), time.Minute)
	defer cancel()
	if _, err := a.assistantRuntimes.CheckOnline(ctx); err != nil {
		a.recordAuditForRequest(request, "assistant_runtime_check", "runtime", "failed")
		http.Error(response, webText(resolveWebLocale(request), "assistant.runtime_check_failed"), http.StatusBadGateway)
		return
	}
	a.recordAuditForRequest(request, "assistant_runtime_check", "runtime", "succeeded")
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) installAssistantRuntime(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Minute)
	defer cancel()
	if err := a.assistantRuntimes.InstallOnline(ctx); err != nil {
		a.recordAuditForRequest(request, "assistant_runtime_install", "online", "failed")
		http.Error(response, webText(resolveWebLocale(request), "assistant.runtime_install_failed"), assistantRuntimeMutationStatus(err))
		return
	}
	a.recordAuditForRequest(request, "assistant_runtime_install", a.assistantRuntimes.Snapshot().ActiveVersion, "succeeded")
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

var errAssistantRuntimeUploadTooLarge = errors.New("assistant runtime upload part is too large")

func readAssistantRuntimeUploadPart(reader io.Reader, maximum int64) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > maximum {
		return nil, errAssistantRuntimeUploadTooLarge
	}
	return value, nil
}

func (a *App) installAssistantRuntimeOffline(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if request.ContentLength > maxAssistantRuntimeOfflineRequestBytes {
		http.Error(response, webText(locale, "assistant.runtime_offline_too_large"), http.StatusRequestEntityTooLarge)
		return
	}
	resetReadDeadline := setRequestReadDeadline(response, 15*time.Minute)
	defer resetReadDeadline()
	request.Body = http.MaxBytesReader(response, request.Body, maxAssistantRuntimeOfflineRequestBytes)

	reader, err := request.MultipartReader()
	if err != nil {
		if !validSessionCSRF(request) {
			http.Error(response, webText(locale, "assistant.csrf_error"), http.StatusForbidden)
			return
		}
		http.Error(response, webText(locale, "assistant.runtime_offline_invalid_request"), http.StatusBadRequest)
		return
	}

	var csrfToken string
	var manifestRaw, signatureRaw []byte
	seen := make(map[string]bool)
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			if !validSessionCSRFValue(request, csrfToken) {
				http.Error(response, webText(locale, "assistant.csrf_error"), http.StatusForbidden)
				return
			}
			http.Error(response, webText(locale, "assistant.runtime_offline_missing"), http.StatusBadRequest)
			return
		}
		if nextErr != nil {
			var maximumError *http.MaxBytesError
			if errors.As(nextErr, &maximumError) {
				http.Error(response, webText(locale, "assistant.runtime_offline_too_large"), http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(response, webText(locale, "assistant.runtime_offline_invalid_request"), http.StatusBadRequest)
			return
		}

		field := part.FormName()
		if seen[field] || field == "" {
			_ = part.Close()
			http.Error(response, webText(locale, "assistant.runtime_offline_invalid_request"), http.StatusBadRequest)
			return
		}
		seen[field] = true
		switch field {
		case "csrf_token":
			value, readErr := readAssistantRuntimeUploadPart(part, 64<<10)
			_ = part.Close()
			if readErr != nil {
				http.Error(response, webText(locale, "assistant.csrf_error"), http.StatusForbidden)
				return
			}
			csrfToken = string(value)
		case "runtime_manifest":
			if !validSessionCSRFValue(request, csrfToken) {
				_ = part.Close()
				http.Error(response, webText(locale, "assistant.csrf_error"), http.StatusForbidden)
				return
			}
			manifestRaw, err = readAssistantRuntimeUploadPart(part, runtimeinstall.MaxManifestBytes)
			_ = part.Close()
			if err != nil {
				http.Error(response, webText(locale, "assistant.runtime_offline_too_large"), http.StatusRequestEntityTooLarge)
				return
			}
		case "runtime_signature":
			if !validSessionCSRFValue(request, csrfToken) {
				_ = part.Close()
				http.Error(response, webText(locale, "assistant.csrf_error"), http.StatusForbidden)
				return
			}
			signatureRaw, err = readAssistantRuntimeUploadPart(part, runtimeinstall.MaxSignatureBytes)
			_ = part.Close()
			if err != nil {
				http.Error(response, webText(locale, "assistant.runtime_offline_too_large"), http.StatusRequestEntityTooLarge)
				return
			}
		case "runtime_archive":
			if !validSessionCSRFValue(request, csrfToken) {
				_ = part.Close()
				http.Error(response, webText(locale, "assistant.csrf_error"), http.StatusForbidden)
				return
			}
			if len(manifestRaw) == 0 || len(signatureRaw) == 0 || part.FileName() == "" {
				_ = part.Close()
				http.Error(response, webText(locale, "assistant.runtime_offline_missing"), http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(request.Context(), 15*time.Minute)
			installErr := a.assistantRuntimes.InstallOffline(ctx, manifestRaw, signatureRaw, part)
			cancel()
			_ = part.Close()
			if installErr != nil {
				a.recordAuditForRequest(request, "assistant_runtime_install", "offline", "failed")
				status := assistantRuntimeMutationStatus(installErr)
				if status == http.StatusBadRequest {
					status = http.StatusUnprocessableEntity
				}
				http.Error(response, webText(locale, "assistant.runtime_offline_invalid"), status)
				return
			}
			a.recordAuditForRequest(request, "assistant_runtime_install", a.assistantRuntimes.Snapshot().ActiveVersion, "succeeded")
			http.Redirect(response, request, "/settings/ai?runtime_install=offline", http.StatusSeeOther)
			return
		default:
			_ = part.Close()
			http.Error(response, webText(locale, "assistant.runtime_offline_invalid_request"), http.StatusBadRequest)
			return
		}
	}
}

func (a *App) rollbackAssistantRuntime(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), time.Minute)
	defer cancel()
	if err := a.assistantRuntimes.Rollback(ctx); err != nil {
		a.recordAuditForRequest(request, "assistant_runtime_rollback", "runtime", "failed")
		http.Error(response, webText(resolveWebLocale(request), "assistant.runtime_rollback_failed"), assistantRuntimeMutationStatus(err))
		return
	}
	a.recordAuditForRequest(request, "assistant_runtime_rollback", a.assistantRuntimes.Snapshot().ActiveVersion, "succeeded")
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func assistantRuntimeMutationStatus(err error) int {
	if errors.Is(err, runtimeinstall.ErrRuntimeBusy) || errors.Is(err, runtimeinstall.ErrOperationActive) {
		return http.StatusConflict
	}
	if errors.Is(err, runtimeinstall.ErrArchiveUntrusted) {
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadRequest
}

func (a *App) saveAssistantModel(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.FormValue("id"))
	supportsReasoning := request.FormValue("supports_reasoning") != ""
	defaultThinkingLevel := request.FormValue("default_thinking_level")
	if !supportsReasoning {
		defaultThinkingLevel = "off"
	}
	model, err := a.assistant.SaveModel(request.Context(), assistantActor(request), id, assistant.ModelInput{
		Name: request.FormValue("name"), Provider: request.FormValue("provider"), Model: request.FormValue("model"),
		Endpoint: request.FormValue("endpoint"), APIKey: request.FormValue("api_key"),
		MakeDefault: request.FormValue("make_default") != "", SupportsImages: request.FormValue("supports_images") != "",
		SupportsReasoning: supportsReasoning, DefaultThinkingLevel: defaultThinkingLevel,
		Shared: request.FormValue("shared") != "",
	})
	if err != nil {
		status := http.StatusUnprocessableEntity
		if !errors.Is(err, assistant.ErrInvalidInput) && !errors.Is(err, assistant.ErrNotFound) {
			status = http.StatusInternalServerError
		}
		http.Error(response, assistantWebError(resolveWebLocale(request), err), status)
		return
	}
	action := "assistant_llm_create"
	if id != "" {
		action = "assistant_llm_update"
	}
	a.recordAuditForRequest(request, action, model.ID, "succeeded")
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) setDefaultAssistantModel(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if err := a.assistant.SetDefaultModel(request.Context(), assistantActor(request), id); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "assistant_llm_default", id, "succeeded")
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) deleteAssistantModel(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if err := a.assistant.DeleteModel(request.Context(), assistantActor(request), id); err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "assistant_llm_delete", id, "succeeded")
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func (a *App) saveAssistantDefaults(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	maximum, err := strconv.Atoi(request.FormValue("max_active_conversations"))
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "assistant.invalid_input"), http.StatusUnprocessableEntity)
		return
	}
	err = a.assistant.UpdateSettings(request.Context(), assistantActor(request), assistant.SettingsInput{
		Enabled: request.FormValue("enabled") != "", DefaultAutoApproval: request.FormValue("default_auto_approval") != "",
		MaxActiveConversations: maximum,
	})
	if err != nil {
		a.writeAssistantMutationError(response, request, err)
		return
	}
	_ = a.assistantRuntime.SetMaximum(maximum)
	a.recordAuditForRequest(request, "assistant_settings_update", "assistant", "succeeded")
	http.Redirect(response, request, "/settings/ai", http.StatusSeeOther)
}

func assistantModelViews(models []assistant.ModelConfig) []assistantModelView {
	views := make([]assistantModelView, 0, len(models))
	for _, model := range models {
		providerLabel := "OpenAI Compatible"
		switch model.Provider {
		case assistant.ProviderOpenAI:
			providerLabel = "OpenAI"
		case assistant.ProviderAnthropic:
			providerLabel = "Anthropic"
		}
		views = append(views, assistantModelView{
			ModelConfig: model, ProviderLabel: providerLabel,
			EndpointDisplay: strings.TrimPrefix(strings.TrimPrefix(model.Endpoint, "https://"), "http://"),
		})
	}
	return views
}

func (a *App) assistantResourceSearch(response http.ResponseWriter, request *http.Request) {
	currentSession := request.Context().Value(sessionContextKey).(session)
	if !roleAllows(currentSession.role, permissionReadFiles) || a.files == nil {
		http.Error(response, "File references are not available for this role", http.StatusForbidden)
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("query"))
	if query == "" || len(query) > 4096 || !filepath.IsAbs(query) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(map[string]any{"resources": []assistantResourceView{}})
		return
	}
	resources, err := a.assistantHostPathResources(request.Context(), query)
	if err != nil {
		http.Error(response, "Unable to search host path", http.StatusBadRequest)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(map[string]any{"resources": resources})
}

func (a *App) assistantHostPathResources(ctx context.Context, query string) ([]assistantResourceView, error) {
	query = filepath.Clean(query)
	if roots, err := a.hostRoots(ctx); err == nil {
		for _, root := range roots {
			if hostfiles.ComparisonKey(root.Path) == hostfiles.ComparisonKey(query) {
				return []assistantResourceView{{Kind: "directory", ID: root.Name, Label: root.Name, Detail: query, Icon: "folder", Category: "files"}}, nil
			}
		}
	}
	parent, nameQuery := filepath.Dir(query), strings.ToLower(filepath.Base(query))
	entries, err := a.hostList(ctx, parent)
	if err != nil {
		return nil, err
	}
	resources := make([]assistantResourceView, 0, 24)
	for _, entry := range entries {
		if entry.Hidden || entry.Kind != hostfiles.Directory && entry.Kind != hostfiles.Regular {
			continue
		}
		if nameQuery != "." && nameQuery != "" && !strings.Contains(strings.ToLower(entry.Name), nameQuery) {
			continue
		}
		resource, found := a.assistantHostEntryResource(ctx, entry)
		if !found {
			continue
		}
		resources = append(resources, resource)
		if hostfiles.ComparisonKey(entry.Path) == hostfiles.ComparisonKey(query) || len(resources) >= 24 {
			break
		}
	}
	return resources, nil
}

func (a *App) assistantHostEntryResource(ctx context.Context, entry hostfiles.Entry) (assistantResourceView, bool) {
	roots, err := a.hostRoots(ctx)
	if err != nil {
		return assistantResourceView{}, false
	}
	for _, root := range roots {
		if !hostfiles.Contains(root.Path, entry.Path) {
			continue
		}
		relativePath, relativeErr := filepath.Rel(root.Path, entry.Path)
		if relativeErr != nil || relativePath == "." || filepath.IsAbs(relativePath) {
			return assistantResourceView{}, false
		}
		kind, icon := "directory", "folder"
		if entry.Kind == hostfiles.Regular {
			kind, icon = "file", "file-text"
			if assistantRasterFilename(entry.Name) {
				icon = "image"
			}
		}
		stableID := assistantPathStableID(kind, root.Name, relativePath)
		if filepath.Dir(relativePath) == "." {
			if kind == "file" {
				stableID = assistantFileStableID(root.Name, entry.Name)
			} else {
				stableID = assistantDirectoryStableID(root.Name, entry.Name)
			}
		}
		return assistantResourceView{
			Kind: kind, ID: stableID, Label: entry.Name, Detail: filepath.Dir(entry.Path), Icon: icon,
			Category: "files", ImageHint: kind == "file" && assistantRasterFilename(entry.Name),
		}, true
	}
	return assistantResourceView{}, false
}

func (a *App) assistantResourceCatalog(request *http.Request, role userRole) []assistantResourceView {
	resources := make([]assistantResourceView, 0, 40)
	if roleAllows(role, permissionReadFiles) && a.files != nil {
		if roots, err := a.hostRoots(request.Context()); err == nil {
			fileCount := 0
			directoryCount := 0
			for _, root := range roots {
				resources = append(resources, assistantResourceView{Kind: "directory", ID: root.Name, Label: root.Name, Detail: webText(resolveWebLocale(request), "assistant.resource_host_root"), Icon: "folder", Category: "files"})
				if fileCount >= 8 && directoryCount >= 8 {
					continue
				}
				entries, listErr := a.hostList(request.Context(), root.Path)
				if listErr != nil {
					continue
				}
				for _, entry := range entries {
					if entry.Hidden {
						continue
					}
					switch entry.Kind {
					case hostfiles.Directory:
						if directoryCount < 8 {
							resources = append(resources, assistantResourceView{
								Kind: "directory", ID: assistantDirectoryStableID(root.Name, entry.Name), Label: entry.Name,
								Detail: root.Name, Icon: "folder", Category: "files",
							})
							directoryCount++
						}
					case hostfiles.Regular:
						if fileCount < 8 {
							imageHint := assistantRasterFilename(entry.Name)
							icon := "file-text"
							if imageHint {
								icon = "image"
							}
							resources = append(resources, assistantResourceView{
								Kind: "file", ID: assistantFileStableID(root.Name, entry.Name), Label: entry.Name,
								Detail: root.Name, Icon: icon, Category: "files", ImageHint: imageHint,
							})
							fileCount++
						}
					}
				}
			}
		}
	}
	if view, err := a.applicationStatus.View(request.Context(), appstatus.Query{Limit: 8}); err == nil {
		for index, application := range append(view.Pinned, view.Applications...) {
			if index >= 8 {
				break
			}
			resources = append(resources, assistantResourceView{Kind: "application", ID: application.ID, Label: application.Name, Detail: string(application.Kind), Icon: "app-window", Category: "applications"})
		}
	}
	if monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{}); err == nil {
		for index, monitor := range monitors {
			if index >= 6 {
				break
			}
			resources = append(resources, assistantResourceView{Kind: "website", ID: monitor.ID, Label: monitor.Config.Name, Detail: string(monitor.State), Icon: "network", Category: "monitors"})
		}
	}
	if a.db != nil {
		rows, err := a.db.QueryContext(request.Context(), "SELECT id, name, locked FROM quick_runs ORDER BY sort_order, created_at LIMIT 6")
		if err == nil {
			for rows.Next() {
				var id, name string
				var locked int64
				if rows.Scan(&id, &name, &locked) == nil {
					detail := webText(resolveWebLocale(request), "assistant.resource_quick_run")
					if locked != 0 {
						detail += " · " + webText(resolveWebLocale(request), "assistant.resource_locked")
					}
					resources = append(resources, assistantResourceView{Kind: "quick_run", ID: id, Label: name, Detail: detail, Icon: "play", Category: "automation"})
				}
			}
			_ = rows.Close()
		}
		rows, err = a.db.QueryContext(request.Context(), "SELECT id, name, enabled FROM schedules WHERE deleted = 0 ORDER BY name COLLATE NOCASE LIMIT 6")
		if err == nil {
			for rows.Next() {
				var id, name string
				var enabled int64
				if rows.Scan(&id, &name, &enabled) == nil {
					stateKey := "assistant.resource_disabled"
					if enabled != 0 {
						stateKey = "assistant.resource_enabled"
					}
					resources = append(resources, assistantResourceView{
						Kind: "schedule", ID: id, Label: name,
						Detail: webText(resolveWebLocale(request), "assistant.resource_schedule") + " · " + webText(resolveWebLocale(request), stateKey),
						Icon:   "calendar-clock", Category: "automation",
					})
				}
			}
			_ = rows.Close()
		}
		rows, err = a.db.QueryContext(request.Context(), "SELECT id, source_name, status FROM runs ORDER BY created_at DESC LIMIT 6")
		if err == nil {
			for rows.Next() {
				var id, name, status string
				if rows.Scan(&id, &name, &status) == nil {
					if strings.TrimSpace(name) == "" {
						name = id
					}
					resources = append(resources, assistantResourceView{Kind: "run", ID: id, Label: name, Detail: status, Icon: "square-terminal", Category: "automation"})
				}
			}
			_ = rows.Close()
		}
	}
	return deduplicateAssistantResources(resources)
}

func assistantRasterFilename(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}

func deduplicateAssistantResources(resources []assistantResourceView) []assistantResourceView {
	seen := make(map[string]struct{}, len(resources))
	result := make([]assistantResourceView, 0, len(resources))
	for _, resource := range resources {
		key := resource.Kind + "\x00" + resource.ID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, resource)
	}
	return result
}

func markAssistantResourcesSelected(resources []assistantResourceView, references []assistant.ContextRef) []assistantResourceView {
	selected := make(map[string]struct{}, len(references))
	for _, reference := range references {
		selected[reference.Kind+"\x00"+reference.StableID] = struct{}{}
	}
	for index := range resources {
		_, resources[index].Selected = selected[resources[index].Kind+"\x00"+resources[index].ID]
	}
	return resources
}

func (a *App) assistantContextReferencesFromForm(request *http.Request, role userRole, existing []assistant.ContextRef) ([]assistant.ContextRef, error) {
	kinds := request.PostForm["context_kind"]
	ids := request.PostForm["context_id"]
	if len(kinds) != len(ids) || len(kinds) > 32 {
		return nil, fmt.Errorf("%w: malformed context references", assistant.ErrInvalidInput)
	}
	available := a.assistantResourceCatalog(request, role)
	byKey := make(map[string]assistantResourceView, len(available))
	for _, resource := range available {
		byKey[resource.Kind+"\x00"+resource.ID] = resource
	}
	existingByKey := make(map[string]assistant.ContextRef, len(existing))
	for _, reference := range existing {
		existingByKey[reference.Kind+"\x00"+reference.StableID] = reference
	}
	references := make([]assistant.ContextRef, 0, len(kinds))
	seen := make(map[string]struct{}, len(kinds))
	for index := range kinds {
		key := strings.TrimSpace(kinds[index]) + "\x00" + strings.TrimSpace(ids[index])
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		resource, exists := byKey[key]
		if !exists {
			persisted, persistedExists := existingByKey[key]
			if persistedExists {
				resource = assistantResourceView{Kind: persisted.Kind, ID: persisted.StableID, Label: persisted.Label}
			} else if kinds[index] == "file" || kinds[index] == "directory" {
				entry, found := a.assistantHostEntryByStableID(request.Context(), kinds[index], strings.TrimSpace(ids[index]))
				if !found {
					return nil, fmt.Errorf("%w: unknown context reference", assistant.ErrInvalidInput)
				}
				resource, found = a.assistantHostEntryResource(request.Context(), entry)
				if !found {
					return nil, fmt.Errorf("%w: unknown context reference", assistant.ErrInvalidInput)
				}
			} else {
				return nil, fmt.Errorf("%w: unknown context reference", assistant.ErrInvalidInput)
			}
		}
		seen[key] = struct{}{}
		references = append(references, assistant.ContextRef{Kind: resource.Kind, StableID: resource.ID, Label: resource.Label})
	}
	return references, nil
}
