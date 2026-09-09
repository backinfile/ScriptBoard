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
