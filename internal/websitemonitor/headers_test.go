package websitemonitor

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRequestHeadersNormalizesNamesAndPreservesValues(t *testing.T) {
	headers, err := ParseRequestHeaders("authorization: Bearer secret\nX-Tenant: north:1\n\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []RequestHeader{{Name: "Authorization", Value: "Bearer secret"}, {Name: "X-Tenant", Value: "north:1"}}
	if len(headers) != len(want) {
		t.Fatalf("headers = %#v", headers)
	}
	for index := range want {
		if headers[index] != want[index] {
			t.Fatalf("headers[%d] = %#v, want %#v", index, headers[index], want[index])
		}
	}
	if formatted := FormatRequestHeaders(headers); formatted != "Authorization: Bearer secret\nX-Tenant: north:1" {
		t.Fatalf("formatted = %q", formatted)
	}
}

func TestNormalizeConfigRejectsInvalidOrReservedRequestHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers []RequestHeader
	}{
		{name: "invalid name", headers: []RequestHeader{{Name: "Bad Header", Value: "value"}}},
		{name: "newline", headers: []RequestHeader{{Name: "X-Test", Value: "one\r\ntwo"}}},
		{name: "control character", headers: []RequestHeader{{Name: "X-Test", Value: "one\x00two"}}},
		{name: "reserved", headers: []RequestHeader{{Name: "Content-Length", Value: "10"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeConfig(Config{Name: "headers", Kind: KindHTTP, URL: "https://example.com", RequestHeaders: test.headers})
			if err == nil || !strings.Contains(err.Error(), "请求头") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestManagerPersistsRequestHeaders(t *testing.T) {
	manager := newTestManager(t, Options{Probe: probeFunc(func(context.Context, Config) Evidence {
		return Evidence{Success: true}
	})})
	created, err := manager.Create(context.Background(), Config{
		Name: "authenticated", Kind: KindHTTP, URL: "https://example.com/health",
		RequestHeaders: []RequestHeader{{Name: "authorization", Value: "Bearer secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Config.RequestHeaders) != 1 || loaded.Config.RequestHeaders[0] != (RequestHeader{Name: "Authorization", Value: "Bearer secret"}) {
		t.Fatalf("request headers = %#v", loaded.Config.RequestHeaders)
	}
}

func TestResolveRequestHeadersExpandsEmbeddedVariables(t *testing.T) {
	headers, err := ResolveRequestHeaders([]RequestHeader{
		{Name: "Authorization", Value: "Bearer {{API_TOKEN}}"},
		{Name: "X-Environment", Value: "{{ENVIRONMENT}}-api"},
	}, map[string]string{"API_TOKEN": "secret", "ENVIRONMENT": "staging"})
	if err != nil {
		t.Fatal(err)
	}
	want := []RequestHeader{
		{Name: "Authorization", Value: "Bearer secret"},
		{Name: "X-Environment", Value: "staging-api"},
	}
	for index := range want {
		if headers[index] != want[index] {
			t.Fatalf("headers[%d] = %#v, want %#v", index, headers[index], want[index])
		}
	}
}

func TestResolveRequestHeadersRejectsMissingMalformedOrUnsafeVariables(t *testing.T) {
	tests := []struct {
		name      string
		header    RequestHeader
		variables map[string]string
	}{
		{name: "missing", header: RequestHeader{Name: "Authorization", Value: "Bearer {{MISSING}}"}},
		{name: "malformed", header: RequestHeader{Name: "X-Test", Value: "{{not_valid}}"}},
		{name: "control character", header: RequestHeader{Name: "X-Test", Value: "{{VALUE}}"}, variables: map[string]string{"VALUE": "one\r\ntwo"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ResolveRequestHeaders([]RequestHeader{test.header}, test.variables); err == nil {
				t.Fatal("expected variable resolution error")
			}
		})
	}
}

func TestManagerResolvesLatestVariablesWithoutPersistingValues(t *testing.T) {
	received := make(chan Config, 1)
	var loads atomic.Int32
	manager := newTestManager(t, Options{
		LoadVariables: func(context.Context) (map[string]string, error) {
			if loads.Add(1) == 1 {
				return map[string]string{"API_TOKEN": "first"}, nil
			}
			return map[string]string{"API_TOKEN": "latest"}, nil
		},
		Probe: probeFunc(func(_ context.Context, config Config) Evidence {
			received <- config
			return Evidence{Success: true}
		}),
	})
	created, err := manager.Create(context.Background(), Config{
		Name: "variables", Kind: KindHTTP, URL: "https://example.com/health",
		RequestHeaders: []RequestHeader{{Name: "Authorization", Value: "Bearer {{API_TOKEN}}"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case checked := <-received:
		if checked.RequestHeaders[0].Value != "Bearer latest" {
			t.Fatalf("checked headers = %#v", checked.RequestHeaders)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for website check")
	}
	loaded, err := manager.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.RequestHeaders[0].Value != "Bearer {{API_TOKEN}}" {
		t.Fatalf("persisted headers = %#v", loaded.Config.RequestHeaders)
	}
}

func TestManagerRejectsMissingRequestHeaderVariable(t *testing.T) {
	manager := newTestManager(t, Options{
		LoadVariables: func(context.Context) (map[string]string, error) { return map[string]string{}, nil },
		Probe:         probeFunc(func(context.Context, Config) Evidence { return Evidence{Success: true} }),
	})
	_, err := manager.Create(context.Background(), Config{
		Name: "missing", Kind: KindHTTP, URL: "https://example.com/health",
		RequestHeaders: []RequestHeader{{Name: "Authorization", Value: "Bearer {{MISSING}}"}},
	})
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagerRecordsConfigurationFailureWhenVariableDisappearsBeforeCheck(t *testing.T) {
	var loads atomic.Int32
	probeCalled := make(chan struct{}, 1)
	manager := newTestManager(t, Options{
		LoadVariables: func(context.Context) (map[string]string, error) {
			if loads.Add(1) == 1 {
				return map[string]string{"API_TOKEN": "available-at-save"}, nil
			}
			return map[string]string{}, nil
		},
		Probe: probeFunc(func(context.Context, Config) Evidence {
			probeCalled <- struct{}{}
			return Evidence{Success: true}
		}),
	})
	created, err := manager.Create(context.Background(), Config{
		Name: "disappearing", Kind: KindHTTP, URL: "https://example.com/health",
		RequestHeaders: []RequestHeader{{Name: "Authorization", Value: "Bearer {{API_TOKEN}}"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, getErr := manager.Get(context.Background(), created.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if loaded.Latest.ErrorCategory == "configuration" {
			if !strings.Contains(loaded.Latest.TechnicalError, "API_TOKEN") {
				t.Fatalf("technical error = %q", loaded.Latest.TechnicalError)
			}
			select {
			case <-probeCalled:
				t.Fatal("probe received an unresolved request header")
			default:
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for configuration failure")
}
