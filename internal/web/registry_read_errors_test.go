package web_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	app "scriptboard/internal/web"
	"strings"
	"testing"
)

func TestRegistryReadFailureIsNotEmpty(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer registry.Close()
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	csrf := formToken(t, getBody(t, client, base+"/resources/registries/task?task=new", 200))
	response, err := client.PostForm(base+"/resources/registries/save", url.Values{"csrf_token": {csrf}, "name": {"Unavailable"}, "endpoint": {registry.URL}, "auth_mode": {"anonymous"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location := response.Header.Get("Location")
	u, _ := url.Parse(location)
	id := u.Query().Get("connection")
	for _, path := range []string{location, "/resources/registries/task?task=detail&connection=" + id + "&repository=team/api"} {
		body := string(getBody(t, client, base+path, 200))
		if !strings.Contains(body, `role="alert"`) || strings.Contains(body, `class="registry-empty"`) || strings.Contains(body, `data-registry-selection`) {
			t.Fatalf("read failure shown as empty data: %s", body)
		}
	}
}

func TestRegistryReadOnlyConnectionUIAndEnforcement(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			t.Error("read-only performed DELETE")
		}
		if strings.Contains(r.URL.Path, "_catalog") {
			fmt.Fprint(w, `{"repositories":["team/api"]}`)
		} else if strings.Contains(r.URL.Path, "tags/list") {
			fmt.Fprint(w, `{"tags":[]}`)
		} else {
			w.WriteHeader(404)
		}
	}))
	defer registry.Close()
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	fresh := getBody(t, client, base+"/resources/registries/task?task=new", 200)
	if !strings.Contains(string(fresh), `value="read" selected`) {
		t.Fatal("new form not read-only by default")
	}
	csrf := formToken(t, fresh)
	res, err := client.PostForm(base+"/resources/registries/save", url.Values{"csrf_token": {csrf}, "name": {"Read only"}, "endpoint": {registry.URL}, "auth_mode": {"anonymous"}, "access_mode": {"readonly"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	u, _ := url.Parse(res.Header.Get("Location"))
	id := u.Query().Get("connection")
	for _, path := range []string{u.String(), "/resources/registries/task?task=detail&connection=" + id + "&repository=team/api", "/resources/registries/task?task=cleanup&connection=" + id} {
		body := string(getBody(t, client, base+path, 200))
		if strings.Contains(body, `action="/resources/registries/preview"`) || strings.Contains(body, `data-registry-preview`) {
			t.Fatalf("read-only exposes cleanup: %s", body)
		}
	}
	page := string(getBody(t, client, base+u.String(), 200))
	if !strings.Contains(page, "resource-workspace") || !strings.Contains(page, "resource-connection-rail") {
		t.Fatal("missing database-style workspace")
	}
	for _, cmd := range []string{"preview", "execute"} {
		res, err := client.PostForm(base+"/resources/registries/"+cmd, url.Values{"csrf_token": {csrf}, "connection": {id}, "repository": {"team/api"}, "access_mode": {"writable"}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("read-only %s status %d", cmd, res.StatusCode)
		}
	}
	res, err = client.PostForm(base+"/resources/registries/save", url.Values{"csrf_token": {csrf}, "connection": {id}, "name": {"Read only"}, "endpoint": {registry.URL}, "auth_mode": {"anonymous"}, "access_mode": {"writable"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	editable := getBody(t, client, base+u.String(), 200)
	if !strings.Contains(string(editable), "data-registry-preview") {
		t.Fatal("writable actions missing")
	}
	res, err = client.PostForm(base+"/resources/registries/test", url.Values{"csrf_token": {csrf}, "connection": {id}, "name": {"Read only"}, "endpoint": {registry.URL}, "auth_mode": {"anonymous"}, "access_mode": {"readonly"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(res.Body)
	res.Body.Close()
	afterTest := getBody(t, client, base+u.String(), 200)
	if !strings.Contains(string(afterTest), "data-registry-preview") {
		t.Fatal("test persisted an unsaved mode")
	}
}

func TestReviewNewRegistryMissingModeDefaultsReadOnly(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"repositories":[]}`))
	}))
	defer registry.Close()
	client, base := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state")})
	csrf := formToken(t, getBody(t, client, base+"/resources/registries/task?task=new", 200))
	response, err := client.PostForm(base+"/resources/registries/save", url.Values{"csrf_token": {csrf}, "name": {"Missing mode"}, "endpoint": {registry.URL}, "auth_mode": {"anonymous"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	u, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	id := u.Query().Get("connection")
	if id == "" {
		t.Fatalf("no connection created: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	body := string(getBody(t, client, base+"/resources/registries/task?task=edit&connection="+id, 200))
	if !strings.Contains(body, `value="read" selected`) {
		t.Fatal("new connection is writable when access_mode is omitted")
	}
}
