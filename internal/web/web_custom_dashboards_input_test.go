package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"scriptboard/internal/customdashboard"
)

func TestRegistryCardInputOnlyPreservesBasicCredentials(t *testing.T) {
	parse := func(authMode string) customdashboard.CardInput {
		values := url.Values{
			"name": {"镜像"}, "type": {"registry"}, "registry_endpoint": {"https://registry.example.test"},
			"registry_images": {"team/api"}, "registry_auth_mode": {authMode}, "registry_username": {"robot"},
		}
		request := httptest.NewRequest(http.MethodPost, "/config/dashboard-cards/card/test", strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		input, err := customDashboardCardInput(request, true)
		if err != nil {
			t.Fatal(err)
		}
		return input
	}

	if parse("anonymous").PreserveRegistryPassword {
		t.Fatal("anonymous Registry edit must not request preservation of a nonexistent password")
	}
	if !parse("basic").PreserveRegistryPassword {
		t.Fatal("existing basic Registry edit should preserve an omitted password")
	}
}
