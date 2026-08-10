package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"scriptboard/internal/buildinfo"
)

const githubLatestReleaseURL = "https://api.github.com/repos/" + buildinfo.Repository + "/releases/latest"

type RemoteRelease struct {
	NotModified  bool
	ETag         string
	ReleaseURL   string
	ReleaseNotes string
	ManifestRaw  []byte
	SignatureRaw []byte
	Manifest     Manifest
	AssetURLs    map[string]string
}

type ReleaseSource interface {
	Check(context.Context, string) (RemoteRelease, error)
	Download(context.Context, string, string, Asset) error
}

type GitHubSource struct {
	Client       *http.Client
	APIURL       string
	proxyBaseURL string
	proxyAPI     bool
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	HTMLURL    string `json:"html_url"`
	Body       string `json:"body"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

func NewGitHubSource() *GitHubSource {
	return newGitHubProxySource("", false)
}

func newGitHubProxySource(proxyBaseURL string, proxyAPI bool) *GitHubSource {
	proxyHost := ""
	if proxyBaseURL != "" {
		if parsed, err := url.Parse(proxyBaseURL); err == nil {
			proxyHost = parsed.Hostname()
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many release download redirects")
		}
		if request.URL.Scheme != "https" || request.URL.User != nil ||
			(request.URL.Port() != "" && request.URL.Port() != "443") ||
			!allowedSourceHost(request.URL.Hostname(), proxyHost) {
			return fmt.Errorf("release redirect host %q is not allowed", request.URL.Hostname())
		}
		request.Header.Del("Authorization")
		return nil
	}
	return &GitHubSource{Client: client, APIURL: githubLatestReleaseURL, proxyBaseURL: proxyBaseURL, proxyAPI: proxyAPI}
}

func (source *GitHubSource) Check(ctx context.Context, etag string) (RemoteRelease, error) {
	apiURL := source.APIURL
	if apiURL == "" {
		apiURL = githubLatestReleaseURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.requestURL(apiURL, true), nil)
	if err != nil {
		return RemoteRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ScriptBoard/"+buildinfo.Current().Version)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := source.client().Do(request)
	if err != nil {
		return RemoteRelease{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return RemoteRelease{NotModified: true, ETag: response.Header.Get("ETag")}, nil
	}
	if response.StatusCode != http.StatusOK {
		return RemoteRelease{}, fmt.Errorf("GitHub Releases API returned %s", response.Status)
	}
	var release githubRelease
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&release); err != nil {
		return RemoteRelease{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	if release.Draft || release.Prerelease || release.TagName == "" {
		return RemoteRelease{}, errors.New("GitHub latest release is not a stable published release")
	}
	if err := validateReleasePageURL(release.HTMLURL, release.TagName); err != nil {
		return RemoteRelease{}, err
	}
	urls := make(map[string]string, len(release.Assets))
	sizes := make(map[string]int64, len(release.Assets))
	for _, asset := range release.Assets {
		if _, duplicate := urls[asset.Name]; duplicate {
			return RemoteRelease{}, fmt.Errorf("release contains duplicate asset %q", asset.Name)
		}
		if err := validateReleaseAssetURL(asset.BrowserDownloadURL, release.TagName, asset.Name); err != nil {
			return RemoteRelease{}, err
		}
		urls[asset.Name] = asset.BrowserDownloadURL
		sizes[asset.Name] = asset.Size
	}
	manifestURL, ok := urls[ManifestFilename]
	if !ok {
		return RemoteRelease{}, errors.New("release is missing release-manifest.json")
	}
	signatureURL, ok := urls[SignatureFilename]
	if !ok {
		return RemoteRelease{}, errors.New("release is missing release-manifest.json.sig")
	}
	manifestRaw, err := source.downloadSmall(ctx, manifestURL, MaxManifestBytes)
	if err != nil {
		return RemoteRelease{}, err
	}
	signatureRaw, err := source.downloadSmall(ctx, signatureURL, MaxSignatureBytes)
	if err != nil {
		return RemoteRelease{}, err
	}
	manifest, err := VerifyTrustedManifest(manifestRaw, signatureRaw)
	if err != nil {
		return RemoteRelease{}, err
	}
	if manifest.Tag != release.TagName {
		return RemoteRelease{}, errors.New("GitHub release tag does not match signed manifest")
	}
	for _, asset := range manifest.Assets {
		if urls[asset.Name] == "" || sizes[asset.Name] != asset.Size {
			return RemoteRelease{}, fmt.Errorf("GitHub asset %q does not match signed manifest", asset.Name)
		}
	}
	notes := release.Body
	if len(notes) > 64<<10 {
		notes = notes[:64<<10]
	}
	return RemoteRelease{
		ETag: response.Header.Get("ETag"), ReleaseURL: release.HTMLURL, ReleaseNotes: notes,
		ManifestRaw: manifestRaw, SignatureRaw: signatureRaw, Manifest: manifest, AssetURLs: urls,
	}, nil
}

func (source *GitHubSource) Download(ctx context.Context, downloadURL, destination string, asset Asset) error {
	if err := validateReleaseAssetURL(downloadURL, "v"+assetNameVersion(asset.Name), asset.Name); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.requestURL(downloadURL, false), nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "ScriptBoard/"+buildinfo.Current().Version)
	response, err := source.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release asset download returned %s", response.Status)
	}
	if response.ContentLength >= 0 && response.ContentLength != asset.Size {
		return errors.New("release asset Content-Length does not match signed manifest")
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, asset.Size+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if written != asset.Size || written > MaxArchiveBytes {
		_ = os.Remove(destination)
		return errors.New("downloaded release asset size does not match signed manifest")
	}
	if hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		_ = os.Remove(destination)
		return errors.New("downloaded release asset SHA-256 does not match signed manifest")
	}
	if syncErr != nil {
		_ = os.Remove(destination)
		return syncErr
	}
	return closeErr
}

func (source *GitHubSource) downloadSmall(ctx context.Context, downloadURL string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.requestURL(downloadURL, false), nil)
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
		return nil, fmt.Errorf("release metadata download returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maximum {
		return nil, errors.New("release metadata exceeds size limit")
	}
	return raw, nil
}

func (source *GitHubSource) client() *http.Client {
	if source.Client != nil {
		return source.Client
	}
	return NewGitHubSource().Client
}

func (source *GitHubSource) requestURL(rawURL string, api bool) string {
	if source.proxyBaseURL == "" || (api && !source.proxyAPI) {
		return rawURL
	}
	return strings.TrimRight(source.proxyBaseURL, "/") + "/" + rawURL
}

func validateReleaseAssetURL(rawURL, tag, name string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("release asset URL %q is not an official GitHub download URL", rawURL)
	}
	wantPath := path.Join("/", buildinfo.Repository, "releases", "download", tag, name)
	if parsed.EscapedPath() != wantPath {
		return fmt.Errorf("release asset URL %q does not match the signed release", rawURL)
	}
	return nil
}

func validateReleasePageURL(rawURL, tag string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("release page URL %q is not an official GitHub release URL", rawURL)
	}
	wantPath := path.Join("/", buildinfo.Repository, "releases", "tag", tag)
	if parsed.EscapedPath() != wantPath {
		return fmt.Errorf("release page URL %q does not match the signed release", rawURL)
	}
	return nil
}

func allowedDownloadHost(host string) bool {
	switch strings.ToLower(host) {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	default:
		return false
	}
}

func allowedSourceHost(host, proxyHost string) bool {
	return allowedDownloadHost(host) || (proxyHost != "" && strings.EqualFold(host, proxyHost))
}

func assetNameVersion(name string) string {
	const prefix = "scriptboard-v"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(name, prefix)
	if index := strings.Index(remainder, "-"); index >= 0 {
		return remainder[:index]
	}
	return ""
}
