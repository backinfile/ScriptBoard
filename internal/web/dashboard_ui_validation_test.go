package web

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardFlowValidationIdentifiesFieldAndLine(t *testing.T) {
	r := httptest.NewRequest("POST", "/config/dashboard-cards/example", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	writeDashboardFlowValidation(w, r, errors.New("yaml: line 3: did not find expected key"))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"flow_yaml"`) || !strings.Contains(w.Body.String(), `"line":3`) {
		t.Fatalf("validation=%d %s", w.Code, w.Body.String())
	}
}
func TestStepUpTargetLabelsExcludeUntrustedDestinations(t *testing.T) {
	if got := stepUpTargetLabel(localeEnglishUS, "/config/quick-runs?secret=hidden"); got != "Quick Runs" {
		t.Fatal(got)
	}
	if got := stepUpTargetLabel(localeEnglishUS, "https://evil.test/secret"); got != "Previous page" {
		t.Fatal(got)
	}
}
