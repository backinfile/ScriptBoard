package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/appstatus"
	"scriptboard/internal/websitemonitor"
)

func TestApplicationsPageExposesLiveFactsAndExpandableObservationDetails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	collectedAt := time.Now().UTC()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		ApplicationProbe: applicationFixtureProbe{snapshot: appstatus.RawSnapshot{
			CollectedAt:      collectedAt,
			LogicalCores:     4,
			TotalMemoryBytes: 8 << 30,
			Processes: []appstatus.RawProcess{{
				PID: 901, CreatedAt: collectedAt.Add(-time.Hour), Name: "Ledger worker",
				ExecutablePath: "/opt/ledger-worker", ResidentMemoryBytes: 128 << 20,
			}},
		}},
	})

	response, err := client.Get(serverURL + "/monitor/applications")
	if err != nil {
		t.Fatal(err)
	}
	initialPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	response, err = client.Get(serverURL + "/monitor/applications/data")
	if err != nil {
		t.Fatal(err)
	}
	var view appstatus.View
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(view.Applications) != 1 {
		t.Fatalf("applications = %#v", view.Applications)
	}

	response, err = client.PostForm(
		serverURL+"/monitor/applications/"+view.Applications[0].ID+"/pin",
		url.Values{"csrf_token": {formToken(t, initialPage)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("pin status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/monitor/applications")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	requireAlignmentFragments(t, page,
		`class="applications-fact-strip"`,
		`data-pinned-live-fact data-state="on"`,
		`>Pinned live updates<`,
		`data-application-mode="pinned"`,
		`data-application-mode="runtime"`,
		`data-application-drawer aria-hidden="true"`,
		`role="tablist"`,
		`data-application-detail-tab="history"`,
		`data-application-detail-tab="runtime"`,
		`data-application-range="15m"`,
		`data-application-range="1h"`,
		`data-application-range="6h"`,
		`data-application-range="24h"`,
		`data-application-detail-panel="history"`,
		`data-application-history-output`,
		`data-application-detail-panel="runtime"`,
		`data-application-runtime-output`,
		`name="direction" value="top"`,
		`class="application-record pinned-application running-application"`,
		`tabindex="0" aria-haspopup="dialog"`,
	)
	if bytes.Contains(page, []byte(`data-application-detail-toggle`)) ||
		bytes.Contains(page, []byte(`data-running-detail-for`)) {
		t.Fatal("application details must use the shared drawer instead of inline expansion")
	}

	response, err = client.Get(serverURL + "/assets/app-v2.js")
	if err != nil {
		t.Fatal(err)
	}
	script, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	requireAlignmentFragments(t, script,
		`data-task-panel-close`,
		`ScriptBoard will not resubmit it automatically.`,
		`taskPanelState !== submittingTaskState`,
		`signal:controller.signal`,
		`backgroundSurfaceBlocked`,
		`drawerNavigation.hidden = activeMode === "runtime"`,
		`loadDrawerDetails(true)`,
		`application-series--${item.color}`,
		`state !== "available" && state !== "partial"`,
	)
	if bytes.Contains(script, []byte(`form.submit();`)) {
		t.Fatal("async POST failures must not replay the form submission")
	}
}

func TestWebsiteCreateEditNginxAndDetailArePanelSafeTasks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(context.Context, websitemonitor.Config) websitemonitor.Evidence {
				return websitemonitor.Evidence{Success: true, StatusCode: http.StatusNoContent}
			}),
		},
	})

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	requireAlignmentFragments(t, newPage,
		`data-task-page`,
		`data-task-kind="website-new"`,
		`data-task-close-label="Close"`,
		`data-website-monitor-form data-async`,
	)

	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {formToken(t, newPage)},
		"name":              {"Panel-safe website"},
		"scope":             {"external"},
		"kind":              {"http"},
		"url":               {"https://panel-safe.example/health"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"10"},
		"http_method":       {"GET"},
		"http_success_mode": {"range"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	detailPath := response.Header.Get("Location")
	if !strings.HasPrefix(detailPath, "/monitor/websites/") {
		t.Fatalf("create location=%q", detailPath)
	}

	response, err = client.Get(serverURL + detailPath + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	editPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	requireAlignmentFragments(t, editPage,
		`data-task-page`,
		`data-task-kind="website-edit"`,
		`data-task-close-label="Close"`,
		`data-website-monitor-form data-async`,
	)

	response, err = client.Get(serverURL + "/monitor/websites/nginx")
	if err != nil {
		t.Fatal(err)
	}
	nginxPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	requireAlignmentFragments(t, nginxPage,
		`data-task-page`,
		`data-task-kind="website-nginx"`,
		`data-task-close-label="Close"`,
		`data-website-nginx`,
		`action="/monitor/websites/nginx/scan" data-async`,
	)

	response, err = client.Get(serverURL + detailPath)
	if err != nil {
		t.Fatal(err)
	}
	detailPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	requireAlignmentFragments(t, detailPage,
		`data-task-page`,
		`data-task-kind="website-detail"`,
		`data-task-close-label="Close"`,
		`data-website-check-form`,
		`data-check-timeout-ms="10000"`,
		`href="`+detailPath+`/edit" data-task-link`,
		`action="`+detailPath+`/pause" data-async`,
		`action="`+detailPath+`/delete" data-async`,
	)
}

func TestPendingWebsiteIsReportedAndFilteredAsAwaitingVerification(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	defer close(release)
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(ctx context.Context, _ websitemonitor.Config) websitemonitor.Evidence {
				startOnce.Do(func() { close(started) })
				select {
				case <-release:
					return websitemonitor.Evidence{Success: true, StatusCode: http.StatusNoContent}
				case <-ctx.Done():
					return websitemonitor.Evidence{ErrorCategory: "cancelled", Summary: ctx.Err().Error()}
				}
			}),
			Tick: time.Hour,
		},
	})

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {formToken(t, newPage)},
		"name":              {"Awaiting first check"},
		"scope":             {"external"},
		"kind":              {"http"},
		"url":               {"https://pending.example/health"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"30"},
		"http_method":       {"GET"},
		"http_success_mode": {"range"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	detailPath := response.Header.Get("Location")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first website check did not start")
	}

	response, err = client.Get(serverURL + "/monitor/websites/data?state=verifying")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Monitors []struct {
			State websitemonitor.State
		}
		Counts struct {
			Verifying int
		}
		NeedsCare int
	}
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(snapshot.Monitors) != 1 || snapshot.Monitors[0].State != websitemonitor.StatePending ||
		snapshot.Counts.Verifying != 1 || snapshot.NeedsCare != 1 {
		t.Fatalf("pending snapshot = %#v", snapshot)
	}

	response, err = client.Get(serverURL + detailPath)
	if err != nil {
		t.Fatal(err)
	}
	detailPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	requireAlignmentFragments(t, detailPage,
		`website-current-incident--verifying`,
		`Awaiting confirmation`,
		`Consecutive failures`,
		`First failure`,
		`Next check`,
	)
}

func TestWebsiteDetailRendersSeventyTwoBucketsFiveChecksAndAlertFacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var probeCalls atomic.Int32
	client, serverURL := authenticatedClientWithConfig(t, app.Config{
		ManagedRoot: filepath.Join(root, "managed"),
		StateRoot:   filepath.Join(root, "state"),
		WebsiteMonitorOptions: websitemonitor.Options{
			Probe: websiteProbeFunc(func(context.Context, websitemonitor.Config) websitemonitor.Evidence {
				call := probeCalls.Add(1)
				return websitemonitor.Evidence{
					Success:       false,
					ErrorCategory: "connect",
					Summary:       fmt.Sprintf("connection failure %d", call),
					Latency:       time.Duration(call) * time.Millisecond,
				}
			}),
			Tick:       time.Hour,
			RetryDelay: time.Hour,
		},
	})

	response, err := client.Get(serverURL + "/monitor/websites/new")
	if err != nil {
		t.Fatal(err)
	}
	newPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	csrfToken := formToken(t, newPage)
	response, err = client.PostForm(serverURL+"/monitor/websites", url.Values{
		"csrf_token":        {csrfToken},
		"name":              {"Failure evidence"},
		"scope":             {"external"},
		"kind":              {"http"},
		"url":               {"https://failure-evidence.example/health"},
		"frequency_seconds": {"60"},
		"timeout_seconds":   {"10"},
		"http_method":       {"GET"},
		"http_success_mode": {"range"},
		"follow_redirects":  {"1"},
		"verify_tls":        {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	detailPath := response.Header.Get("Location")

	waitForWebsiteCheckCount(t, client, serverURL, detailPath, 1)
	for expected := 2; expected <= 5; expected++ {
		response, err = client.PostForm(serverURL+detailPath+"/check", url.Values{
			"csrf_token": {csrfToken},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("check %d status=%d", expected, response.StatusCode)
		}
		waitForWebsiteCheckCount(t, client, serverURL, detailPath, expected)
	}

	response, err = client.Get(serverURL + detailPath)
	if err != nil {
		t.Fatal(err)
	}
	detailPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	bucketPattern := regexp.MustCompile(`class="website-availability__(?:gap|up|down)"`)
	if count := len(bucketPattern.FindAll(detailPage, -1)); count != 72 {
		t.Fatalf("detail availability buckets=%d, want 72: %s", count, detailPage)
	}
	recentStart := bytes.Index(detailPage, []byte(`<div class="website-recent-checks`))
	if recentStart < 0 {
		t.Fatalf("recent checks section missing: %s", detailPage)
	}
	tbodyStart := bytes.Index(detailPage[recentStart:], []byte("<tbody>"))
	tbodyEnd := bytes.Index(detailPage[recentStart:], []byte("</tbody>"))
	if tbodyStart < 0 || tbodyEnd <= tbodyStart {
		t.Fatalf("recent checks table body missing: %s", detailPage[recentStart:])
	}
	recentBody := detailPage[recentStart+tbodyStart : recentStart+tbodyEnd]
	if rows := bytes.Count(recentBody, []byte("<tr>")); rows != 5 {
		t.Fatalf("recent check rows=%d, want 5: %s", rows, recentBody)
	}

	response, err = client.Get(serverURL + "/monitor/websites")
	if err != nil {
		t.Fatal(err)
	}
	listPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	alertStart := bytes.Index(listPage, []byte(`class="website-alert website-alert--down"`))
	if alertStart < 0 {
		t.Fatalf("confirmed failure alert missing: %s", listPage)
	}
	alertEnd := bytes.Index(listPage[alertStart:], []byte("</section>"))
	if alertEnd < 0 {
		t.Fatalf("confirmed failure alert is incomplete: %s", listPage[alertStart:])
	}
	alert := listPage[alertStart : alertStart+alertEnd]
	requireAlignmentFragments(t, alert,
		`class="website-alert__facts"`,
		`<dt>Consecutive failures</dt><dd>5</dd>`,
		`<dt>Started</dt>`,
		`<dt>Duration</dt>`,
		`<dt>Next check</dt>`,
	)
	if times := bytes.Count(alert, []byte("<time ")); times < 2 {
		t.Fatalf("alert timestamps=%d, want incident start and next check: %s", times, alert)
	}
}

func waitForWebsiteCheckCount(
	t *testing.T,
	client *http.Client,
	serverURL string,
	detailPath string,
	want int,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var latest int
	for time.Now().Before(deadline) {
		response, err := client.Get(serverURL + detailPath + "/data")
		if err != nil {
			t.Fatal(err)
		}
		var snapshot struct {
			TotalChecks int
		}
		err = json.NewDecoder(response.Body).Decode(&snapshot)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		latest = snapshot.TotalChecks
		if latest >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("website checks=%d, want at least %d", latest, want)
}

func requireAlignmentFragments(t *testing.T, body []byte, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !bytes.Contains(body, []byte(fragment)) {
			t.Fatalf("response missing %q: %s", fragment, body)
		}
	}
}
