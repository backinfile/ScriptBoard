package web_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	app "scriptboard/internal/web"
)

func TestRegistryManagementDrawerFlowAndCSRF(t *testing.T) {
	deleted := false
	digest := "sha256:" + strings.Repeat("a", 64)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			fmt.Fprint(w, `{"repositories":["team-a/api","team-ab/api"]}`)
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			fmt.Fprint(w, `{"tags":["v1","latest"]}`)
		case r.Method == "DELETE":
			deleted = true
			w.WriteHeader(202)
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Docker-Content-Digest", digest)
			fmt.Fprint(w, `{"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	body := getBody(t, client, base+"/resources/registries/task?task=new", 200)
	csrf := formToken(t, body)
	if !strings.Contains(string(body), `data-task-page`) || !strings.Contains(string(body), `<form data-async`) {
		t.Fatal("connection form is not an async task")
	}
	res, e := client.PostForm(base+"/resources/registries/save", url.Values{"name": {"Main"}, "endpoint": {registry.URL}})
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("missing CSRF accepted")
	}
	res, e = client.PostForm(base+"/resources/registries/save", url.Values{"csrf_token": {csrf}, "name": {"Main"}, "endpoint": {registry.URL}, "auth_mode": {"anonymous"}, "username": {"ignored-user"}, "access_mode": {"write"}})
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Fatalf("save %d", res.StatusCode)
	}
	destination := res.Header.Get("Location")
	u, _ := url.Parse(destination)
	id := u.Query().Get("connection")
	page := getBody(t, client, base+destination+"&namespace=team-a%2F", 200)
	if !strings.Contains(string(page), "team-a/api") || strings.Contains(string(page), "team-ab/api") {
		t.Fatal("namespace scope mismatch")
	}
	if !strings.Contains(string(page), "data-task-link") {
		t.Fatal("missing drawer links")
	}
	detail := getBody(t, client, base+"/resources/registries/task?task=detail&connection="+id+"&repository=team-a%2Fapi", 200)
	if !strings.Contains(string(detail), digest) {
		t.Fatal("missing digest details")
	}
	res, e = client.PostForm(base+"/resources/registries/preview", url.Values{"csrf_token": {csrf}, "connection": {id}, "prefix": {"team-a/"}})
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Fatalf("preview %d", res.StatusCode)
	}
	planURL := res.Header.Get("Location")
	u, _ = url.Parse(planURL)
	plan := u.Query().Get("plan")
	planBody := getBody(t, client, base+planURL, 200)
	if !strings.Contains(string(planBody), "latest, v1") || strings.Contains(string(planBody), "team-ab/api") {
		t.Fatal("inaccurate deletion preview")
	}
	res, e = client.PostForm(base+"/resources/registries/execute", url.Values{"csrf_token": {csrf}, "connection": {id}, "plan": {plan}, "confirmation": {"wrong"}})
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 422 || deleted {
		t.Fatal("confirmation bypass")
	}
	res, e = client.PostForm(base+"/resources/registries/execute", url.Values{"csrf_token": {csrf}, "connection": {id}, "plan": {plan}, "confirmation": {"Main"}})
	if e != nil {
		t.Fatal(e)
	}
	result, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !deleted || !strings.Contains(string(result), "data-task-page") {
		t.Fatalf("execute %d %s", res.StatusCode, result)
	}
	getBody(t, client, base+destination+"&tab=history", 200)
}

func TestRegistryInventoryPagination(t *testing.T) {
	repositories := make([]string, 25)
	for i := range repositories {
		repositories[i] = fmt.Sprintf("jiangnan/main/server-%02d", i)
	}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/_catalog" {
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repositories})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			fmt.Fprint(w, `{"tags":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer registry.Close()
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	csrf := formToken(t, getBody(t, client, base+"/resources/registries/task?task=new", 200))
	response, err := client.PostForm(base+"/resources/registries/save", url.Values{"csrf_token": {csrf}, "name": {"Pages"}, "endpoint": {registry.URL}, "auth_mode": {"anonymous"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	destination := response.Header.Get("Location") + "&namespace=jiangnan%2Fmain%2F&query=server"
	first := string(getBody(t, client, base+destination, 200))
	second := string(getBody(t, client, base+destination+"&page=2", 200))
	if strings.Count(first, `class="registry-image-link"`) != 20 || strings.Count(second, `class="registry-image-link"`) != 5 {
		t.Fatal("incorrect page sizes")
	}
	if !strings.Contains(second, "server-24") || strings.Contains(second, "server-00") {
		t.Fatal("incorrect page contents")
	}
	invalid := string(getBody(t, client, base+destination+"&page=999999999999999999999", 200))
	if strings.Count(invalid, `class="registry-image-link"`) != 20 {
		t.Fatal("invalid page did not reset")
	}
	empty := string(getBody(t, client, base+destination+"-missing", 200))
	if strings.Contains(empty, `class="registry-image-link"`) {
		t.Fatal("empty search returned images")
	}
}
