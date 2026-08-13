package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"scriptboard/internal/websitemonitor"
)

func TestWebsiteMonitorConfigAcceptsStatusCodeRangesAndAnyResponse(t *testing.T) {
	baseForm := url.Values{
		"name":              {"Status rules"},
		"scope":             {"external"},
		"kind":              {"http"},
		"url":               {"https://example.com/health"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"10"},
		"http_method":       {"GET"},
	}

	rangeForm := cloneWebsiteFormValues(baseForm)
	rangeForm.Set("http_success_mode", "exact")
	rangeForm.Set("expected_statuses", "200;401-499;503")
	rangeConfig, rangeErrors := websiteConfigFromForm(t, rangeForm)
	if len(rangeErrors) != 0 {
		t.Fatalf("range form errors = %#v", rangeErrors)
	}
	wantRanges := []websitemonitor.HTTPStatusRange{
		{Start: 200, End: 200},
		{Start: 401, End: 499},
		{Start: 503, End: 503},
	}
	if !reflect.DeepEqual(rangeConfig.ExpectedStatusRanges, wantRanges) {
		t.Fatalf("status ranges = %#v, want %#v", rangeConfig.ExpectedStatusRanges, wantRanges)
	}

	responseForm := cloneWebsiteFormValues(baseForm)
	responseForm.Set("http_success_mode", "response")
	responseConfig, responseErrors := websiteConfigFromForm(t, responseForm)
	if len(responseErrors) != 0 {
		t.Fatalf("any-response form errors = %#v", responseErrors)
	}
	if responseConfig.HTTPSuccessMode != websitemonitor.HTTPSuccessAnyResponse {
		t.Fatalf("success mode = %q", responseConfig.HTTPSuccessMode)
	}
}

func websiteConfigFromForm(t *testing.T, form url.Values) (websitemonitor.Config, map[string]string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/monitor/websites", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return websiteMonitorConfigFromRequest(request)
}

func cloneWebsiteFormValues(source url.Values) url.Values {
	clone := make(url.Values, len(source))
	for key, values := range source {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
