package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/assistant"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/websitemonitor"
)

type assistantModelView struct {
	assistant.ModelConfig
	ProviderLabel, EndpointDisplay string
}

type assistantResourceView struct {
	Kind, ID, Label, Detail, Icon, Category string
	Selected                                bool
}

type assistantPageData struct {
	Locale              webLocale
	CSRFToken           string
	Models              []assistantModelView
	Conversations       []assistant.Conversation
	Archived            []assistant.Conversation
	Current             *assistant.Conversation
	SelectedModelID     string
	DefaultAutoApproval bool
	RuntimeAvailable    bool
	RuntimeVersion      string
	CanManageAI         bool
	Resources           []assistantResourceView
	ContextReferences   []assistant.ContextRef
	Messages            []assistant.Message
	AssistantEnabled    bool
}

type assistantSettingsPageData struct {
	Locale             webLocale
	CSRFToken          string
	SettingsNavigation settingsNavigationData
	Settings           assistant.Settings
	Models             []assistantModelView
	DefaultModel       *assistantModelView
	RuntimeAvailable   bool
	RuntimeVersion     string
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
	models, err := a.assistant.ListModels(request.Context())
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
	selectedModelID := ""
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
	locale := resolveWebLocale(request)
	resources := markAssistantResourcesSelected(a.assistantResourceCatalog(request, currentSession.role), contextReferences)
	managedRuntime, runtimeErr := a.assistantRuntime.Runtime()
	runtimeAvailable := runtimeErr == nil
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = assistantTemplate.Execute(response, assistantPageData{
		Locale: locale, CSRFToken: currentSession.csrfToken, Models: assistantModelViews(models),
		Conversations: conversations, Archived: archived, Current: current, SelectedModelID: selectedModelID,
		DefaultAutoApproval: settings.DefaultAutoApproval, RuntimeAvailable: runtimeAvailable, RuntimeVersion: managedRuntime.Version,
		CanManageAI: roleAllows(currentSession.role, permissionManageSystem),
		Resources:   resources, ContextReferences: contextReferences, Messages: messages, AssistantEnabled: settings.Enabled,
	})
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
		Context: contextReferences, AutoApproval: autoApproval,
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
			prompt := a.assistantPromptWithReferences(request.Context(), currentSession.role, message, contextReferences)
			if executeErr := a.assistantRuntime.Execute(request.Context(), assistantActor(request), conversation, prompt, reply); executeErr != nil {
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
	if err := a.assistantRuntime.Execute(request.Context(), actor, conversation, a.assistantPromptWithReferences(request.Context(), currentSession.role, message, references), turn.User, turn.Assistant); err != nil {
		_ = a.assistant.FinishTurn(request.Context(), actor, id, turn.Assistant.ID, "error", managedRuntime.Version)
		status, key := assistantRuntimeWebError(err)
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
		if !writeAssistantSSE(response, subscription.watermark, "snapshot", map[string]any{"messages": messages}) {
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
	models, err := a.assistant.ListModels(request.Context())
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
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = assistantSettingsTemplate.Execute(response, assistantSettingsPageData{
		Locale: locale, CSRFToken: current.csrfToken, SettingsNavigation: newSettingsNavigation(current, locale, "ai"),
		Settings: settings, Models: views, DefaultModel: defaultModel,
		RuntimeAvailable: runtimeErr == nil, RuntimeVersion: managedRuntime.Version,
	})
}

func (a *App) saveAssistantModel(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "assistant.csrf_error"), http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(request.FormValue("id"))
	model, err := a.assistant.SaveModel(request.Context(), assistantActor(request), id, assistant.ModelInput{
		Name: request.FormValue("name"), Provider: request.FormValue("provider"), Model: request.FormValue("model"),
		Endpoint: request.FormValue("endpoint"), APIKey: request.FormValue("api_key"),
		MakeDefault: request.FormValue("make_default") != "",
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

func (a *App) assistantResourceCatalog(request *http.Request, role userRole) []assistantResourceView {
	resources := make([]assistantResourceView, 0, 40)
	if roleAllows(role, permissionReadFiles) && a.files != nil {
		if roots, err := a.files.Roots(); err == nil {
			fileCount := 0
			directoryCount := 0
			for _, root := range roots {
				resources = append(resources, assistantResourceView{Kind: "directory", ID: root.Name, Label: root.Name, Detail: webText(resolveWebLocale(request), "assistant.resource_host_root"), Icon: "folder", Category: "files"})
				if fileCount >= 8 && directoryCount >= 8 {
					continue
				}
				entries, listErr := a.files.List(root.Path)
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
							resources = append(resources, assistantResourceView{
								Kind: "file", ID: assistantFileStableID(root.Name, entry.Name), Label: entry.Name,
								Detail: root.Name, Icon: "file-text", Category: "files",
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
			if !persistedExists {
				return nil, fmt.Errorf("%w: unknown context reference", assistant.ErrInvalidInput)
			}
			resource = assistantResourceView{Kind: persisted.Kind, ID: persisted.StableID, Label: persisted.Label}
		}
		seen[key] = struct{}{}
		references = append(references, assistant.ContextRef{Kind: resource.Kind, StableID: resource.ID, Label: resource.Label})
	}
	return references, nil
}
