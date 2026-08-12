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

const maxResponseBytes = 2 << 20

var imagePattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._-][a-z0-9]+)|(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*))*$`)

type Config struct {
	Endpoint string   `json:"endpoint"`
	Images   []string `json:"images"`
	AuthMode string   `json:"authMode,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"-"`
}

type ImageResult struct {
	Image             string    `json:"image"`
	Tag               string    `json:"tag,omitempty"`
	PushedAt          time.Time `json:"pushedAt,omitempty"`
	PushTimeAvailable bool      `json:"pushTimeAvailable,omitempty"`
	Error             string    `json:"error,omitempty"`
	Stale             bool      `json:"stale,omitempty"`
}

type Client struct{ client *http.Client }

func New(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{client: client}
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
		if !imagePattern.MatchString(image) {
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
	results := make([]ImageResult, 0, len(config.Images))
	for _, image := range config.Images {
		result := ImageResult{Image: image}
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
		}
		results = append(results, result)
	}
	return results, nil
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if config.AuthMode == "basic" || config.Username != "" {
		request.SetBasicAuth(config.Username, config.Password)
	}
	response, err := client.client.Do(request)
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
	return client.client.Do(retry)
}

func (client *Client) exchangeToken(ctx context.Context, challenge string, config Config) (string, error) {
	parameters := parseChallenge(strings.TrimSpace(challenge[len("Bearer "):]))
	realm := parameters["realm"]
	parsed, err := url.Parse(realm)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("Registry 返回了无效的令牌服务地址")
	}
	registryURL, _ := url.Parse(config.Endpoint)
	if registryURL != nil && registryURL.Scheme == "https" && parsed.Scheme != "https" {
		return "", errors.New("HTTPS Registry 的令牌服务不能降级为 HTTP")
	}
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
	response, err := client.client.Do(request)
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
	response, err := client.client.Do(request)
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
