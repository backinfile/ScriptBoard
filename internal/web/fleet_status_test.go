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

	response, err = remoteClient.PostForm(remoteURL+"/settings/nodes/access-tokens", url.Values{
		"csrf_token": {formToken(t, settings)}, "label": {"Primary overview"},
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
	if response.StatusCode != http.StatusOK || !strings.Contains(string(peerDetail), "Remove machine") || strings.Contains(string(peerDetail), token) {
		t.Fatalf("peer detail status=%d body=%s", response.StatusCode, peerDetail)
	}
}
