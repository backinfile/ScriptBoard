package assistant

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestModelConfigurationsKeepCredentialsOutsideSQLiteAndMaintainOneDefault(t *testing.T) {
	t.Parallel()

	service, db, stateRoot := newTestService(t)
	actor := Actor{UserID: "admin-one", Username: "admin"}
	ctx := context.Background()

	primary, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "OpenAI · Production", Provider: ProviderOpenAI, Model: "gpt-5.2",
		Endpoint: "https://api.openai.com/v1", APIKey: "sk-secret-primary", MakeDefault: true,
	})
	if err != nil {
		t.Fatalf("save primary model: %v", err)
	}
	secondary, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Anthropic · Operations", Provider: ProviderAnthropic, Model: "claude-opus-4-1",
		Endpoint: "https://api.anthropic.com", APIKey: "sk-secret-secondary", MakeDefault: true,
	})
	if err != nil {
		t.Fatalf("save secondary model: %v", err)
	}

	models, err := service.ListModels(ctx, actor)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("model count = %d, want 2", len(models))
	}
	for _, model := range models {
		if model.ID == secondary.ID && !model.Default {
			t.Fatalf("new default model is not marked default: %+v", model)
		}
		if model.ID == primary.ID && model.Default {
			t.Fatalf("old default model remained default: %+v", model)
		}
		if !model.CredentialConfigured {
			t.Fatalf("model credential status is false: %+v", model)
		}
	}

	rows, err := db.QueryContext(ctx, "SELECT name, provider, model, endpoint FROM assistant_models")
	if err != nil {
		t.Fatalf("query model rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var values [4]string
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
			t.Fatalf("scan model row: %v", err)
		}
		if strings.Contains(strings.Join(values[:], " "), "sk-secret") {
			t.Fatal("provider credential was stored in SQLite")
		}
	}

	secretPath := filepath.Join(stateRoot, "secrets", "assistant-provider.enc")
	secretBody, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	if strings.Contains(string(secretBody), "sk-secret-primary") || strings.Contains(string(secretBody), "sk-secret-secondary") {
		t.Fatalf("credential file contains plaintext provider keys: %s", secretBody)
	}

	updated, err := service.SaveModel(ctx, actor, primary.ID, ModelInput{
		Name: "OpenAI · Primary", Provider: ProviderOpenAI, Model: "gpt-5.3",
		Endpoint: "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("update model without replacing credential: %v", err)
	}
	if !updated.CredentialConfigured || updated.Model != "gpt-5.3" {
		t.Fatalf("updated model = %+v", updated)
	}
	credential, err := service.ModelCredential(ctx, primary.ID)
	if err != nil {
		t.Fatalf("read model credential: %v", err)
	}
	if credential != "sk-secret-primary" {
		t.Fatalf("credential changed during metadata-only edit")
	}
}

func TestServiceMigratesPlaintextProviderCredentialsToSealedStorage(t *testing.T) {
	stateRoot := t.TempDir()
	secretsDirectory := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(secretsDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(secretsDirectory, "assistant-provider.json")
	if err := os.WriteFile(legacyPath, []byte(`{"model-one":"legacy-provider-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := New(db, Options{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.loadCredentials()
	if err != nil || credentials["model-one"] != "legacy-provider-secret" {
		t.Fatalf("migrated credentials=%v err=%v", credentials, err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("plaintext credential file remained after migration: %v", err)
	}
	sealed, err := os.ReadFile(filepath.Join(secretsDirectory, "assistant-provider.enc"))
	if err != nil || strings.Contains(string(sealed), "legacy-provider-secret") {
		t.Fatalf("sealed migration output is invalid: err=%v body=%s", err, sealed)
	}
}

func TestNewModelConfigurationRequiresCredential(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	_, err := service.SaveModel(context.Background(), Actor{UserID: "admin-one"}, "", ModelInput{
		Name: "Missing credential", Provider: ProviderOpenAI, Model: "gpt-5.2", Endpoint: "https://api.openai.com/v1",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("save model without credential error = %v, want %v", err, ErrInvalidInput)
	}
}

func TestModelConfigurationAcceptsRemoteHTTPServer(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	model, err := service.SaveModel(context.Background(), Actor{UserID: "admin-one"}, "", ModelInput{
		Name: "LAN model", Provider: ProviderOpenAICompatible, Model: "local-model",
		Endpoint: "http://llm.internal:11434/v1", APIKey: "lan-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if model.Endpoint != "http://llm.internal:11434/v1" {
		t.Fatalf("endpoint=%q", model.Endpoint)
	}
}

func TestConversationWindowBoundsLongPersistentHistory(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "history-owner", Username: "operator"}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Fixture", Provider: ProviderOpenAICompatible, Model: "fixture", Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, actor, ConversationInput{ModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	message := strings.Repeat("x", 32<<10)
	for index := 0; index < 70; index++ {
		turn, turnErr := service.BeginTurn(ctx, actor, conversation.ID, message)
		if turnErr != nil {
			t.Fatal(turnErr)
		}
		if finishErr := service.FinishTurn(ctx, actor, conversation.ID, turn.Assistant.ID, "complete", "fixture"); finishErr != nil {
			t.Fatal(finishErr)
		}
	}

	window, err := service.ConversationWindow(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	bytes := 0
	for _, item := range window.Messages {
		bytes += len(item.Body)
	}
	if !window.Truncated || bytes > maxConversationWindowMessageBytes || len(window.Messages) >= 140 {
		t.Fatalf("window messages=%d bytes=%d truncated=%v", len(window.Messages), bytes, window.Truncated)
	}
	if got := window.Messages[len(window.Messages)-1].Sequence; got != 140 {
		t.Fatalf("latest sequence=%d, want 140", got)
	}
}

func TestModelReasoningDefaultsArePersistedAndInheritedByNewConversations(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "admin-one", Username: "admin"}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Reasoning model", Provider: ProviderOpenAI, Model: "gpt-5.2",
		Endpoint: "https://api.openai.com/v1", APIKey: "sk-secret",
		SupportsReasoning: true, DefaultThinkingLevel: "high",
	})
	if err != nil {
		t.Fatalf("save reasoning model: %v", err)
	}
	if !model.SupportsReasoning || model.DefaultThinkingLevel != "high" {
		t.Fatalf("reasoning model = %+v", model)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatalf("enable assistant: %v", err)
	}
	conversation, err := service.CreateConversation(ctx, actor, ConversationInput{ModelID: model.ID})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if conversation.ThinkingLevel != "high" {
		t.Fatalf("conversation thinking level = %q, want high", conversation.ThinkingLevel)
	}
	if _, err := service.SaveModel(ctx, actor, model.ID, ModelInput{
		Name: model.Name, Provider: model.Provider, Model: model.Model, Endpoint: model.Endpoint,
		SupportsReasoning: true, DefaultThinkingLevel: "unknown",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid default thinking level error = %v, want %v", err, ErrInvalidInput)
	}
}

func TestModelConnectionStatusIsInformationalAndResetsAfterEditing(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "admin-one", Username: "admin"}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Connection status", Provider: ProviderOpenAICompatible, Model: "model-v1",
		Endpoint: "https://example.com/v1", APIKey: "secret", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if model.ConnectionOK {
		t.Fatalf("new model connection status = true, want false: %+v", model)
	}
	if _, err := service.CreateConversation(ctx, actor, ConversationInput{ModelID: model.ID}); err != nil {
		t.Fatalf("model with an unverified connection cannot be selected: %v", err)
	}
	if err := service.SetModelConnectionOK(ctx, model.ID, true); err != nil {
		t.Fatal(err)
	}
	model, err = service.Model(ctx, model.ID)
	if err != nil || !model.ConnectionOK {
		t.Fatalf("tested model = %+v, error = %v", model, err)
	}
	model, err = service.SaveModel(ctx, actor, model.ID, ModelInput{
		Name: "Connection status", Provider: ProviderOpenAICompatible, Model: "model-v2",
		Endpoint: "https://example.com/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if model.ConnectionOK {
		t.Fatalf("edited model retained a stale successful status: %+v", model)
	}
}

func TestModelConfigurationNamesAreUniqueAndReferencedModelsCannotBeDeleted(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "admin-one", Username: "admin"}
	primary, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Operations", Provider: ProviderOpenAI, Model: "gpt-5.2",
		Endpoint: "https://api.openai.com/v1", APIKey: "sk-primary", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Fallback", Provider: ProviderAnthropic, Model: "claude-opus-4-1",
		Endpoint: "https://api.anthropic.com", APIKey: "sk-secondary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "operations", Provider: ProviderOpenAI, Model: "gpt-5.2-mini",
		Endpoint: "https://api.openai.com/v1", APIKey: "sk-duplicate",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate model name error = %v, want %v", err, ErrInvalidInput)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateConversation(ctx, actor, ConversationInput{ModelID: secondary.ID, InitialMessage: "Inspect"}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteModel(ctx, actor, secondary.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("delete referenced model error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := service.Model(ctx, secondary.ID); err != nil {
		t.Fatalf("referenced model was removed after rejection: %v", err)
	}
	if err := service.DeleteModel(ctx, actor, primary.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("delete default model error = %v, want %v", err, ErrInvalidInput)
	}
}

func TestConversationRequiresAnAvailableModelAndScopesEveryLookupToItsOwner(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	admin := Actor{UserID: "admin-one", Username: "admin"}
	other := Actor{UserID: "operator-two", Username: "operator"}

	if _, err := service.CreateConversation(ctx, admin, ConversationInput{}); !errors.Is(err, ErrModelRequired) {
		t.Fatalf("create without model error = %v, want %v", err, ErrModelRequired)
	}
	if _, err := service.CreateConversation(ctx, admin, ConversationInput{ModelID: "missing"}); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("create with missing model error = %v, want %v", err, ErrModelUnavailable)
	}

	model, err := service.SaveModel(ctx, admin, "", ModelInput{
		Name: "OpenAI · Production", Provider: ProviderOpenAI, Model: "gpt-5.2",
		Endpoint: "https://api.openai.com/v1", APIKey: "sk-secret", MakeDefault: true,
	})
	if err != nil {
		t.Fatalf("save model: %v", err)
	}
	if _, err := service.CreateConversation(ctx, admin, ConversationInput{ModelID: model.ID}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("create while assistant is disabled error = %v, want %v", err, ErrDisabled)
	}
	if err := service.UpdateSettings(ctx, admin, SettingsInput{Enabled: true, MaxActiveConversations: 2, DefaultAutoApproval: true}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	conversation, err := service.CreateConversation(ctx, admin, ConversationInput{
		Title: "分析主机资源", ModelID: model.ID,
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if conversation.ModelID != model.ID || !conversation.AutoApproval || conversation.OwnerUserID != admin.UserID {
		t.Fatalf("conversation defaults = %+v", conversation)
	}
	if err := service.UpdateSettings(ctx, admin, SettingsInput{Enabled: false, MaxActiveConversations: 2, DefaultAutoApproval: true}); err != nil {
		t.Fatalf("disable assistant conversations: %v", err)
	}
	if _, err := service.BeginTurn(ctx, admin, conversation.ID, "This message must be rejected."); !errors.Is(err, ErrDisabled) {
		t.Fatalf("send to existing conversation while disabled error = %v, want %v", err, ErrDisabled)
	}
	if err := service.UpdateSettings(ctx, admin, SettingsInput{Enabled: true, MaxActiveConversations: 2, DefaultAutoApproval: true}); err != nil {
		t.Fatalf("re-enable assistant conversations: %v", err)
	}

	if _, err := service.Conversation(ctx, other, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner lookup error = %v, want opaque not found", err)
	}
	if err := service.SetAutoApproval(ctx, other, conversation.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner auto approval update error = %v, want opaque not found", err)
	}
	if err := service.ArchiveConversation(ctx, other, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner archive error = %v, want opaque not found", err)
	}

	if err := service.SetAutoApproval(ctx, admin, conversation.ID, false); err != nil {
		t.Fatalf("disable auto approval: %v", err)
	}
	if err := service.ArchiveConversation(ctx, admin, conversation.ID); err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
	active, err := service.ListConversations(ctx, admin, ConversationFilter{})
	if err != nil {
		t.Fatalf("list active conversations: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active conversations = %+v, want none after archive", active)
	}
	archived, err := service.ListConversations(ctx, admin, ConversationFilter{Archived: true})
	if err != nil {
		t.Fatalf("list archived conversations: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != conversation.ID || archived[0].AutoApproval {
		t.Fatalf("archived conversations = %+v", archived)
	}
}

func TestConversationPersistsCapabilityProfileThinkingAndBoundedTelemetry(t *testing.T) {
	t.Parallel()
	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "owner", Username: "operator"}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Operations", Provider: ProviderOpenAICompatible, Model: "local",
		Endpoint: "http://localhost:11434/v1", APIKey: "key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, actor, ConversationInput{ModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	if conversation.CapabilityProfile != ProfileGeneral || conversation.ThinkingLevel != "medium" {
		t.Fatalf("conversation defaults = %#v", conversation)
	}
	if err := service.SetCapabilityProfile(ctx, actor, conversation.ID, ProfileDiagnoseFailedRun); err != nil {
		t.Fatal(err)
	}
	if err := service.SetThinkingLevel(ctx, actor, conversation.ID, "high"); err != nil {
		t.Fatal(err)
	}
	percent := 62.5
	telemetry := SessionTelemetry{
		UserMessages: 4, AssistantMessages: 4, ToolCalls: 3, ToolResults: 3, TotalMessages: 14,
		InputTokens: 4800, OutputTokens: 700, CacheReadTokens: 2100, CacheWriteTokens: 80, TotalTokens: 7680,
		Cost: 0.42, ContextTokens: 10000, ContextWindow: 16000, ContextPercent: &percent,
	}
	if err := service.UpdateSessionTelemetry(ctx, actor, conversation.ID, telemetry); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Conversation(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CapabilityProfile != ProfileDiagnoseFailedRun || updated.ThinkingLevel != "high" ||
		updated.Telemetry.TotalTokens != 7680 || updated.Telemetry.ContextPercent == nil || *updated.Telemetry.ContextPercent != percent {
		t.Fatalf("updated conversation = %#v", updated)
	}
	if err := service.UpdateSessionTelemetry(ctx, actor, conversation.ID, SessionTelemetry{TotalTokens: -1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative telemetry error = %v", err)
	}
	if err := service.SetCapabilityProfile(ctx, actor, conversation.ID, "arbitrary"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown profile error = %v", err)
	}
}

func TestConversationPersistsInitialMessageAndValidatedContextReferences(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "operator-one", Username: "operator"}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Operations", Provider: ProviderOpenAICompatible, Model: "operations-1",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "local-test-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatalf("save model: %v", err)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatalf("enable assistant: %v", err)
	}

	autoApproval := true
	conversation, err := service.CreateConversation(ctx, actor, ConversationInput{
		Title: "Inspect host pressure", ModelID: model.ID, InitialMessage: "Summarize CPU and memory pressure.",
		AutoApproval: &autoApproval,
		Context: []ContextRef{
			{Kind: "directory", StableID: "host", Label: "host"},
			{Kind: "application", StableID: "api-prod", Label: "API production"},
			{Kind: "directory", StableID: "host", Label: "host"},
		},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if !conversation.AutoApproval {
		t.Fatal("per-conversation auto approval did not override the disabled default")
	}

	references, err := service.ContextReferences(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatalf("list context references: %v", err)
	}
	if len(references) != 2 || references[0].Kind != "directory" || references[1].StableID != "api-prod" {
		t.Fatalf("context references = %+v", references)
	}
	messages, err := service.Messages(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "user" || messages[0].Body != "Summarize CPU and memory pressure." || messages[0].Status != "complete" {
		t.Fatalf("messages = %+v", messages)
	}

	other := Actor{UserID: "viewer-two", Username: "viewer"}
	if _, err := service.ContextReferences(ctx, other, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner context lookup error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.Messages(ctx, other, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner message lookup error = %v, want %v", err, ErrNotFound)
	}
}

func TestBeginTurnWithContextAtomicallyReplacesConversationReferences(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	owner := Actor{UserID: "operator-one", Username: "operator"}
	other := Actor{UserID: "viewer-two", Username: "viewer"}
	model, err := service.SaveModel(ctx, owner, "", ModelInput{
		Name: "Operations", Provider: ProviderOpenAICompatible, Model: "operations-1",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "local-test-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, owner, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, owner, ConversationInput{
		ModelID: model.ID,
		Context: []ContextRef{{Kind: "directory", StableID: "host", Label: "host"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement := []ContextRef{
		{Kind: "file", StableID: "file-1", Label: "service.conf"},
		{Kind: "schedule", StableID: "schedule-1", Label: "Nightly check"},
	}
	if _, err := service.BeginTurnWithContext(ctx, other, conversation.ID, "guess", replacement); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner context turn error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.BeginTurnWithContext(ctx, owner, conversation.ID, "inspect", replacement); err != nil {
		t.Fatal(err)
	}
	references, err := service.ContextReferences(ctx, owner, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 2 || references[0].Kind != "file" || references[1].StableID != "schedule-1" {
		t.Fatalf("replaced context references = %+v", references)
	}
}

func TestConversationRejectsInvalidMessageAndContextReference(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "admin-one", Username: "admin"}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Operations", Provider: ProviderOpenAI, Model: "gpt-5.2",
		Endpoint: "https://api.openai.com/v1", APIKey: "sk-test", MakeDefault: true,
	})
	if err != nil {
		t.Fatalf("save model: %v", err)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatalf("enable assistant: %v", err)
	}

	_, err = service.CreateConversation(ctx, actor, ConversationInput{
		ModelID: model.ID,
		Context: []ContextRef{{Kind: "shell", StableID: "anything", Label: "Anything"}},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid context error = %v, want %v", err, ErrInvalidInput)
	}
	_, err = service.CreateConversation(ctx, actor, ConversationInput{
		ModelID: model.ID, InitialMessage: strings.Repeat("x", maxMessageRunes+1),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized message error = %v, want %v", err, ErrInvalidInput)
	}
}

func TestAgentTurnPersistsStreamingAssistantMessageAndSettlesConversation(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "operator-one", Username: "operator"}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{
		Name: "Operations", Provider: ProviderOpenAI, Model: "gpt-5.2",
		Endpoint: "https://api.openai.com/v1", APIKey: "sk-test", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, actor, ConversationInput{ModelID: model.ID, InitialMessage: "Inspect the host."})
	if err != nil {
		t.Fatal(err)
	}

	reply, err := service.BeginAssistantReply(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Role != "assistant" || reply.Status != "streaming" || reply.Sequence != 2 {
		t.Fatalf("reply = %+v", reply)
	}
	if _, err := service.BeginTurn(ctx, actor, conversation.ID, "concurrent"); !errors.Is(err, ErrConversationBusy) {
		t.Fatalf("concurrent turn error = %v", err)
	}
	if err := service.AppendAssistantText(ctx, actor, conversation.ID, reply.ID, "CPU "); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendAssistantText(ctx, actor, conversation.ID, reply.ID, "is stable."); err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTurn(ctx, actor, conversation.ID, reply.ID, "complete", "0.83.0"); err != nil {
		t.Fatal(err)
	}

	stored, err := service.Messages(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[1].Body != "CPU is stable." || stored[1].Status != "complete" || stored[1].FinishedAt == nil {
		t.Fatalf("stored messages = %+v", stored)
	}
	settled, err := service.Conversation(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != "idle" || settled.Revision <= conversation.Revision {
		t.Fatalf("settled conversation = %+v", settled)
	}
}

func TestBeginTurnIsOwnerScopedAndCanResumeAfterFailure(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	owner := Actor{UserID: "owner"}
	other := Actor{UserID: "other"}
	model, err := service.SaveModel(ctx, owner, "", ModelInput{Name: "Local", Provider: ProviderOpenAICompatible, Model: "local", Endpoint: "http://127.0.0.1:11434/v1", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := service.SaveModel(ctx, owner, "", ModelInput{Name: "Fallback", Provider: ProviderOpenAICompatible, Model: "fallback", Endpoint: "http://127.0.0.1:11434/v1", APIKey: "key-two"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, owner, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, owner, ConversationInput{ModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginTurn(ctx, other, conversation.ID, "guess"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner turn error = %v", err)
	}
	turn, err := service.BeginTurn(ctx, owner, conversation.ID, "try once")
	if err != nil {
		t.Fatal(err)
	}
	if turn.User.Role != "user" || turn.Assistant.Role != "assistant" || turn.Assistant.Sequence != turn.User.Sequence+1 {
		t.Fatalf("turn = %+v", turn)
	}
	if err := service.FinishTurn(ctx, owner, conversation.ID, turn.Assistant.ID, "error", "0.83.0"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetConversationModel(ctx, owner, conversation.ID, fallback.ID); err != nil {
		t.Fatalf("select fallback after failure: %v", err)
	}
	if _, err := service.BeginTurn(ctx, owner, conversation.ID, "try again"); err != nil {
		t.Fatalf("resume after failure: %v", err)
	}
}

func TestRecoverInterruptedTurnsDoesNotReplayUnfinishedWork(t *testing.T) {
	t.Parallel()
	service, _, _ := newTestService(t)
	ctx := context.Background()
	actor := Actor{UserID: "owner"}
	model, err := service.SaveModel(ctx, actor, "", ModelInput{Name: "Local", Provider: ProviderOpenAICompatible, Model: "local", Endpoint: "http://localhost:11434/v1", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, actor, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, actor, ConversationInput{ModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.BeginTurn(ctx, actor, conversation.ID, "unfinished")
	if err != nil {
		t.Fatal(err)
	}
	call, err := service.StartToolCall(ctx, actor, conversation.ID, turn.Assistant.ID, "recovery-call", ToolCallInput{Name: "check_website_now", TargetSummary: "site-1 website", ParameterSummary: "check now"})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := service.RequestApproval(ctx, actor, conversation.ID, "recovery-call", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideApproval(ctx, actor, conversation.ID, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	if recovered, err := service.RecoverInterruptedTurns(ctx); err != nil || recovered != 1 {
		t.Fatalf("recovered = %d, error = %v", recovered, err)
	}
	messages, err := service.Messages(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages[len(messages)-1].ID != turn.Assistant.ID || messages[len(messages)-1].Status != "interrupted" {
		t.Fatalf("messages = %+v", messages)
	}
	recoveredConversation, err := service.Conversation(ctx, actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredConversation.Status != "interrupted" {
		t.Fatalf("conversation = %+v", recoveredConversation)
	}
	recoveredApproval, err := service.Approval(ctx, actor, conversation.ID, approval.ID)
	if err != nil || recoveredApproval.Status != "cancelled" {
		t.Fatalf("approval = %#v error = %v", recoveredApproval, err)
	}
	recoveredCall, err := service.ToolCallByID(ctx, actor, conversation.ID, call.ID)
	if err != nil || recoveredCall.Status != "interrupted" || recoveredCall.ErrorCode != "service_restarted" {
		t.Fatalf("tool call = %#v error = %v", recoveredCall, err)
	}
}

func TestToolApprovalIsConversationScopedParameterBoundAndSingleUse(t *testing.T) {
	t.Parallel()
	service, _, _ := newTestService(t)
	ctx := context.Background()
	owner := Actor{UserID: "owner"}
	other := Actor{UserID: "other"}
	model, err := service.SaveModel(ctx, owner, "", ModelInput{Name: "Local", Provider: ProviderOpenAICompatible, Model: "local", Endpoint: "http://localhost:11434/v1", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, owner, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, owner, ConversationInput{ModelID: model.ID, AutoApproval: boolPointer(false)})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.BeginTurn(ctx, owner, conversation.ID, "start the quick run")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AppendAssistantText(ctx, owner, conversation.ID, turn.Assistant.ID, "Before "); err != nil {
		t.Fatal(err)
	}
	call, err := service.StartToolCall(ctx, owner, conversation.ID, turn.Assistant.ID, "pi-call-1", ToolCallInput{
		Name: "start_quick_run", TargetSummary: "Nightly cleanup", ParameterSummary: "quick_run=quick-1",
		RequestJSON: "{\n  \"tool\": \"start_quick_run\",\n  \"parameters\": {\n    \"id\": \"quick-1\"\n  }\n}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if call.BodyOffset != 7 {
		t.Fatalf("tool-call body offset = %d, want 7", call.BodyOffset)
	}
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	approval, err := service.RequestApproval(ctx, owner, conversation.ID, "pi-call-1", digestA)
	if err != nil {
		t.Fatal(err)
	}
	if approval.ToolCallID != call.ID || approval.Status != "pending" {
		t.Fatalf("approval = %#v call = %#v", approval, call)
	}
	if _, err := service.DecideApproval(ctx, other, conversation.ID, approval.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner approval error = %v", err)
	}
	if _, err := service.DecideApproval(ctx, owner, conversation.ID, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := service.ConsumeApproval(ctx, owner, conversation.ID, "pi-call-1", approval.ID, digestB); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("changed digest consume error = %v", err)
	}
	if err := service.ConsumeApproval(ctx, owner, conversation.ID, "pi-call-1", approval.ID, digestA); err != nil {
		t.Fatal(err)
	}
	if err := service.ConsumeApproval(ctx, owner, conversation.ID, "pi-call-1", approval.ID, digestA); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("replayed approval error = %v", err)
	}
	if err := service.FinishToolCall(ctx, owner, conversation.ID, "pi-call-1", "complete", "", "Run accepted"); err != nil {
		t.Fatal(err)
	}
	responseJSON := "{\n  \"status\": \"success\",\n  \"content\": {\n    \"accepted\": true\n  }\n}"
	if _, err := service.RecordToolCallResponse(ctx, owner, conversation.ID, "pi-call-1", responseJSON); err != nil {
		t.Fatal(err)
	}
	calls, err := service.ToolCalls(ctx, owner, conversation.ID)
	if err != nil || len(calls) != 1 || calls[0].Status != "complete" || calls[0].ResultSummary != "Run accepted" || calls[0].RequestJSON != call.RequestJSON || calls[0].ResponseJSON != responseJSON {
		t.Fatalf("tool calls = %#v, error = %v", calls, err)
	}
}

func TestApprovalDecisionRollsBackWhenToolCallTransitionFails(t *testing.T) {
	t.Parallel()

	service, db, _ := newTestService(t)
	ctx := context.Background()
	owner := Actor{UserID: "owner"}
	model, err := service.SaveModel(ctx, owner, "", ModelInput{
		Name: "Local", Provider: ProviderOpenAICompatible, Model: "local",
		Endpoint: "http://localhost:11434/v1", APIKey: "key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateSettings(ctx, owner, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.CreateConversation(ctx, owner, ConversationInput{ModelID: model.ID, AutoApproval: boolPointer(false)})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.BeginTurn(ctx, owner, conversation.ID, "start the quick run")
	if err != nil {
		t.Fatal(err)
	}
	call, err := service.StartToolCall(ctx, owner, conversation.ID, turn.Assistant.ID, "pi-call-rollback", ToolCallInput{
		Name: "start_quick_run", TargetSummary: "Nightly cleanup", ParameterSummary: "quick_run=quick-1",
		RequestJSON: `{"tool":"start_quick_run","parameters":{"id":"quick-1"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := service.RequestApproval(ctx, owner, conversation.ID, "pi-call-rollback", strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_tool_call_rejection
		BEFORE UPDATE OF status ON assistant_tool_calls
		WHEN NEW.status = 'rejected'
		BEGIN SELECT RAISE(FAIL, 'tool call store unavailable'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if _, err := service.DecideApproval(ctx, owner, conversation.ID, approval.ID, false); err == nil {
		t.Fatal("approval rejection succeeded after tool-call transition failed")
	}
	reloadedApproval, err := service.Approval(ctx, owner, conversation.ID, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedApproval.Status != "pending" {
		t.Fatalf("approval status = %q, want pending after rollback", reloadedApproval.Status)
	}
	reloadedCall, err := service.ToolCallByID(ctx, owner, conversation.ID, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedCall.Status != "waiting_approval" {
		t.Fatalf("tool call status = %q, want waiting_approval after rollback", reloadedCall.Status)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestModelConfigurationsArePrivateUnlessExplicitlyShared(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()
	owner := Actor{UserID: "owner", Username: "owner"}
	other := Actor{UserID: "other", Username: "other"}
	if err := service.UpdateSettings(ctx, owner, SettingsInput{Enabled: true, MaxActiveConversations: 2}); err != nil {
		t.Fatal(err)
	}
	privateModel, err := service.SaveModel(ctx, owner, "", ModelInput{
		Name: "Owner private", Provider: ProviderOpenAICompatible, Model: "private",
		Endpoint: "https://example.com/v1", APIKey: "private-key", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedModel, err := service.SaveModel(ctx, owner, "", ModelInput{
		Name: "Owner shared", Provider: ProviderOpenAICompatible, Model: "shared",
		Endpoint: "https://example.com/v1", APIKey: "shared-key", Shared: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ownerModels, err := service.ListModels(ctx, owner)
	if err != nil || len(ownerModels) != 2 || !ownerModels[0].Owned {
		t.Fatalf("owner models = %#v, error = %v", ownerModels, err)
	}
	otherModels, err := service.ListModels(ctx, other)
	if err != nil || len(otherModels) != 1 || otherModels[0].ID != sharedModel.ID || otherModels[0].Owned || !otherModels[0].Shared {
		t.Fatalf("other models = %#v, error = %v", otherModels, err)
	}
	if _, err := service.CreateConversation(ctx, other, ConversationInput{ModelID: privateModel.ID}); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("create conversation with another user's private model error = %v", err)
	}
	if _, err := service.CreateConversation(ctx, other, ConversationInput{ModelID: sharedModel.ID}); err != nil {
		t.Fatalf("create conversation with shared model: %v", err)
	}
	if _, err := service.SaveModel(ctx, other, privateModel.ID, ModelInput{
		Name: "Stolen", Provider: ProviderOpenAICompatible, Model: "private", Endpoint: "https://example.com/v1",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner update error = %v", err)
	}
	if err := service.SetDefaultModel(ctx, other, sharedModel.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner default error = %v", err)
	}
	if err := service.DeleteModel(ctx, other, sharedModel.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner delete error = %v", err)
	}
}

func newTestService(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	stateRoot := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(stateRoot, "assistant-test.db"))+"?cache=shared")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("initialize assistant schema: %v\n%s", err, statement)
		}
	}
	clock := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	service, err := New(db, Options{StateRoot: stateRoot, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatalf("open assistant service: %v", err)
	}
	return service, db, stateRoot
}
