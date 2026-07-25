package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"testing"
)

func TestAIProfileDiagnosticUsesAutomaticToolChoice(t *testing.T) {
	requestCount := 0
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount++
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
		}
		if requestCount == 1 && body["tool_choice"] != "auto" {
			t.Errorf("tool_choice = %#v, want %q", body["tool_choice"], "auto")
		}
		response.Header().Set("Content-Type", "text/event-stream")
		if requestCount == 1 {
			_, _ = fmt.Fprint(response, `data: {"id":"probe-1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_probe","type":"function","function":{"name":"scriptboard_probe","arguments":"{}"}}]},"finish_reason":null}]}

data: {"id":"probe-1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`)
			return
		}
		_, _ = fmt.Fprint(response, `data: {"id":"probe-2","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}

data: [DONE]

`)
	}))
	defer modelServer.Close()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/settings/ai")
	if err != nil {
		t.Fatal(err)
	}
	settingsPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	response, err = client.PostForm(serverURL+"/settings/ai/profiles", url.Values{
		"csrf_token":                  {formToken(t, settingsPage)},
		"name":                        {"Thinking-compatible diagnostic"},
		"protocol":                    {"openai_chat"},
		"base_url":                    {modelServer.URL},
		"model":                       {"model"},
		"context_window":              {"128000"},
		"max_output_tokens":           {"64"},
		"default_run_timeout_seconds": {"300"},
		"risk_confirmed":              {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create profile status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}

	response, err = client.Get(serverURL + "/settings/ai")
	if err != nil {
		t.Fatal(err)
	}
	updatedPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	actionMatch := regexp.MustCompile(`action="(/settings/ai/profiles/[^"]+/test)"`).FindSubmatch(updatedPage)
	if len(actionMatch) != 2 {
		t.Fatal("diagnostic action not found in settings page")
	}

	response, err = client.PostForm(serverURL+string(actionMatch[1]), url.Values{
		"csrf_token": {formToken(t, updatedPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("diagnostic status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	if requestCount != 2 {
		t.Fatalf("model request count = %d, want 2", requestCount)
	}
}
