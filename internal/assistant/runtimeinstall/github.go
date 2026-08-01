package runtimeinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"scriptboard/internal/buildinfo"
)

type Remote struct {
	ManifestRaw, SignatureRaw []byte
	Manifest                  Manifest
	ReleaseTag, ReleaseURL    string
	AssetURLs                 map[string]string
	AssetSizes                map[string]int64
}

type Source interface {
	Fetch(context.Context, Compatibility) (Remote, error)
	Open(context.Context, string, string, Asset) (io.ReadCloser, error)
}

type GitHubSource struct{ Client *http.Client }

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	HTMLURL    string `json:"html_url"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

func NewGitHubSource() *GitHubSource {
	client := &http.Client{Timeout: 45 * time.Second}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many assistant runtime redirects")
		}
		if request.URL.Scheme != "https" || request.URL.User != nil || (request.URL.Port() != "" && request.URL.Port() != "443") || !allowedRuntimeDownloadHost(request.URL.Hostname()) {
			return fmt.Errorf("assistant runtime redirect host %q is not allowed", request.URL.Hostname())
		}
		request.Header.Del("Authorization")
		return nil
	}
	return &GitHubSource{Client: client}
}

func (source *GitHubSource) Fetch(ctx context.Context, compatibility Compatibility) (Remote, error) {
	if !stableVersionPattern.MatchString(compatibility.ScriptBoardVersion) || compatibility.ScriptBoardTag != "v"+compatibility.ScriptBoardVersion {
		return Remote{}, errors.New("development builds do not fetch assistant runtimes")
	}
	apiURL := "https://api.github.com/repos/" + buildinfo.Repository + "/releases/tags/" + compatibility.ScriptBoardTag
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Remote{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ScriptBoard/"+compatibility.ScriptBoardVersion)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := source.client().Do(request)
	if err != nil {
		return Remote{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Remote{}, fmt.Errorf("GitHub runtime release lookup returned %s", response.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return Remote{}, fmt.Errorf("decode GitHub runtime release: %w", err)
	}
	if release.Draft || release.Prerelease || release.TagName != compatibility.ScriptBoardTag {
		return Remote{}, errors.New("GitHub runtime release does not match this ScriptBoard release")
	}
	if err := validateRuntimeReleasePageURL(release.HTMLURL, release.TagName); err != nil {
		return Remote{}, err
	}
	urls := make(map[string]string, len(release.Assets))
	sizes := make(map[string]int64, len(release.Assets))
	for _, asset := range release.Assets {
		if _, duplicate := urls[asset.Name]; duplicate {
			return Remote{}, fmt.Errorf("GitHub release contains duplicate asset %q", asset.Name)
		}
		if err := validateRuntimeReleaseAssetURL(asset.BrowserDownloadURL, release.TagName, asset.Name); err != nil {
			return Remote{}, err
		}
		urls[asset.Name] = asset.BrowserDownloadURL
		sizes[asset.Name] = asset.Size
	}
	manifestURL, manifestOK := urls[ManifestFilename]
	signatureURL, signatureOK := urls[SignatureFilename]
	if !manifestOK || !signatureOK {
		return Remote{}, errors.New("GitHub release is missing signed assistant runtime metadata")
	}
	manifestRaw, err := source.downloadSmall(ctx, manifestURL, MaxManifestBytes)
	if err != nil {
		return Remote{}, err
	}
	signatureRaw, err := source.downloadSmall(ctx, signatureURL, MaxSignatureBytes)
	if err != nil {
		return Remote{}, err
	}
	return Remote{
		ManifestRaw: manifestRaw, SignatureRaw: signatureRaw, ReleaseTag: release.TagName, ReleaseURL: release.HTMLURL,
		AssetURLs: urls, AssetSizes: sizes,
	}, nil
}

func (source *GitHubSource) Open(ctx context.Context, rawURL, releaseTag string, asset Asset) (io.ReadCloser, error) {
	if err := validateRuntimeReleaseAssetURL(rawURL, releaseTag, asset.Name); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "ScriptBoard/"+buildinfo.Current().Version)
	response, err := source.client().Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("assistant runtime download returned %s", response.Status)
	}
	if response.ContentLength >= 0 && response.ContentLength != asset.Size {
		response.Body.Close()
		return nil, errors.New("assistant runtime Content-Length does not match signed manifest")
	}
	return response.Body, nil
}

func (source *GitHubSource) downloadSmall(ctx context.Context, rawURL string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "ScriptBoard/"+buildinfo.Current().Version)
	response, err := source.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("assistant runtime metadata download returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maximum {
		return nil, errors.New("assistant runtime metadata exceeds size limit")
	}
	return raw, nil
}

func (source *GitHubSource) client() *http.Client {
	if source.Client != nil {
		return source.Client
	}
	return NewGitHubSource().Client
}

func validateRuntimeReleaseAssetURL(rawURL, tag, name string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("assistant runtime asset URL is not an official GitHub download URL")
	}
	if parsed.EscapedPath() != path.Join("/", buildinfo.Repository, "releases", "download", tag, name) {
		return errors.New("assistant runtime asset URL does not match the ScriptBoard release")
	}
	return nil
}

func validateRuntimeReleasePageURL(rawURL, tag string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("assistant runtime release URL is not an official GitHub URL")
	}
	if parsed.EscapedPath() != path.Join("/", buildinfo.Repository, "releases", "tag", tag) {
		return errors.New("assistant runtime release URL does not match the ScriptBoard release")
	}
	return nil
}

func allowedRuntimeDownloadHost(host string) bool {
	switch strings.ToLower(host) {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	default:
		return false
	}
}
