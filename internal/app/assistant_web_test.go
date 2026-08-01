package app_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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
		`data-resource-kind="file"`, `data-resource-label="service.conf"`,
	} {
		if !strings.Contains(string(workspace), expected) {
			t.Fatalf("AI workspace is missing %q: %s", expected, workspace)
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
		`name="default_auto_approval"`, `name="max_active_conversations"`,
		`action="/settings/ai/runtime/check"`,
	} {
		if !strings.Contains(string(settings), expected) {
			t.Fatalf("AI settings are missing %q: %s", expected, settings)
		}
	}

	response, err = client.PostForm(serverURL+"/settings/ai/llms", url.Values{
		"name": {"OpenAI · Production"}, "provider": {"openai"}, "model": {"gpt-5.2"},
		"endpoint": {"https://api.openai.com/v1"}, "api_key": {"sk-never-render-this"},
		"make_default": {"true"}, "csrf_token": {formToken(t, settings)},
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
	for _, expected := range []string{"分析当前主机资源", "OpenAI · Production", "Summarize the current host pressure.", `data-context-key="directory:host"`, `data-auto-approval-toggle aria-pressed="false"`} {
		if !strings.Contains(string(conversation), expected) {
			t.Fatalf("conversation is missing %q: %s", expected, conversation)
		}
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

	secretBody, err := os.ReadFile(filepath.Join(root, "state", "secrets", "assistant-provider.json"))
	if err != nil {
		t.Fatalf("read assistant provider secret file: %v", err)
	}
	if !strings.Contains(string(secretBody), "sk-never-render-this") {
		t.Fatalf("provider credential was not persisted outside SQLite: %s", secretBody)
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
	for _, path := range []string{"/settings/ai/runtime/check", "/settings/ai/runtime/install", "/settings/ai/runtime/rollback"} {
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
