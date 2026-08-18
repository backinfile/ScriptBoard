package registrymonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBytes       = 2 << 20
	defaultInspectTimeout  = 20 * time.Second
	maxCatalogPages        = 100
	maxCatalogRepositories = 480
	maxExpandedImages      = 500
	ImageTimePushed        = "pushed"
	ImageTimeCreated       = "created"
)

var imagePattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._-][a-z0-9]+)|(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*))*$`)

var digestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[+._-][a-z0-9]+)*:[a-fA-F0-9]{32,}$`)

type Config struct {
	Endpoint string   `json:"endpoint"`
	Images   []string `json:"images"`
	AuthMode string   `json:"authMode,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"-"`
}

type ImageResult struct {
	Image string `json:"image"`
	Tag   string `json:"tag,omitempty"`
	// PushedAt and PushTimeAvailable keep the persisted snapshot contract. TimeSource
	// distinguishes an actual Registry push time from the OCI image creation fallback.
	PushedAt          time.Time `json:"pushedAt,omitempty"`
	PushTimeAvailable bool      `json:"pushTimeAvailable,omitempty"`
	TimeSource        string    `json:"timeSource,omitempty"`
	Error             string    `json:"error,omitempty"`
	Stale             bool      `json:"stale,omitempty"`
}

type Client struct {
	client         *http.Client
	inspectTimeout time.Duration
}

func New(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{client: client, inspectTimeout: defaultInspectTimeout}
}

func NormalizeConfig(config Config) Config {
	config.Endpoint = strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	config.Username = strings.TrimSpace(config.Username)
	if config.AuthMode == "" {
		if config.Username != "" || config.Password != "" {
			config.AuthMode = "basic"
		} else {
			config.AuthMode = "anonymous"
		}
	}
	images := make([]string, 0, len(config.Images))
	seen := map[string]bool{}
	for _, image := range config.Images {
		image = strings.Trim(strings.ToLower(strings.TrimSpace(image)), "/")
		if image != "" && !seen[image] {
			images = append(images, image)
			seen[image] = true
		}
	}
	config.Images = images
	return config
}

func ValidateConfig(config Config) error {
	config = NormalizeConfig(config)
	parsed, err := url.Parse(config.Endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Registry 地址必须使用 HTTP 或 HTTPS")
	}
	if len(config.Images) == 0 {
		return errors.New("请至少填写一个镜像")
	}
	if len(config.Images) > 20 {
		return errors.New("一张卡片最多查询 20 个镜像")
	}
	for _, image := range config.Images {
		if !imagePattern.MatchString(image) && !isImageSelector(image) {
			return fmt.Errorf("镜像名称无效：%s（请勿包含仓库地址、标签或摘要）", image)
		}
	}
	if config.AuthMode != "anonymous" && config.AuthMode != "basic" {
		return errors.New("不支持的 Registry 认证方式")
	}
	if config.AuthMode == "basic" && config.Username == "" {
		return errors.New("用户名不能为空")
	}
	return nil
}

func (client *Client) Inspect(ctx context.Context, config Config) ([]ImageResult, error) {
	config = NormalizeConfig(config)
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	timeout := client.inspectTimeout
	if timeout <= 0 {
		timeout = defaultInspectTimeout
	}
	// One deadline covers every image and authentication round trip in this card inspection.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	images := config.Images
	var catalogErr error
	if hasImageSelector(images) {
		var repositories []string
		repositories, catalogErr = client.catalog(ctx, config)
		if catalogErr == nil {
			var expandErr error
			images, expandErr = expandImageSelectors(ctx, images, repositories)
			if expandErr != nil {
				catalogErr = expandErr
				images = config.Images
			}
		}
	}
	results := make([]ImageResult, 0, len(images))
	for _, image := range images {
		result := ImageResult{Image: image}
		if isImageSelector(image) {
			if catalogErr != nil {
				result.Error = compactError(catalogErr)
			} else {
				result.Error = "Registry 仓库目录没有匹配的镜像"
			}
			results = append(results, result)
			continue
		}
		if err := ctx.Err(); err != nil {
			result.Error = compactError(err)
			results = append(results, result)
			continue
		}
		tags, err := client.tags(ctx, config, image)
		if err != nil {
			result.Error = compactError(err)
			results = append(results, result)
			continue
		}
		result.Tag = latestTag(tags)
		if result.Tag == "" {
			result.Error = "仓库没有可用标签"
			results = append(results, result)
			continue
		}
		if pushedAt, ok := client.harborPushTime(ctx, config, image, result.Tag); ok {
			result.PushedAt = pushedAt
			result.PushTimeAvailable = true
			result.TimeSource = ImageTimePushed
		} else if createdAt, ok := client.registryImageCreatedTime(ctx, config, image, result.Tag); ok {
			result.PushedAt = createdAt
			result.PushTimeAvailable = true
			result.TimeSource = ImageTimeCreated
		}
		results = append(results, result)
	}
	return results, nil
}

func isImageSelector(value string) bool {
	if value == "*" || value == "*/*" {
		return true
	}
	prefix, ok := strings.CutSuffix(value, "/*")
	return ok && imagePattern.MatchString(prefix)
}

func hasImageSelector(images []string) bool {
	for _, image := range images {
		if isImageSelector(image) {
			return true
		}
	}
	return false
}

func expandImageSelectors(ctx context.Context, configured, repositories []string) ([]string, error) {
	sort.Strings(repositories)
	expanded := make([]string, 0, len(configured)+len(repositories))
	seen := map[string]bool{}
	appendImage := func(image string) error {
		if !seen[image] {
			if len(expanded) >= maxExpandedImages {
				return fmt.Errorf("Registry 镜像匹配结果过多（最多 %d 个）", maxExpandedImages)
			}
			expanded = append(expanded, image)
			seen[image] = true
		}
		return nil
	}
	for _, configuredImage := range configured {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !isImageSelector(configuredImage) {
			if err := appendImage(configuredImage); err != nil {
				return nil, err
			}
			continue
		}
		matched := false
		prefix := strings.TrimSuffix(configuredImage, "*")
		for _, repository := range repositories {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if prefix == "" || prefix == "*/" || strings.HasPrefix(repository, prefix) {
				if err := appendImage(repository); err != nil {
					return nil, err
				}
				matched = true
			}
		}
		if !matched {
			// Preserve an empty selector as an error row instead of silently
			// producing an empty card.
			if err := appendImage(configuredImage); err != nil {
				return nil, err
			}
		}
	}
	return expanded, nil
}

func (client *Client) catalog(ctx context.Context, config Config) ([]string, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil {
		return nil, err
	}
	catalogPath := strings.TrimRight(endpoint.Path, "/") + "/v2/_catalog"
	next := *endpoint
	next.Path, next.RawPath, next.RawQuery = catalogPath, "", "n=100"
	seenPages := map[string]bool{}
	repositories := []string{}
	seenRepositories := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(seenPages) >= maxCatalogPages {
			return nil, fmt.Errorf("Registry 仓库目录分页过多（最多 %d 页）", maxCatalogPages)
		}
		pageURL := next.String()
		if seenPages[pageURL] {
			return nil, errors.New("Registry 仓库目录返回了重复分页")
		}
		seenPages[pageURL] = true
		response, requestErr := client.doAuthenticated(ctx, pageURL, config)
		if requestErr != nil {
			return nil, requestErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return nil, fmt.Errorf("Registry 仓库目录返回 HTTP %d", response.StatusCode)
		}
		var document struct {
			Repositories []string `json:"repositories"`
		}
		decodeErr := decodeJSON(response.Body, &document)
		link := strings.Join(response.Header.Values("Link"), ",")
		response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("解析 Registry 仓库目录：%w", decodeErr)
		}
		for _, repository := range document.Repositories {
			repository = strings.Trim(strings.ToLower(strings.TrimSpace(repository)), "/")
			if imagePattern.MatchString(repository) && !seenRepositories[repository] {
				if len(repositories) >= maxCatalogRepositories {
					return nil, fmt.Errorf("Registry 仓库目录包含过多镜像（最多 %d 个）", maxCatalogRepositories)
				}
				repositories = append(repositories, repository)
				seenRepositories[repository] = true
			}
		}
		if strings.TrimSpace(link) == "" {
			return repositories, nil
		}
		resolved, resolveErr := resolveCatalogLink(link, &next, endpoint, catalogPath)
		if resolveErr != nil {
			return nil, resolveErr
		}
		next = *resolved
	}
}

func resolveCatalogLink(link string, current, endpoint *url.URL, catalogPath string) (*url.URL, error) {
	var target string
	for _, value := range splitLinkValues(link) {
		start := strings.Index(value, "<")
		end := strings.Index(value, ">")
		if start < 0 || end <= start+1 || !linkHasRelation(value[end+1:], "next") {
			continue
		}
		target = value[start+1 : end]
		break
	}
	if target == "" {
		return nil, errors.New("Registry 仓库目录返回了无效分页")
	}
	reference, err := url.Parse(target)
	if err != nil {
		return nil, errors.New("Registry 仓库目录返回了无效分页")
	}
	resolved := current.ResolveReference(reference)
	if resolved.Scheme != endpoint.Scheme || resolved.Host != endpoint.Host || resolved.User != nil || resolved.Path != catalogPath || resolved.Fragment != "" {
		return nil, errors.New("Registry 仓库目录分页不能离开当前 Registry")
	}
	for name := range resolved.Query() {
		if name != "n" && name != "last" {
			return nil, errors.New("Registry 仓库目录返回了无效分页参数")
		}
	}
	return resolved, nil
}

func splitLinkValues(header string) []string {
	values := []string{}
	start := 0
	inAngle, inQuote, escaped := false, false, false
	for index, character := range header {
		switch {
		case escaped:
			escaped = false
		case inQuote && character == '\\':
			escaped = true
		case character == '"' && !inAngle:
			inQuote = !inQuote
		case character == '<' && !inQuote:
			inAngle = true
		case character == '>' && !inQuote:
			inAngle = false
		case character == ',' && !inAngle && !inQuote:
			values = append(values, strings.TrimSpace(header[start:index]))
			start = index + 1
		}
	}
	values = append(values, strings.TrimSpace(header[start:]))
	return values
}

func linkHasRelation(parameters, wanted string) bool {
	for _, parameter := range strings.Split(parameters, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
		if !ok || !strings.EqualFold(name, "rel") {
			continue
		}
		for _, relation := range strings.Fields(strings.Trim(strings.TrimSpace(value), `"`)) {
			if strings.EqualFold(relation, wanted) {
				return true
			}
		}
	}
	return false
}

func (client *Client) tags(ctx context.Context, config Config, image string) ([]string, error) {
	endpoint := config.Endpoint + "/v2/" + escapeRepository(image) + "/tags/list"
	response, err := client.doAuthenticated(ctx, endpoint, config)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Registry 返回 HTTP %d", response.StatusCode)
	}
	var document struct {
		Tags []string `json:"tags"`
	}
	if err := decodeJSON(response.Body, &document); err != nil {
		return nil, fmt.Errorf("解析标签列表：%w", err)
	}
	return document.Tags, nil
}

func (client *Client) doAuthenticated(ctx context.Context, endpoint string, config Config) (*http.Response, error) {
	return client.doAuthenticatedAccept(ctx, endpoint, config, "application/json")
}

func (client *Client) doAuthenticatedAccept(ctx context.Context, endpoint string, config Config, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	if config.AuthMode == "basic" || config.Username != "" {
		request.SetBasicAuth(config.Username, config.Password)
	}
	response, err := client.doRegistryRequest(request)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		return response, err
	}
	challenge := strings.TrimSpace(response.Header.Get("WWW-Authenticate"))
	response.Body.Close()
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(challenge)), "bearer ") {
		return response, nil
	}
	token, err := client.exchangeToken(ctx, challenge, config)
	if err != nil {
		return nil, err
	}
	retry := request.Clone(ctx)
	retry.Header.Set("Authorization", "Bearer "+token)
	return client.doRegistryRequest(retry)
}

func (client *Client) doRegistryRequest(request *http.Request) (*http.Response, error) {
	registryClient := *client.client
	registryClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return registryClient.Do(request)
}

func (client *Client) exchangeToken(ctx context.Context, challenge string, config Config) (string, error) {
	parameters := parseChallenge(strings.TrimSpace(challenge[len("Bearer "):]))
	realm := parameters["realm"]
	parsed, err := url.Parse(realm)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("Registry 返回了无效的令牌服务地址")
	}
	// A Bearer realm owns its HTTP/HTTPS choice independently from the Registry endpoint.
	query := parsed.Query()
	for _, name := range []string{"service", "scope"} {
		if parameters[name] != "" {
			query.Set(name, parameters[name])
		}
	}
	if config.Username != "" {
		query.Set("account", config.Username)
	}
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	if config.AuthMode == "basic" || config.Username != "" {
		request.SetBasicAuth(config.Username, config.Password)
	}
	response, err := client.doRegistryRequest(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("令牌服务返回 HTTP %d", response.StatusCode)
	}
	var document struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := decodeJSON(response.Body, &document); err != nil {
		return "", fmt.Errorf("解析 Registry 令牌：%w", err)
	}
	if document.Token == "" {
		document.Token = document.AccessToken
	}
	if document.Token == "" {
		return "", errors.New("令牌服务没有返回 token")
	}
	return document.Token, nil
}

func (client *Client) harborPushTime(ctx context.Context, config Config, image, tag string) (time.Time, bool) {
	parts := strings.Split(image, "/")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	endpoint := config.Endpoint + "/api/v2.0/projects/" + url.PathEscape(parts[0]) + "/repositories/" + url.PathEscape(strings.Join(parts[1:], "/")) + "/artifacts/" + url.PathEscape(tag)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return time.Time{}, false
	}
	if config.AuthMode == "basic" || config.Username != "" {
		request.SetBasicAuth(config.Username, config.Password)
	}
	response, err := client.doRegistryRequest(request)
	if err != nil {
		return time.Time{}, false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return time.Time{}, false
	}
	var document struct {
		PushTime time.Time `json:"push_time"`
	}
	if decodeJSON(response.Body, &document) != nil || document.PushTime.IsZero() {
		return time.Time{}, false
	}
	return document.PushTime, true
}

func (client *Client) registryImageCreatedTime(ctx context.Context, config Config, image, reference string) (time.Time, bool) {
	configDigest, ok := client.registryManifestConfigDigest(ctx, config, image, reference, 2)
	if !ok {
		return time.Time{}, false
	}
	blobURL := config.Endpoint + "/v2/" + escapeRepository(image) + "/blobs/" + configDigest
	response, err := client.doAuthenticated(ctx, blobURL, config)
	if err != nil {
		return time.Time{}, false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return time.Time{}, false
	}
	var imageConfig struct {
		Created time.Time `json:"created"`
	}
	if decodeJSON(response.Body, &imageConfig) != nil || imageConfig.Created.IsZero() {
		return time.Time{}, false
	}
	return imageConfig.Created, true
}

func (client *Client) registryManifestConfigDigest(ctx context.Context, config Config, image, reference string, remainingDepth int) (string, bool) {
	if remainingDepth < 1 {
		return "", false
	}
	manifestURL := config.Endpoint + "/v2/" + escapeRepository(image) + "/manifests/" + url.PathEscape(reference)
	response, err := client.doAuthenticatedAccept(ctx, manifestURL, config, strings.Join([]string{
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
	}, ", "))
	if err != nil {
		return "", false
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return "", false
	}
	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	decodeErr := decodeJSON(response.Body, &manifest)
	response.Body.Close()
	if decodeErr != nil {
		return "", false
	}
	if digestPattern.MatchString(manifest.Config.Digest) {
		return manifest.Config.Digest, true
	}
	for _, child := range manifest.Manifests {
		if !digestPattern.MatchString(child.Digest) {
			continue
		}
		if digest, ok := client.registryManifestConfigDigest(ctx, config, image, child.Digest, remainingDepth-1); ok {
			return digest, true
		}
	}
	return "", false
}

func latestTag(tags []string) string {
	versions := make([]string, 0, len(tags))
	for _, tag := range tags {
		if _, ok := semanticParts(tag); ok {
			versions = append(versions, tag)
		}
	}
	if len(versions) > 0 {
		sort.SliceStable(versions, func(i, j int) bool { return compareSemantic(versions[i], versions[j]) > 0 })
		return versions[0]
	}
	for _, tag := range tags {
		if tag == "latest" {
			return tag
		}
	}
	sort.Strings(tags)
	if len(tags) > 0 {
		return tags[len(tags)-1]
	}
	return ""
}

func semanticParts(value string) ([]int, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	core := strings.SplitN(value, "-", 2)[0]
	segments := strings.Split(core, ".")
	if len(segments) < 2 || len(segments) > 4 {
		return nil, false
	}
	parts := make([]int, len(segments))
	for index, segment := range segments {
		if segment == "" {
			return nil, false
		}
		part, err := strconv.Atoi(segment)
		if err != nil {
			return nil, false
		}
		parts[index] = part
	}
	return parts, true
}

func compareSemantic(left, right string) int {
	a, _ := semanticParts(left)
	b, _ := semanticParts(right)
	for index := 0; index < 4; index++ {
		var av, bv int
		if index < len(a) {
			av = a[index]
		}
		if index < len(b) {
			bv = b[index]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	leftPrerelease := strings.Contains(strings.TrimPrefix(left, "v"), "-")
	rightPrerelease := strings.Contains(strings.TrimPrefix(right, "v"), "-")
	if leftPrerelease != rightPrerelease {
		if leftPrerelease {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}

func parseChallenge(value string) map[string]string {
	result := map[string]string{}
	for _, field := range strings.Split(value, ",") {
		name, raw, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok {
			result[strings.ToLower(name)] = strings.Trim(strings.TrimSpace(raw), `"`)
		}
	}
	return result
}

func escapeRepository(value string) string {
	parts := strings.Split(value, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func decodeJSON(reader io.Reader, destination any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return errors.New("响应内容超过 2 MiB")
	}
	return json.Unmarshal(raw, destination)
}

func compactError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > 180 {
		message = message[:180]
	}
	return message
}
