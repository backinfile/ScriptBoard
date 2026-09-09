package registryconnection

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/registrymonitor"
)

type managementRegistry struct {
	mu      sync.Mutex
	tags    map[string]map[string]string
	deleted []string
	fail    bool
	cycle   bool
}

func (f *managementRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/v2/_catalog" {
		repos := []string{}
		for repo := range f.tags {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/tags/list") {
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/"), "/tags/list")
		tags := []string{}
		for tag := range f.tags[repo] {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		if f.cycle {
			w.Header().Set("Link", "<"+r.URL.Path+"?n=100>; rel=\"next\"")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tags": tags})
		return
	}
	if strings.Contains(r.URL.Path, "/manifests/") {
		repo, ref, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/manifests/")
		if r.Method == "DELETE" {
			if f.fail && strings.Contains(repo, "worker") {
				http.Error(w, "blocked", 405)
				return
			}
			f.deleted = append(f.deleted, repo+"@"+ref)
			for tag, d := range f.tags[repo] {
				if d == ref {
					delete(f.tags[repo], tag)
				}
			}
			w.WriteHeader(202)
			return
		}
		digest := f.tags[repo][ref]
		if digest == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest)
		fmt.Fprint(w, `{"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"sha256:`+strings.Repeat("c", 64)+`","size":25},"layers":[{"size":100}]}`)
		return
	}
	if strings.Contains(r.URL.Path, "/blobs/") {
		fmt.Fprint(w, `{"created":"2020-01-01T00:00:00Z"}`)
		return
	}
	http.NotFound(w, r)
}
func fixtureRegistry() *managementRegistry {
	d := "sha256:" + strings.Repeat("a", 64)
	return &managementRegistry{tags: map[string]map[string]string{"team-a/api": {"v1": d, "latest": d}, "team-a/jobs/worker": {"dev-1": d}, "team-ab/api": {"stable": d}}}
}
func setupManagement(t *testing.T, f *managementRegistry) (*Service, string) {
	t.Helper()
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	service, e := New(Options{StateRoot: t.TempDir(), Client: server.Client()})
	if e != nil {
		t.Fatal(e)
	}
	out, e := service.Manage(context.Background(), registrymonitor.ManagementRequest{Command: "save", Name: "Test registry", Config: registrymonitor.Config{Endpoint: server.URL, AuthMode: "anonymous"}})
	if e != nil {
		t.Fatal(e)
	}
	return service, out.ID
}
func TestManagementPrefixPreviewExecutionAndReplay(t *testing.T) {
	f := fixtureRegistry()
	service, id := setupManagement(t, f)
	ctx := context.Background()
	out, err := service.Manage(ctx, registrymonitor.ManagementRequest{Command: "preview", ID: id, Rule: registrymonitor.CleanupRule{Prefix: "team-a/"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Plan.Targets) != 2 || len(out.Plan.Targets[0].Tags) != 2 {
		t.Fatalf("unexpected targets %+v", out.Plan.Targets)
	}
	req := registrymonitor.ManagementRequest{Command: "execute", ID: id, PlanID: out.Plan.ID, Confirmation: "wrong"}
	if _, e := service.Manage(ctx, req); e == nil {
		t.Fatal("accepted wrong confirmation")
	}
	req.Confirmation = "Test registry"
	result, err := service.Manage(ctx, req)
	if err != nil || len(result.Results) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, r := range result.Results {
		if r.Error != "" {
			t.Fatal(r.Error)
		}
	}
	if len(f.deleted) != 2 || len(f.tags["team-ab/api"]) != 1 {
		t.Fatalf("wrong namespace deleted %+v", f.deleted)
	}
	if _, e := service.Manage(ctx, req); e == nil {
		t.Fatal("replayed deletion")
	}
	history, e := service.Manage(ctx, registrymonitor.ManagementRequest{Command: "history", ID: id})
	if e != nil || len(history.Events) != 1 || len(history.Events[0].Results) != 2 {
		t.Fatalf("history %+v %v", history, e)
	}
}
func TestManagementRejectsChangedTagsAndConnection(t *testing.T) {
	for _, kind := range []string{"tag", "connection"} {
		t.Run(kind, func(t *testing.T) {
			f := fixtureRegistry()
			service, id := setupManagement(t, f)
			ctx := context.Background()
			out, e := service.Manage(ctx, registrymonitor.ManagementRequest{Command: "preview", ID: id, Repository: "team-a/api", Tag: "v1"})
			if e != nil {
				t.Fatal(e)
			}
			if kind == "tag" {
				f.mu.Lock()
				f.tags["team-a/api"]["new"] = "sha256:" + strings.Repeat("b", 64)
				f.mu.Unlock()
			} else {
				list, _ := service.Manage(ctx, registrymonitor.ManagementRequest{Command: "list"})
				_, e = service.Manage(ctx, registrymonitor.ManagementRequest{Command: "save", ID: id, Name: "Edited", Config: list.Connections[0].Config})
				if e != nil {
					t.Fatal(e)
				}
			}
			if _, e = service.Manage(ctx, registrymonitor.ManagementRequest{Command: "execute", ID: id, PlanID: out.Plan.ID, Confirmation: "Test registry"}); e == nil {
				t.Fatal("accepted stale preview")
			}
			if len(f.deleted) > 0 {
				t.Fatal("deleted before validation")
			}
		})
	}
}
func TestManagementProtectsSharedDigestAndReportsPartialFailure(t *testing.T) {
	f := fixtureRegistry()
	service, id := setupManagement(t, f)
	ctx := context.Background()
	out, e := service.Manage(ctx, registrymonitor.ManagementRequest{Command: "preview", ID: id, Rule: registrymonitor.CleanupRule{Prefix: "team-a/", Protect: true}})
	if e != nil || len(out.Plan.Targets) != 1 || out.Plan.Targets[0].Repository != "team-a/jobs/worker" {
		t.Fatalf("protected %+v %v", out, e)
	}
	out, e = service.Manage(ctx, registrymonitor.ManagementRequest{Command: "preview", ID: id, Rule: registrymonitor.CleanupRule{Prefix: "team-a/"}})
	if e != nil {
		t.Fatal(e)
	}
	f.fail = true
	out, e = service.Manage(ctx, registrymonitor.ManagementRequest{Command: "execute", ID: id, PlanID: out.Plan.ID, Confirmation: "Test registry"})
	if e != nil || len(out.Results) != 2 || out.Results[0].Error != "" || out.Results[1].Error == "" {
		t.Fatalf("partial %+v %v", out, e)
	}
}
func TestManagementPaginationFailureDoesNotProducePlan(t *testing.T) {
	f := fixtureRegistry()
	f.cycle = true
	service, id := setupManagement(t, f)
	out, e := service.Manage(context.Background(), registrymonitor.ManagementRequest{Command: "preview", ID: id, Rule: registrymonitor.CleanupRule{Prefix: "team-a/"}})
	if e == nil || out.Plan != nil {
		t.Fatalf("incomplete plan %+v %v", out, e)
	}
}
func TestManagementTLSModes(t *testing.T) {
	for _, mode := range []string{"http", "verified", "untrusted", "skip"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtureRegistry()
			server := httptest.NewUnstartedServer(f)
			if mode == "http" {
				server.Start()
			} else {
				server.StartTLS()
			}
			defer server.Close()
			var client *http.Client
			if mode == "verified" {
				client = server.Client()
			}
			service, e := New(Options{StateRoot: t.TempDir(), Client: client})
			if e != nil {
				t.Fatal(e)
			}
			out, e := service.Manage(context.Background(), registrymonitor.ManagementRequest{Command: "save", Name: "TLS", Config: registrymonitor.Config{Endpoint: server.URL, SkipTLSVerify: mode == "skip"}})
			if e != nil {
				t.Fatal(e)
			}
			out, e = service.Manage(context.Background(), registrymonitor.ManagementRequest{Command: "catalog", ID: out.ID})
			if mode == "untrusted" {
				if e == nil {
					t.Fatal("trusted an unverified certificate")
				}
			} else if e != nil || len(out.Repositories) != 3 {
				t.Fatalf("%+v %v", out, e)
			}
		})
	}
}

func TestManagementExpiryAndStorageIsolation(t *testing.T) {
	f := fixtureRegistry()
	service, id := setupManagement(t, f)
	ctx := context.Background()
	if configured, err := service.Configured(ctx, id); err != nil || configured {
		t.Fatalf("management connection leaked into card store: %v %v", configured, err)
	}
	out, err := service.Manage(ctx, registrymonitor.ManagementRequest{Command: "preview", ID: id, Repository: "team-a/api"})
	if err != nil {
		t.Fatal(err)
	}
	store := &Service{path: service.path + ".management", vault: service.vault}
	state, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	plan := state.Plans[out.Plan.ID]
	plan.Created = time.Now().Add(-11 * time.Minute)
	state.Plans[plan.ID] = plan
	for i := 0; i < 12; i++ {
		state.Events = append(state.Events, registrymonitor.ManagementEvent{Summary: strings.Repeat("history", 15000)})
	}
	if err := store.writeManagement(state); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.load()
	if err != nil || len(reloaded.Events) >= 12 || len(reloaded.Active) != 1 {
		t.Fatalf("storage budget corrupted connections: %+v %v", reloaded, err)
	}
	if _, err := service.Manage(ctx, registrymonitor.ManagementRequest{Command: "execute", ID: id, PlanID: plan.ID, Confirmation: "Test registry"}); err == nil {
		t.Fatal("accepted expired plan")
	}
	if len(f.deleted) != 0 {
		t.Fatal("expired plan deleted an image")
	}
}
