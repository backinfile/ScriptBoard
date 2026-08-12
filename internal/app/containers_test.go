package app_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/appstatus"
)

type containerFixtureOperation struct {
	name   string
	action appstatus.ContainerAction
}

type containerFixtureProbe struct {
	mu         sync.Mutex
	snapshot   appstatus.RawSnapshot
	operations []containerFixtureOperation
}

func (probe *containerFixtureProbe) Snapshot(context.Context) appstatus.RawSnapshot {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.snapshot
}

func (probe *containerFixtureProbe) OperateContainer(_ context.Context, name string, action appstatus.ContainerAction) error {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	probe.operations = append(probe.operations, containerFixtureOperation{name: name, action: action})
	return nil
}

func TestContainersPageListsAllStatesSortsInHeadersAndGuardsOperations(t *testing.T) {
	probe := &containerFixtureProbe{snapshot: appstatus.RawSnapshot{
		CollectedAt: time.Now().UTC(), DockerAvailable: true,
		Containers: []appstatus.RawContainer{
			{ID: "new-api-id", Name: "/Api-Prod", Image: "ghcr.io/acme/api:v2", State: "running", Status: "Up 4 minutes", ComposeProject: "platform", ComposeService: "api", PublishedPorts: []string{"127.0.0.1:8443->443/tcp"}},
			{ID: "worker-id", Name: "worker", Image: "ghcr.io/acme/worker:v1", State: "exited", Status: "Exited (0) 2 hours ago"},
		},
	}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), ApplicationProbe: probe,
	})

	response, err := client.Get(serverURL + "/monitor/containers")
	if err != nil {
		t.Fatal(err)
	}
	page, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, expected := range [][]byte{
		[]byte("Api-Prod"), []byte("worker"), []byte("ghcr.io/acme/api:v2"),
		[]byte(`href="/monitor/containers?direction=asc&amp;sort=name"`),
		[]byte(`href="/monitor/containers?direction=desc&amp;sort=state"`),
		[]byte(`data-container-status-link`), []byte(`data-container-operation`),
	} {
		if response.StatusCode != http.StatusOK || !bytes.Contains(page, expected) {
			t.Fatalf("container page status=%d missing %q: %s", response.StatusCode, expected, page)
		}
	}

	operationURL := serverURL + "/monitor/containers/api-prod/operate"
	response, err = client.PostForm(operationURL, url.Values{
		"csrf_token": {formToken(t, page)}, "action": {"restart"}, "return_to": {"/monitor/containers?status=running"},
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmation, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(confirmation, []byte("api-prod")) {
		t.Fatalf("operation confirmation status=%d body=%s", response.StatusCode, confirmation)
	}
	if len(probe.operations) != 0 {
		t.Fatalf("operation ran before confirmation: %#v", probe.operations)
	}

	response, err = client.PostForm(operationURL, url.Values{
		"csrf_token": {formToken(t, confirmation)}, "action": {"restart"}, "confirmed": {"yes"}, "return_to": {"/monitor/containers?status=running"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/monitor/containers?status=running" {
		t.Fatalf("confirmed operation status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if len(probe.operations) != 1 || probe.operations[0].name != "api-prod" || probe.operations[0].action != appstatus.ContainerRestart {
		t.Fatalf("operations = %#v", probe.operations)
	}
}

func TestMonitorTabsReplaceOnlyTheSnapshotAndRestoreScroll(t *testing.T) {
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		StateRoot: filepath.Join(t.TempDir(), "state"), ApplicationProbe: &containerFixtureProbe{},
	})
	response, err := client.Get(serverURL + "/assets/app-v2.js")
	if err != nil {
		t.Fatal(err)
	}
	script, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, expected := range [][]byte{
		[]byte(`"[data-container-status-link],.container-sort-link"`),
		[]byte(`".kubernetes-status-tabs a,.kubernetes-sort-link"`),
		[]byte(`fetchDocument(destination, { cache: "no-store"`),
		[]byte(`fetchDocument(snapshotLink.href, { cache: "no-store"`),
		[]byte(`const scrollX = window.scrollX, scrollY = window.scrollY;`),
		[]byte(`window.scrollTo(scrollX, scrollY)`),
	} {
		if !bytes.Contains(script, expected) {
			t.Fatalf("monitor partial navigation is missing %q", expected)
		}
	}
}
