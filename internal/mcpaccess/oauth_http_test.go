package mcpaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRevocationEndpointDoesNotHideStorageFailure(t *testing.T) {
	store, db, _ := testStore(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	oauth := &OAuthHTTP{Store: store, Limiter: NewLimiter(10, 1)}
	request := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader("token=value"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	oauth.Revoke(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%q, want 500", response.Code, response.Body.String())
	}
}
