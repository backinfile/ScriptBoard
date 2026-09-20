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

func TestArtifactsOrderByCreationNewestFirst(t *testing.T) {
	dates := map[string]string{"a-old": "2026-01-01T00:00:00Z", "z-new": "2026-09-20T00:00:00Z", "b-tie": "2026-09-20T00:00:00Z", "unknown": ""}
	tags := []string{"a-old", "unknown", "z-new", "b-tie"}
	digests := map[string]string{}
	for i, tag := range tags {
		digests[tag] = fmt.Sprintf("sha256:%064x", i+1)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			fmt.Fprint(w, `{"tags":["a-old","unknown","z-new","b-tie"]}`)
			return
		}
		for tag, digest := range digests {
			if strings.HasSuffix(r.URL.Path, "/blobs/"+digest) {
				fmt.Fprintf(w, `{"created":%q,"os":"linux","architecture":"amd64"}`, dates[tag])
				return
			}
		}
		for tag, digest := range digests {
			if strings.HasSuffix(r.URL.Path, "/manifests/"+tag) {
				w.Header().Set("Docker-Content-Digest", digest)
				fmt.Fprintf(w, `{"config":{"digest":%q,"size":100}}`, digest)
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	items, err := New(server.Client()).Artifacts(context.Background(), Config{Endpoint: server.URL}, "team/api")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range items {
		got = append(got, item.Tag)
	}
	if !reflect.DeepEqual(got, []string{"b-tie", "z-new", "a-old", "unknown"}) {
		t.Fatalf("order=%v", got)
	}
}
