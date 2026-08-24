package web_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	app "scriptboard/internal/web"
)

func TestAmbiguousEncodedPathSeparatorIsRejectedBeforeRouting(t *testing.T) {
	application, err := app.Open(app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	client := *server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/assets%2fapp.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("encoded path separator status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestBackslashInRequestPathIsRejectedBeforeRouting(t *testing.T) {
	application, err := app.Open(app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/assets\\app.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("backslash path status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestDotSegmentInRequestPathIsRejectedBeforeServeMuxRedirect(t *testing.T) {
	application, err := app.Open(app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	client := *server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/assets/../login", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("dot-segment path status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestRepeatedPathSeparatorIsRejectedBeforeServeMuxRedirect(t *testing.T) {
	application, err := app.Open(app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	client := *server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/assets//app.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("repeated path separator status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestEncodedSeparatorsRemainValidInsideQueryValues(t *testing.T) {
	application, err := app.Open(app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)

	response, err := server.Client().Get(server.URL + "/login?return_to=%2Fresources%2Ffiles&path=C%3A%5Cscripts")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("encoded query separator status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestAbsoluteFormRequestTargetIsRejectedByOriginServer(t *testing.T) {
	application, err := app.Open(app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), AllowedHosts: []string{"panel.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })

	request := httptest.NewRequest(http.MethodGet, "http://panel.example/login", nil)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("absolute-form request target status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
