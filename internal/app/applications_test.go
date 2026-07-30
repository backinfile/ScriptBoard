package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/appstatus"
)

type applicationFixtureProbe struct {
	snapshot appstatus.RawSnapshot
}

func (p applicationFixtureProbe) Snapshot(context.Context) appstatus.RawSnapshot {
	return p.snapshot
}

func (p applicationFixtureProbe) RuntimeDetail(_ context.Context, request appstatus.DetailRequest) appstatus.RuntimeDetail {
	switch request.Application.Kind {
	case appstatus.KindHost:
		return appstatus.RuntimeDetail{
			State: appstatus.RuntimeAvailable,
			Kind:  appstatus.KindHost,
			Host: &appstatus.HostRuntimeDetail{
				CommandLine:    request.Application.Technical + " --serve",
				PID:            request.Processes[0].PID,
				ExecutablePath: request.Application.Technical,
			},
		}
	case appstatus.KindDocker:
		return appstatus.RuntimeDetail{
			State: appstatus.RuntimeAvailable,
			Kind:  appstatus.KindDocker,
			Docker: &appstatus.DockerRuntimeDetail{
				ContainerID: request.Container.ID,
				Image:       request.Application.Technical,
			},
		}
	default:
		return appstatus.RuntimeDetail{State: appstatus.RuntimeUnavailable, Kind: request.Application.Kind}
	}
}

func TestApplicationsPageListsDeterministicProbeDataAndPersistsPin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	collectedAt := time.Date(2026, 7, 29, 6, 30, 0, 0, time.UTC)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		ApplicationProbe: applicationFixtureProbe{snapshot: appstatus.RawSnapshot{
			CollectedAt:      collectedAt,
			LogicalCores:     4,
			TotalMemoryBytes: 16 << 30,
			DockerAvailable:  true,
			Processes: []appstatus.RawProcess{{
				PID: 201, CreatedAt: collectedAt.Add(-time.Hour), Name: "Host Agent",
				ExecutablePath: "/opt/host-agent", ResidentMemoryBytes: 256 << 20, Threads: 8,
			}},
			Containers: []appstatus.RawContainer{{
				Name: "api-prod", Image: "ghcr.io/example/api:2026.07", CPUPercent: 22.5,
				MemoryBytes: 720 << 20, MemoryLimitBytes: 2 << 30,
				ReadBytesPerSecond: 4 << 20, WriteBytesPerSecond: 2 << 20, ProcessCount: 18,
			}},
		}},
	})

	response, err := client.Get(serverURL + "/monitor/applications")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte(`data-applications-page`)) {
		t.Fatalf("applications status=%d body=%s", response.StatusCode, page)
	}
	if !bytes.Contains(page, []byte(`data-enabled-label="Live updates on"`)) ||
		!bytes.Contains(page, []byte(`data-disabled-label="Live updates off"`)) {
		t.Fatalf("refresh switch labels are not safely embedded: %s", page)
	}
	apiPosition := bytes.Index(page, []byte("api-prod"))
	hostPosition := bytes.Index(page, []byte("Host Agent"))
	if apiPosition < 0 || hostPosition < 0 || apiPosition >= hostPosition {
		t.Fatalf("default CPU ordering is not reflected in page: %s", page)
	}
	action := regexp.MustCompile(`action="(/monitor/applications/[^"]+/pin)"`).FindSubmatch(page)
	if len(action) != 2 {
		t.Fatalf("pin action not found: %s", page)
	}

	response, err = client.PostForm(serverURL+string(action[1]), url.Values{
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor/applications" {
		t.Fatalf("pin status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/monitor/applications")
	if err != nil {
		t.Fatal(err)
	}
	pinnedPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	pinnedStart := bytes.Index(pinnedPage, []byte(`data-pinned-applications`))
	runningStart := bytes.Index(pinnedPage, []byte(`data-running-applications`))
	if pinnedStart < 0 || runningStart <= pinnedStart || !bytes.Contains(pinnedPage[pinnedStart:runningStart], []byte("api-prod")) {
		t.Fatalf("pinned application is not rendered in the pinned section: %s", pinnedPage)
	}

	response, err = client.Get(serverURL + "/monitor/applications/data")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("applications data status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	var payload appstatus.View
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(payload.Pinned) != 1 || payload.Pinned[0].Name != "api-prod" ||
		payload.Applications[0].Name != "api-prod" || payload.Applications[0].MemoryLimitBytes != 2<<30 {
		t.Fatalf("applications data = %#v", payload)
	}

	response, err = client.Get(serverURL + "/history/audit?q=pin_application")
	if err != nil {
		t.Fatal(err)
	}
	auditPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(auditPage, []byte("pin_application")) ||
		!bytes.Contains(auditPage, []byte(payload.Pinned[0].ID)) {
		t.Fatalf("application audit did not retain its opaque identity: %s", auditPage)
	}
	if bytes.Contains(auditPage, []byte("/opt/host-agent")) ||
		bytes.Contains(auditPage, []byte("ghcr.io/example/api:2026.07")) {
		t.Fatalf("application audit leaked technical identity: %s", auditPage)
	}

	response, err = client.Get(serverURL + "/monitor/applications/data?sort=unknown")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid applications query status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
}

func TestApplicationsPinRequiresCSRF(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		ApplicationProbe: applicationFixtureProbe{snapshot: appstatus.RawSnapshot{
			CollectedAt: time.Now().UTC(),
			Containers:  []appstatus.RawContainer{{Name: "api-prod", Image: "example/api"}},
		}},
	})
	response, err := client.Get(serverURL + "/monitor/applications/data")
	if err != nil {
		t.Fatal(err)
	}
	var payload appstatus.View
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	response, err = client.PostForm(serverURL+"/monitor/applications/"+payload.Applications[0].ID+"/pin", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("pin without CSRF status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}
}

func TestApplicationDetailsRouteReturnsRuntimeAndHistoryJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	collectedAt := time.Now().UTC().Truncate(time.Second)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		ApplicationProbe: applicationFixtureProbe{snapshot: appstatus.RawSnapshot{
			CollectedAt: collectedAt,
			Processes: []appstatus.RawProcess{{
				PID: 301, ParentPID: 1, CreatedAt: collectedAt.Add(-time.Hour),
				Name: "Host Agent", ExecutablePath: "/opt/host-agent",
			}},
		}},
	})
	response, err := client.Get(serverURL + "/monitor/applications/data")
	if err != nil {
		t.Fatal(err)
	}
	var view appstatus.View
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	response, err = client.Get(serverURL + "/monitor/applications/" + view.Applications[0].ID + "/details?range=15m")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("Cache-Control") != "no-store" ||
		response.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("details status=%d cache=%q type=%q", response.StatusCode, response.Header.Get("Cache-Control"), response.Header.Get("Content-Type"))
	}
	var details appstatus.ApplicationDetails
	if err := json.NewDecoder(response.Body).Decode(&details); err != nil {
		t.Fatal(err)
	}
	if details.Application.Name != "Host Agent" ||
		details.Runtime.State != appstatus.RuntimeAvailable ||
		details.Runtime.Host == nil ||
		details.Runtime.Host.CommandLine != "/opt/host-agent --serve" ||
		details.History.Range != "15m" ||
		len(details.History.Points) == 0 {
		t.Fatalf("details = %#v", details)
	}

	response, err = client.Get(serverURL + "/monitor/applications/" + view.Applications[0].ID + "/details?range=7d")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var errorPayload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&errorPayload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest ||
		errorPayload.Error.Code != "invalid_history_range" {
		t.Fatalf("invalid range status=%d payload=%#v", response.StatusCode, errorPayload)
	}
}

func TestMovePinnedApplicationRoutePersistsOrderAndRequiresCSRF(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		ApplicationProbe: applicationFixtureProbe{snapshot: appstatus.RawSnapshot{
			CollectedAt: time.Now().UTC(),
			Containers: []appstatus.RawContainer{
				{ID: "alpha-id", Name: "alpha"},
				{ID: "beta-id", Name: "beta"},
				{ID: "gamma-id", Name: "gamma"},
			},
		}},
	})
	response, err := client.Get(serverURL + "/monitor/applications")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	token := formToken(t, page)

	response, err = client.Get(serverURL + "/monitor/applications/data?sort=name&direction=asc")
	if err != nil {
		t.Fatal(err)
	}
	var view appstatus.View
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	for _, application := range view.Applications {
		response, err = client.PostForm(serverURL+"/monitor/applications/"+application.ID+"/pin", url.Values{"csrf_token": {token}})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("pin %s status=%d", application.Name, response.StatusCode)
		}
	}
	gammaID := view.Applications[2].ID
	response, err = client.PostForm(serverURL+"/monitor/applications/"+gammaID+"/move", url.Values{
		"csrf_token": {token},
		"direction":  {"up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/monitor/applications/data")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := []string{view.Pinned[0].Name, view.Pinned[1].Name, view.Pinned[2].Name}; got[0] != "alpha" || got[1] != "gamma" || got[2] != "beta" {
		t.Fatalf("pin order after move = %v", got)
	}

	response, err = client.PostForm(serverURL+"/monitor/applications/"+gammaID+"/move", url.Values{
		"csrf_token": {token},
		"direction":  {"top"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move to top status=%d", response.StatusCode)
	}
	response, err = client.Get(serverURL + "/monitor/applications/data")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := []string{view.Pinned[0].Name, view.Pinned[1].Name, view.Pinned[2].Name}; got[0] != "gamma" || got[1] != "alpha" || got[2] != "beta" {
		t.Fatalf("pin order after move to top = %v", got)
	}

	response, err = client.PostForm(serverURL+"/monitor/applications/"+gammaID+"/move", url.Values{"direction": {"up"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("move without CSRF status=%d", response.StatusCode)
	}
}

func TestApplicationMonitoringRoutesRequireASession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application, err := app.Open(app.Config{
		ManagedRoot:      filepath.Join(root, "managed"),
		StateRoot:        filepath.Join(root, "state"),
		ApplicationProbe: applicationFixtureProbe{snapshot: appstatus.RawSnapshot{CollectedAt: time.Now().UTC()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	server := httptest.NewServer(application.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/monitor/applications"},
		{method: http.MethodGet, path: "/monitor/applications/data"},
		{method: http.MethodGet, path: "/monitor/applications/opaque-id/details"},
		{method: http.MethodPost, path: "/monitor/applications/opaque-id/pin"},
		{method: http.MethodPost, path: "/monitor/applications/opaque-id/unpin"},
		{method: http.MethodPost, path: "/monitor/applications/opaque-id/move"},
	} {
		request, err := http.NewRequest(route.method, server.URL+route.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
			t.Fatalf("%s %s status=%d location=%q", route.method, route.path, response.StatusCode, response.Header.Get("Location"))
		}
	}
}
