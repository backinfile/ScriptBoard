package registrymonitor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestManagementTagPaginationAndDeleteBearerScope(t *testing.T) {
	var endpoint string
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.URL.Query().Get("scope") != "repository:team/api:delete" {
				t.Error("wrong token scope")
			}
			fmt.Fprint(w, `{"token":"delete-token"}`)
			return
		}
		if r.Method == http.MethodDelete {
			if r.Header.Get("Authorization") != "Bearer delete-token" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+endpoint+`/token",service="registry",scope="repository:team/api:delete"`)
				w.WriteHeader(401)
				return
			}
			deleted = true
			w.WriteHeader(202)
			return
		}
		if r.URL.Query().Get("last") == "v1" {
			fmt.Fprint(w, `{"tags":["v2"]}`)
			return
		}
		w.Header().Set("Link", `</v2/team/api/tags/list?n=1&last=v1>; rel="next"`)
		fmt.Fprint(w, `{"tags":["v1"]}`)
	}))
	defer server.Close()
	endpoint = server.URL
	client := New(server.Client())
	config := Config{Endpoint: endpoint, AuthMode: "anonymous"}
	tags, err := client.managementTags(context.Background(), config, "team/api")
	if err != nil || !reflect.DeepEqual(tags, []string{"v1", "v2"}) {
		t.Fatalf("tags %v %v", tags, err)
	}
	err = client.DeleteManifest(context.Background(), config, DeleteTarget{Repository: "team/api", Digest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil || !deleted {
		t.Fatalf("delete %v, called=%v", err, deleted)
	}
}

func TestArtifactsIndexPlatformSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			fmt.Fprint(w, `{"tags":["latest"]}`)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("a", 64))
		fmt.Fprint(w, `{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"platform":{"os":"linux","architecture":"arm64","variant":"v8"}},{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"unknown","architecture":"unknown"}},{"platform":{"os":"linux","architecture":"amd64"}}]}`)
	}))
	defer server.Close()
	client := New(server.Client())
	items, err := client.Artifacts(context.Background(), Config{Endpoint: server.URL}, "team/api")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !reflect.DeepEqual(items[0].Platforms, []string{"linux/arm64/v8", "linux/amd64"}) || items[0].Size != 0 || !items[0].Created.IsZero() {
		t.Fatalf("incorrect index summary: %+v", items)
	}
}

func TestManagementSingleManifestIncludesPlatform(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tags/list") {
			fmt.Fprint(w, "{\"tags\":[\"latest\"]}")
			return
		}
		if strings.Contains(r.URL.Path, "blobs/") {
			fmt.Fprint(w, "{\"os\":\"linux\",\"architecture\":\"arm64\",\"variant\":\"v8\",\"created\":\"2026-09-20T00:00:00Z\"}")
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("b", 64))
		fmt.Fprintf(w, "{\"config\":{\"digest\":\"sha256:%s\",\"size\":100},\"layers\":[{\"size\":900}]}", strings.Repeat("a", 64))
	}))
	defer server.Close()
	client := New(server.Client())
	items, err := client.Artifacts(context.Background(), Config{Endpoint: server.URL}, "team/api")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !reflect.DeepEqual(items[0].Platforms, []string{"linux/arm64/v8"}) || items[0].Size != 1000 || items[0].Created.IsZero() {
		t.Fatalf("items=%+v", items)
	}
}
