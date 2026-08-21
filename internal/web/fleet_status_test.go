package web_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	app "scriptboard/internal/web"
)

func TestScriptBoardInstancesShareReadOnlyStatusWithFleetOverview(t *testing.T) {
	remoteRoot := t.TempDir()
	remoteClient, remoteURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(remoteRoot, "state")})

	response, err := remoteClient.Get(remoteURL + "/settings/nodes")
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(settings), `data-node-settings`) {
		t.Fatalf("node settings status=%d body=%s", response.StatusCode, settings)
	}
	if !strings.Contains(string(settings), `href="/settings/nodes/access-tokens/new" data-task-link`) ||
		strings.Contains(string(settings), `class="fleet-token-form"`) {
		t.Fatalf("node settings must launch token creation as a task drawer: %s", settings)
	}

	response, err = remoteClient.Get(remoteURL + "/settings/nodes/access-tokens/new")
	if err != nil {
		t.Fatal(err)
	}
	tokenTask, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(tokenTask), `data-task-kind="fleet-access-token"`) ||
		!strings.Contains(string(tokenTask), `class="fleet-token-form"`) {
		t.Fatalf("token task status=%d body=%s", response.StatusCode, tokenTask)
	}

	response, err = remoteClient.PostForm(remoteURL+"/settings/nodes/access-tokens", url.Values{
		"csrf_token": {formToken(t, tokenTask)}, "label": {"Primary overview"},
	})
	if err != nil {
		t.Fatal(err)
	}
	issuedPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	match := regexp.MustCompile(`data-node-access-token>([^<]+)<`).FindSubmatch(issuedPage)
	if response.StatusCode != http.StatusOK || len(match) != 2 {
		t.Fatalf("issue token status=%d body=%s", response.StatusCode, issuedPage)
	}
	if !strings.Contains(string(issuedPage), `data-task-refresh-on-close`) {
		t.Fatalf("issued token task must refresh settings after close: %s", issuedPage)
	}
	token := string(match[1])

	unauthorized, err := http.Get(remoteURL + "/api/fleet/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized export status=%d", unauthorized.StatusCode)
	}

	request, _ := http.NewRequest(http.MethodGet, remoteURL+"/api/fleet/v1/status", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	exported, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer exported.Body.Close()
	var payload map[string]any
	if exported.StatusCode != http.StatusOK || json.NewDecoder(exported.Body).Decode(&payload) != nil || payload["protocolVersion"] != float64(1) {
		t.Fatalf("export status=%d payload=%v", exported.StatusCode, payload)
	}

	hubRoot := t.TempDir()
	hubClient, hubURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(hubRoot, "state"), FleetStatusClient: &http.Client{}})
	response, err = hubClient.Get(hubURL + "/monitor/nodes/new")
	if err != nil {
		t.Fatal(err)
	}
	addPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	response, err = hubClient.PostForm(hubURL+"/monitor/nodes", url.Values{
		"csrf_token": {formToken(t, addPage)}, "name": {"Production"}, "endpoint": {remoteURL}, "access_token": {token},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor" {
		t.Fatalf("add node status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = hubClient.Get(hubURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	overview, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(overview), `data-fleet-overview`) || !strings.Contains(string(overview), "Production") || strings.Contains(string(overview), token) {
		t.Fatalf("fleet overview status=%d body=%s", response.StatusCode, overview)
	}
	peerMatches := regexp.MustCompile(`data-fleet-node="([^"]+)"`).FindAllSubmatch(overview, -1)
	if len(peerMatches) != 2 {
		t.Fatalf("fleet cards=%d body=%s", len(peerMatches), overview)
	}
	response, err = hubClient.Get(hubURL + "/monitor?node=" + url.QueryEscape(string(peerMatches[1][1])))
	if err != nil {
		t.Fatal(err)
	}
	peerDetail, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{"Remove machine", `data-overview-tab="summary"`, `data-metric-card="cpu"`, "ScriptBoard service"} {
		if !strings.Contains(string(peerDetail), expected) {
			t.Fatalf("peer detail missing %q: %s", expected, peerDetail)
		}
	}
	for _, forbidden := range []string{`tab=details`, `data-overview-range`, `data-metric-chart`, `data-duplex-chart`, `data-active-runs>`, `data-host-detail`, `data-overview-drawer`, token} {
		if strings.Contains(string(peerDetail), forbidden) {
			t.Fatalf("peer detail exposes unavailable UI or a secret %q: %s", forbidden, peerDetail)
		}
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("peer detail status=%d body=%s", response.StatusCode, peerDetail)
	}

	response, err = hubClient.Get(hubURL + "/monitor?node=" + url.QueryEscape(string(peerMatches[1][1])) + "&tab=details")
	if err != nil {
		t.Fatal(err)
	}
	staleDetail, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(staleDetail), `data-overview-tab="summary"`) || strings.Contains(string(staleDetail), `data-host-detail`) {
		t.Fatalf("stale remote detail URL did not fall back to summary: status=%d body=%s", response.StatusCode, staleDetail)
	}
}
