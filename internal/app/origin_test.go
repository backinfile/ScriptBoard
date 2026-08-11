package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStateChangingRequestRejectsCrossOriginBrowserRequests(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "https://panel.example/settings/account", nil)
	request.Header.Set("Origin", "https://evil.example")
	if validRequestOrigin(request) {
		t.Fatal("cross-origin request was accepted")
	}

	request.Header.Set("Origin", "https://panel.example")
	if !validRequestOrigin(request) {
		t.Fatal("same-origin request was rejected")
	}
}

func TestOriginValidationAllowsNonBrowserClientsWithoutOrigin(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "https://panel.example/settings/account", nil)
	if !validRequestOrigin(request) {
		t.Fatal("request without Origin was rejected")
	}
}

func TestOriginValidationRejectsSchemeDowngrade(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "https://panel.example/settings/account", nil)
	request.Header.Set("Origin", "http://panel.example")
	if validRequestOrigin(request) {
		t.Fatal("origin with a downgraded scheme was accepted")
	}
}
