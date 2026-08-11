package websitemonitor

import (
	"context"
	"strings"
	"testing"
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
