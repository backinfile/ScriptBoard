package providerproxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionHidesCredentialAndForwardsOnlyBoundModel(t *testing.T) {
	var receivedAuthorization, receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		receivedAuthorization = request.Header.Get("Authorization")
		body, _ := io.ReadAll(request.Body)
		receivedBody = string(body)
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("data: fixture\n\n"))
	}))
	defer upstream.Close()

	session, err := Start(Config{
		Provider: "openai-compatible", Model: "fixture-model", Endpoint: upstream.URL + "/v1", Credential: "real-provider-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if strings.Contains(session.Endpoint(), "real-provider-secret") || session.Capability() == "real-provider-secret" {
		t.Fatal("provider credential leaked through the runtime-facing proxy contract")
	}

	request, err := http.NewRequest(http.MethodPost, session.Endpoint()+"/chat/completions", bytes.NewBufferString(`{"model":"fixture-model","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+session.Capability())
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if receivedAuthorization != "Bearer real-provider-secret" || !strings.Contains(receivedBody, `"model":"fixture-model"`) {
		t.Fatalf("authorization = %q, body = %q", receivedAuthorization, receivedBody)
	}
}

func TestSessionRejectsWrongCapabilityModelMethodAndPath(t *testing.T) {
	var upstreamRequests int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { upstreamRequests++ }))
	defer upstream.Close()
	session, err := Start(Config{
		Provider: "openai-compatible", Model: "allowed", Endpoint: upstream.URL + "/v1", Credential: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	tests := []struct {
		name, method, path, capability, body string
		want                                 int
	}{
		{name: "capability", method: http.MethodPost, path: "/chat/completions", capability: "wrong", body: `{"model":"allowed"}`, want: http.StatusUnauthorized},
		{name: "model", method: http.MethodPost, path: "/chat/completions", capability: session.Capability(), body: `{"model":"other"}`, want: http.StatusForbidden},
		{name: "method", method: http.MethodGet, path: "/chat/completions", capability: session.Capability(), body: `{"model":"allowed"}`, want: http.StatusMethodNotAllowed},
		{name: "path", method: http.MethodPost, path: "/files", capability: session.Capability(), body: `{"model":"allowed"}`, want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(test.method, session.Endpoint()+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+test.capability)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.want)
			}
		})
	}
	if upstreamRequests != 0 {
		t.Fatalf("rejected requests reached upstream: %d", upstreamRequests)
	}
}

func TestSessionRejectsDuplicateCapabilityHeadersBeforeUpstream(t *testing.T) {
	var upstreamRequests int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { upstreamRequests++ }))
	defer upstream.Close()
	session, err := Start(Config{
		Provider: "openai-compatible", Model: "allowed", Endpoint: upstream.URL + "/v1", Credential: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	request, err := http.NewRequest(http.MethodPost, session.Endpoint()+"/chat/completions", strings.NewReader(`{"model":"allowed"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Add("Authorization", "Bearer "+session.Capability())
	request.Header.Add("Authorization", "Bearer "+session.Capability())
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("duplicate provider capability status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if upstreamRequests != 0 {
		t.Fatalf("duplicate provider capability reached upstream: %d", upstreamRequests)
	}
}

func TestSessionUsesAnthropicCapabilityAndCredentialHeaders(t *testing.T) {
	var apiKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		apiKey = request.Header.Get("X-Api-Key")
		response.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	session, err := Start(Config{
		Provider: "anthropic", Model: "claude-fixture", Endpoint: upstream.URL, Credential: "anthropic-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	request, _ := http.NewRequest(http.MethodPost, session.Endpoint()+"/v1/messages", strings.NewReader(`{"model":"claude-fixture"}`))
	request.Header.Set("X-Api-Key", session.Capability())
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || apiKey != "anthropic-secret" {
		t.Fatalf("status = %d, upstream key = %q", response.StatusCode, apiKey)
	}
}

func TestSessionCloseRevokesCapability(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()
	session, err := Start(Config{
		Provider: "openai", Model: "gpt-fixture", Endpoint: upstream.URL + "/v1", Credential: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := session.Endpoint()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, endpoint+"/responses", strings.NewReader(`{"model":"gpt-fixture"}`))
	request.Header.Set("Authorization", "Bearer "+session.Capability())
	if _, err := http.DefaultClient.Do(request); err == nil {
		t.Fatal("closed provider capability remained reachable")
	}
}
