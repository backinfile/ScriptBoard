package web_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAssistantResourceSearchFindsDeepHostFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	deepDirectory := filepath.Join(hostRoot, "tmp", "scriptboard-ai-files-20260803")
	if err := os.MkdirAll(deepDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	deepFile := filepath.Join(deepDirectory, "keep-renamed.sh")
	if err := os.WriteFile(deepFile, []byte("printf 'deep reference ok\\n'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(serverURL + "/ai/resources?query=" + url.QueryEscape(deepFile))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("deep resource search status=%d body=%s", response.StatusCode, body)
	}
	var payload struct {
		Resources []struct {
			Kind, ID, Label string
		}
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Resources) != 1 || payload.Resources[0].Kind != "file" || payload.Resources[0].Label != "keep-renamed.sh" || payload.Resources[0].ID == "" {
		t.Fatalf("deep resource search = %#v", payload.Resources)
	}
}

func TestAIWorkspaceAndSettingsUsePersistedLLMAndConversationState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "service.conf"), []byte("listen=8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))

	response, err := client.Get(serverURL + "/ai")
	if err != nil {
		t.Fatalf("get AI workspace: %v", err)
	}
	workspace, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read AI workspace: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("AI workspace status = %d: %s", response.StatusCode, workspace)
	}
	for _, expected := range []string{
		`data-assistant-workspace`, `href="/ai" aria-current="page"`, `data-conversation-rail`,
		`data-model-picker`, `aria-required="true"`, `data-auto-approval-toggle`, `data-resource-picker`,
		`class="assistant-composer__toolbar"`, `class="assistant-reference-button"`,
		`data-assistant-approval-label`, `data-manual-label="Manual approval"`,
		`data-resource-kind="file"`, `data-resource-label="service.conf"`,
		`class="assistant-body" data-inspector-open="false"`, `data-assistant-inspector-toggle title="Conversation information" aria-label="Conversation information" aria-expanded="false"`,
	} {
		if !strings.Contains(string(workspace), expected) {
			t.Fatalf("AI workspace is missing %q: %s", expected, workspace)
		}
	}
	for _, obsolete := range []string{`class="assistant-context-bar"`, `class="assistant-switch"`, `>Context details<`, `>Provider reported<`, `data-assistant-rail-close`} {
		if strings.Contains(string(workspace), obsolete) {
			t.Fatalf("AI workspace still contains obsolete composer markup %q: %s", obsolete, workspace)
		}
	}

	response, err = client.Get(serverURL + "/settings/ai")
	if err != nil {
		t.Fatalf("get AI settings: %v", err)
	}
	settings, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read AI settings: %v", err)
	}
	for _, expected := range []string{
		`href="/settings/ai" aria-current="page"`, `data-assistant-settings`, `data-llm-drawer`,
		`data-open-guardrails`, `data-guardrail-drawer`, `class="assistant-guardrail-summary"`,
		`name="default_auto_approval"`, `name="max_active_conversations"`,
		`>Enable AI conversations<`, `new conversations cannot be created and messages cannot be sent in existing conversations`,
		`name="shared"`, `name="supports_reasoning"`, `name="default_thinking_level"`,
		`HTTP and HTTPS endpoints are supported. HTTP sends credentials and prompts without transport encryption.`,
		`action="/settings/ai/runtime/check"`,
		`action="/settings/ai/runtime/offline"`, `enctype="multipart/form-data"`,
		`name="runtime_manifest"`, `name="runtime_signature"`, `name="runtime_archive"`,
	} {
		if !strings.Contains(string(settings), expected) {
			t.Fatalf("AI settings are missing %q: %s", expected, settings)
		}
	}
	for _, obsolete := range []string{`01 / LLM`, `02 / RUNTIME`, `03 / GUARDRAILS`} {
		if strings.Contains(string(settings), obsolete) {
			t.Fatalf("AI settings still contain obsolete section index %q: %s", obsolete, settings)
		}
	}

	response, err = client.PostForm(serverURL+"/settings/ai/llms", url.Values{
		"name": {"OpenAI · Production"}, "provider": {"openai"}, "model": {"gpt-5.2"},
		"endpoint": {"https://api.openai.com/v1"}, "api_key": {"sk-never-render-this"},
		"make_default": {"true"}, "supports_reasoning": {"true"}, "default_thinking_level": {"high"},
		"shared": {"true"}, "csrf_token": {formToken(t, settings)},
	})
	if err != nil {
		t.Fatalf("create LLM configuration: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/settings/ai" {
		t.Fatalf("create LLM status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/settings/ai")
	if err != nil {
		t.Fatalf("reload AI settings: %v", err)
	}
	settings, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read reloaded AI settings: %v", err)
	}
	if !strings.Contains(string(settings), "OpenAI · Production") || !strings.Contains(string(settings), `data-edit-llm`) {
		t.Fatalf("configured LLM is not editable: %s", settings)
	}
	if !strings.Contains(string(settings), `data-shared="true"`) || !strings.Contains(string(settings), `data-owned="true"`) {
		t.Fatalf("configured LLM does not preserve owned/shared state: %s", settings)
	}
	if !strings.Contains(string(settings), `data-supports-reasoning="true"`) || !strings.Contains(string(settings), `data-default-thinking-level="high"`) {
		t.Fatalf("configured LLM does not preserve reasoning defaults: %s", settings)
	}
	for _, expected := range []string{`data-connection-ok="false"`, `data-state="not-ok"`, `data-server-error-retry`, `class="button button--compact assistant-llm-test"`, `data-connection-test`, `data-test-kind="llm"`, `>Test not passed<`, `>Test connection<`} {
		if !strings.Contains(string(settings), expected) {
			t.Fatalf("configured LLM is missing connection status or the labeled test action %q: %s", expected, settings)
		}
	}
	if strings.Contains(string(settings), "sk-never-render-this") {
		t.Fatal("provider credential was reflected into settings HTML")
	}
	modelMatch := regexp.MustCompile(`data-llm-id="([^"]+)"`).FindSubmatch(settings)
	if len(modelMatch) != 2 {
		t.Fatalf("configured model ID not found: %s", settings)
	}
	modelID := string(modelMatch[1])

	response, err = client.PostForm(serverURL+"/ai/conversations", url.Values{
		"title": {"Disabled assistant"}, "model_id": {modelID}, "message": {"This must not be stored."},
		"csrf_token": {formToken(t, workspace)},
	})
	if err != nil {
		t.Fatalf("create AI conversation while disabled: %v", err)
	}
	disabledBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("disabled conversation status=%d body=%s", response.StatusCode, disabledBody)
	}

	response, err = client.PostForm(serverURL+"/settings/ai/defaults", url.Values{
		"enabled": {"true"}, "default_auto_approval": {"true"}, "max_active_conversations": {"2"},
		"csrf_token": {formToken(t, settings)},
	})
	if err != nil {
		t.Fatalf("save AI defaults: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("save AI defaults status = %d", response.StatusCode)
	}

	response, err = client.PostForm(serverURL+"/ai/conversations", url.Values{
		"title": {"分析当前主机资源"}, "model_id": {modelID},
		"message": {"Summarize the current host pressure."}, "context_kind": {"directory"}, "context_id": {"host"},
		"auto_approval": {"false"},
		"csrf_token":    {formToken(t, workspace)},
	})
	if err != nil {
		t.Fatalf("create AI conversation: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), "/ai/conversations/") {
		t.Fatalf("create conversation status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	conversationPath := response.Header.Get("Location")

	response, err = client.Get(serverURL + conversationPath)
	if err != nil {
		t.Fatalf("get AI conversation: %v", err)
	}
	conversation, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read AI conversation: %v", err)
	}
	for _, expected := range []string{"分析当前主机资源", "OpenAI · Production", "Summarize the current host pressure.", `data-context-key="directory:host"`, `data-auto-approval-toggle aria-pressed="false"`, `data-assistant-abort hidden`, `data-assistant-conversation-status`, `class="assistant-body" data-inspector-open="true"`, `data-reference-kind="host_overview"`, `data-reference-kind="directory"`, `>Referenced content<`, `>Modification operations<`} {
		if !strings.Contains(string(conversation), expected) {
			t.Fatalf("conversation is missing %q: %s", expected, conversation)
		}
	}
	inspectorStart := strings.Index(string(conversation), `<aside class="assistant-inspector"`)
	if inspectorStart < 0 {
		t.Fatalf("conversation inspector is missing: %s", conversation)
	}
	inspectorEnd := strings.Index(string(conversation)[inspectorStart:], `</aside>`)
	if inspectorEnd < 0 {
		t.Fatalf("conversation inspector is not closed: %s", conversation)
	}
	inspectorMarkup := string(conversation)[inspectorStart : inspectorStart+inspectorEnd]
	if strings.Contains(inspectorMarkup, `data-remove-resource`) || strings.Contains(inspectorMarkup, `action="`) {
		t.Fatalf("referenced-content preview exposes an action: %s", inspectorMarkup)
	}

	response, err = client.PostForm(serverURL+"/settings/ai/defaults", url.Values{
		"default_auto_approval": {"true"}, "max_active_conversations": {"2"},
		"csrf_token": {formToken(t, conversation)},
	})
	if err != nil {
		t.Fatalf("disable AI conversations: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable AI conversations status = %d", response.StatusCode)
	}

	response, err = client.Get(serverURL + conversationPath)
	if err != nil {
		t.Fatalf("reload disabled AI conversation: %v", err)
	}
	disabledConversation, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read disabled AI conversation: %v", err)
	}
	for _, expected := range []string{
		`class="assistant-new-chat" aria-disabled="true"`,
		`data-assistant-input disabled`,
		`AI conversations are disabled. New conversations and messages are unavailable.`,
	} {
		if !strings.Contains(string(disabledConversation), expected) {
			t.Fatalf("disabled AI conversation is missing %q: %s", expected, disabledConversation)
		}
	}

	response, err = client.PostForm(serverURL+conversationPath+"/messages", url.Values{
		"message": {"This must not be sent."}, "csrf_token": {formToken(t, disabledConversation)},
	})
	if err != nil {
		t.Fatalf("post message while AI conversations are disabled: %v", err)
	}
	disabledMessageBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(disabledMessageBody), "AI assistant is currently disabled") {
		t.Fatalf("disabled existing conversation status=%d body=%s", response.StatusCode, disabledMessageBody)
	}

	response, err = client.PostForm(serverURL+"/settings/ai/defaults", url.Values{
		"enabled": {"true"}, "default_auto_approval": {"true"}, "max_active_conversations": {"2"},
		"csrf_token": {formToken(t, disabledConversation)},
	})
	if err != nil {
		t.Fatalf("re-enable AI conversations: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("re-enable AI conversations status = %d", response.StatusCode)
	}

	response, err = client.PostForm(serverURL+conversationPath+"/messages", url.Values{
		"message": {"This cannot run without the private runtime."}, "csrf_token": {formToken(t, conversation)},
	})
	if err != nil {
		t.Fatalf("post message without runtime: %v", err)
	}
	runtimeBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(runtimeBody), "Not installed") {
		t.Fatalf("message without runtime status=%d body=%s", response.StatusCode, runtimeBody)
	}

	eventsContext, cancelEvents := context.WithCancel(context.Background())
	eventsRequest, err := http.NewRequestWithContext(eventsContext, http.MethodGet, serverURL+conversationPath+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsResponse, err := client.Do(eventsRequest)
	if err != nil {
		t.Fatalf("open assistant events: %v", err)
	}
	reader := bufio.NewReader(eventsResponse.Body)
	var eventBlock strings.Builder
	for !strings.Contains(eventBlock.String(), "\n\n") {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read assistant snapshot: %v", readErr)
		}
		eventBlock.WriteString(line)
	}
	cancelEvents()
	_ = eventsResponse.Body.Close()
	if eventsResponse.StatusCode != http.StatusOK || !strings.Contains(eventBlock.String(), "event: snapshot") || !strings.Contains(eventBlock.String(), "Summarize the current host pressure.") {
		t.Fatalf("assistant snapshot status=%d event=%s", eventsResponse.StatusCode, eventBlock.String())
	}

	response, err = client.PostForm(serverURL+conversationPath+"/approval-mode", url.Values{
		"auto_approval": {"true"}, "csrf_token": {formToken(t, conversation)},
	})
	if err != nil {
		t.Fatalf("toggle conversation approval mode: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != conversationPath {
		t.Fatalf("toggle approval status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	secretBody, err := os.ReadFile(filepath.Join(root, "state", "secrets", "assistant-provider.enc"))
	if err != nil {
		t.Fatalf("read assistant provider secret file: %v", err)
	}
	if strings.Contains(string(secretBody), "sk-never-render-this") {
		t.Fatalf("sealed provider credential file contains plaintext: %s", secretBody)
	}
}

func TestAIStateChangingRoutesRequireCSRF(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.PostForm(serverURL+"/settings/ai/llms", url.Values{
		"name": {"Unsafe"}, "provider": {"openai"}, "model": {"gpt-5.2"},
		"endpoint": {"https://api.openai.com/v1"}, "api_key": {"secret"},
	})
	if err != nil {
		t.Fatalf("post LLM without CSRF: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("LLM create without CSRF status=%d body=%s", response.StatusCode, body)
	}
	for _, path := range []string{"/settings/ai/runtime/check", "/settings/ai/runtime/install", "/settings/ai/runtime/offline", "/settings/ai/runtime/rollback"} {
		response, err = client.PostForm(serverURL+path, url.Values{})
		if err != nil {
			t.Fatalf("post %s without CSRF: %v", path, err)
		}
		body, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s without CSRF status=%d body=%s", path, response.StatusCode, body)
		}
	}
}

func TestAssistantRuntimeOfflineUploadRejectsUntrustedPackage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/ai")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf_token", formToken(t, settings)); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct {
		field string
		name  string
		body  []byte
	}{
		{field: "runtime_manifest", name: "ASSISTANT-RUNTIME.json", body: []byte(`{"invalid":true}`)},
		{field: "runtime_signature", name: "ASSISTANT-RUNTIME.json.sig", body: []byte(`{"invalid":true}`)},
		{field: "runtime_archive", name: "scriptboard-pi-runtime.zip", body: []byte("not a trusted runtime")},
	} {
		part, createErr := writer.CreateFormFile(file.field, file.name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := part.Write(file.body); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, serverURL+"/settings/ai/runtime/offline", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(string(responseBody), "Runtime failed signature or archive validation") {
		t.Fatalf("offline upload status=%d body=%s", response.StatusCode, responseBody)
	}
}

func TestAIWorkspaceIsAvailableToEveryAuthenticatedRoleButSettingsRequireSystemManagement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	viewer := createRoleUserClient(t, admin, serverURL, "assistant-viewer", "viewer")
	operator := createRoleUserClient(t, admin, serverURL, "assistant-operator", "operator")
	maintainer := createRoleUserClient(t, admin, serverURL, "assistant-maintainer", "maintainer")

	for role, client := range map[string]*http.Client{"viewer": viewer, "operator": operator, "maintainer": maintainer} {
		response, err := client.Get(serverURL + "/ai")
		if err != nil {
			t.Fatalf("get AI workspace as %s: %v", role, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `data-assistant-workspace`) {
			t.Fatalf("AI workspace as %s status=%d body=%s", role, response.StatusCode, body)
		}
	}

	for role, client := range map[string]*http.Client{"viewer": viewer, "operator": operator} {
		response, err := client.Get(serverURL + "/settings/ai")
		if err != nil {
			t.Fatalf("get AI settings as %s: %v", role, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("AI settings as %s status=%d, want 403", role, response.StatusCode)
		}
	}
	response, err := maintainer.Get(serverURL + "/settings/ai")
	if err != nil {
		t.Fatalf("get AI settings as maintainer: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("AI settings as maintainer status=%d, want 200", response.StatusCode)
	}
}
