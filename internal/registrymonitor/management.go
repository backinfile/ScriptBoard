package registrymonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json"

type ManagedConnection struct {
	ID     string
	Name   string
	Config Config
}
type Artifact struct {
	Repository string
	Tag        string
	Digest     string
	Created    time.Time
	MediaType  string
	Size       int64
}
type DeleteTarget struct {
	Repository string
	Digest     string
	Tags       []string
}
type CleanupRule struct {
	Prefix    string
	Pattern   string
	OlderDays int
	Keep      int
	Protect   bool
}
type ManagementRequest struct {
	Command      string
	ID           string
	Name         string
	Config       Config
	Password     string
	Preserve     bool
	Repository   string
	Repositories []string
	Tag          string
	Rule         CleanupRule
	PlanID       string
	Confirmation string
}
type DeletePlan struct {
	ID           string
	ConnectionID string
	Revision     string
	Created      time.Time
	Targets      []DeleteTarget
	Confirmation string
	Revisions    map[string]string
}
type ManagementEvent struct {
	Time         time.Time
	ConnectionID string
	Summary      string
	Results      []DeleteResult
}
type DeleteResult struct {
	Repository string
	Digest     string
	Error      string
}
type ManagementResponse struct {
	Connections  []ManagedConnection
	Repositories []string
	Artifacts    []Artifact
	Plan         *DeletePlan
	Events       []ManagementEvent
	Results      []DeleteResult
	ID           string
}
type ManagementBackend interface {
	Manage(context.Context, ManagementRequest) (ManagementResponse, error)
}

// Repositories refuses an incomplete catalog; a cleanup must never interpret a
// truncated monitoring snapshot as the complete namespace.
func (client *Client) Repositories(ctx context.Context, config Config) ([]string, error) {
	return client.catalog(ctx, config)
}

func (client *Client) Artifacts(ctx context.Context, config Config, repository string) ([]Artifact, error) {
	if !imagePattern.MatchString(repository) {
		return nil, errors.New("invalid repository")
	}
	tags, err := client.managementTags(ctx, config, repository)
	if err != nil {
		return nil, err
	}
	artifacts := make([]Artifact, 0, len(tags))
	for _, tag := range tags {
		endpoint := config.Endpoint + "/v2/" + escapeRepository(repository) + "/manifests/" + url.PathEscape(tag)
		response, err := client.doAuthenticatedAccept(ctx, endpoint, config, manifestAccept)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("manifest HTTP %d", response.StatusCode)
		}
		var doc struct {
			MediaType string `json:"mediaType"`
			Config    struct {
				Digest string `json:"digest"`
				Size   int64  `json:"size"`
			} `json:"config"`
			Layers []struct {
				Size int64 `json:"size"`
			} `json:"layers"`
		}
		digest := response.Header.Get("Docker-Content-Digest")
		err = decodeJSON(response.Body, &doc)
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if !digestPattern.MatchString(digest) {
			return nil, errors.New("Registry did not return a valid manifest digest")
		}
		item := Artifact{Repository: repository, Tag: tag, Digest: digest, MediaType: doc.MediaType, Size: doc.Config.Size}
		for _, layer := range doc.Layers {
			item.Size += layer.Size
		}
		if doc.Config.Digest != "" {
			item.Created, _ = client.registryImageCreatedTime(ctx, config, repository, doc.Config.Digest)
		}
		artifacts = append(artifacts, item)
	}
	return artifacts, nil
}

func (client *Client) managementTags(ctx context.Context, config Config, repository string) ([]string, error) {
	endpoint, _ := url.Parse(config.Endpoint)
	next, _ := url.Parse(config.Endpoint + "/v2/" + escapeRepository(repository) + "/tags/list?n=100")
	path := next.Path
	seen := map[string]bool{}
	tags := []string{}
	unique := map[string]bool{}
	for {
		if len(seen) >= 100 || seen[next.String()] {
			return nil, errors.New("tag pagination is incomplete or cyclic")
		}
		seen[next.String()] = true
		response, err := client.doAuthenticated(ctx, next.String(), config)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return nil, fmt.Errorf("tags HTTP %d", response.StatusCode)
		}
		var doc struct {
			Tags []string `json:"tags"`
		}
		err = decodeJSON(response.Body, &doc)
		link := strings.Join(response.Header.Values("Link"), ",")
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, tag := range doc.Tags {
			if !unique[tag] {
				if len(tags) >= 1000 {
					return nil, errors.New("too many tags; narrow the cleanup scope")
				}
				unique[tag] = true
				tags = append(tags, tag)
			}
		}
		if link == "" {
			sort.Strings(tags)
			return tags, nil
		}
		next, err = resolveCatalogLink(link, next, endpoint, path)
		if err != nil {
			return nil, err
		}
	}
}

func (client *Client) DeleteManifest(ctx context.Context, config Config, target DeleteTarget) error {
	if !imagePattern.MatchString(target.Repository) || !digestPattern.MatchString(target.Digest) {
		return errors.New("invalid deletion target")
	}
	response, err := client.doAuthenticatedMethod(ctx, http.MethodDelete, config.Endpoint+"/v2/"+escapeRepository(target.Repository)+"/manifests/"+url.PathEscape(target.Digest), config, manifestAccept)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 202 && response.StatusCode != 404 {
		return fmt.Errorf("delete HTTP %d; check Registry deletion support and permissions", response.StatusCode)
	}
	return nil
}

func ArtifactRevision(items []Artifact) string {
	ordered := append([]Artifact(nil), items...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Tag < ordered[j].Tag })
	h := sha256.New()
	for _, a := range ordered {
		fmt.Fprintf(h, "%s\x00%s\x00%s\n", a.Repository, a.Tag, a.Digest)
	}
	return hex.EncodeToString(h.Sum(nil))
}
