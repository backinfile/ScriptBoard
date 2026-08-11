package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	StateRoot            string              `yaml:"state_root"`
	Listen               string              `yaml:"listen"`
	TLSCert              string              `yaml:"tls_cert"`
	TLSKey               string              `yaml:"tls_key"`
	ExecutorChains       map[string][]string `yaml:"executor_chains"`
	AdminUsername        string              `yaml:"admin_username"`
	AdminPasswordFile    string              `yaml:"admin_password_file"`
	TrustedProxies       []string            `yaml:"trusted_proxies"`
	AllowedHosts         []string            `yaml:"allowed_hosts"`
	CanonicalExternalURL string              `yaml:"canonical_external_url"`
	RunTimeoutGrace      time.Duration       `yaml:"-"`
	UpdateCheck          bool                `yaml:"update_check"`
	UpdateInterval       time.Duration       `yaml:"-"`
	ConfigPath           string              `yaml:"-"`
}

type yamlConfig struct {
	StateRoot              string              `yaml:"state_root"`
	Listen                 string              `yaml:"listen"`
	TLSCert                string              `yaml:"tls_cert"`
	TLSKey                 string              `yaml:"tls_key"`
	ExecutorChains         map[string][]string `yaml:"executor_chains"`
	AdminUsername          string              `yaml:"admin_username"`
	AdminPasswordFile      string              `yaml:"admin_password_file"`
	TrustedProxies         []string            `yaml:"trusted_proxies"`
	AllowedHosts           []string            `yaml:"allowed_hosts"`
	CanonicalExternalURL   string              `yaml:"canonical_external_url"`
	RunTimeoutGraceSeconds *int                `yaml:"run_timeout_grace_seconds"`
	UpdateCheck            *bool               `yaml:"update_check"`
	UpdateIntervalHours    *int                `yaml:"update_check_interval_hours"`
	RemovedManagedRoot     yaml.Node           `yaml:"managed_root"`
	RemovedGitExecutable   yaml.Node           `yaml:"git_executable"`
	RemovedAdminPassword   yaml.Node           `yaml:"admin_password"`
}

func Load(arguments []string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, legacy := range []string{"SCRIPTBOARD_MANAGED_ROOT", "SCRIPTBOARD_GIT_EXECUTABLE", "SCRIPTBOARD_ADMIN_PASSWORD"} {
		if strings.TrimSpace(getenv(legacy)) != "" {
			if legacy == "SCRIPTBOARD_ADMIN_PASSWORD" {
				return Config{}, errors.New("SCRIPTBOARD_ADMIN_PASSWORD was removed; use SCRIPTBOARD_ADMIN_PASSWORD_FILE or the first-start credential")
			}
			return Config{}, fmt.Errorf("%s was removed; create a new configuration and State Root for this version", legacy)
		}
	}
	for _, argument := range arguments {
		for _, legacy := range []string{"--managed-root", "--here", "--git-executable", "--admin-password"} {
			if argument == legacy || strings.HasPrefix(argument, legacy+"=") {
				if legacy == "--admin-password" {
					return Config{}, errors.New("--admin-password was removed; use --admin-password-file or the first-start credential")
				}
				return Config{}, fmt.Errorf("%s was removed; create a new configuration and State Root for this version", legacy)
			}
		}
	}
	result := defaults()
	configPath, explicit := requestedConfigPath(arguments, result.ConfigPath)
	result.ConfigPath = configPath
	data, err := os.ReadFile(configPath)
	if err == nil {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		var values yamlConfig
		if err := decoder.Decode(&values); err != nil {
			return Config{}, fmt.Errorf("解析 YAML 配置: %w", err)
		}
		if values.RemovedManagedRoot.Kind != 0 {
			return Config{}, errors.New("managed_root was removed; create a new configuration and State Root for this version")
		}
		if values.RemovedGitExecutable.Kind != 0 {
			return Config{}, errors.New("git_executable was removed; create a new configuration and State Root for this version")
		}
		if values.RemovedAdminPassword.Kind != 0 {
			return Config{}, errors.New("admin_password was removed; use admin_password_file or the first-start credential")
		}
		applyYAML(&result, values)
	} else if explicit || !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("读取配置文件 %q: %w", configPath, err)
	}
	applyEnvironment(&result, getenv)

	flags := flag.NewFlagSet("scriptboard", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&result.ConfigPath, "config", result.ConfigPath, "YAML 配置文件")
	flags.StringVar(&result.StateRoot, "state-root", result.StateRoot, "内部状态目录")
	flags.StringVar(&result.Listen, "listen", result.Listen, "HTTP 监听地址")
	flags.StringVar(&result.TLSCert, "tls-cert", result.TLSCert, "TLS 证书路径")
	flags.StringVar(&result.TLSKey, "tls-key", result.TLSKey, "TLS 私钥路径")
	flags.DurationVar(&result.RunTimeoutGrace, "run-timeout-grace", result.RunTimeoutGrace, "自动超时强杀宽限")
	flags.BoolVar(&result.UpdateCheck, "update-check", result.UpdateCheck, "定期检查正式版更新")
	flags.DurationVar(&result.UpdateInterval, "update-check-interval", result.UpdateInterval, "更新检查间隔")
	flags.StringVar(&result.AdminUsername, "admin-username", result.AdminUsername, "权威管理员用户名覆盖")
	flags.StringVar(&result.AdminPasswordFile, "admin-password-file", result.AdminPasswordFile, "权威管理员密码文件")
	trustedProxyFlagSeen := false
	allowedHostFlagSeen := false
	flags.Func("trusted-proxy", "可信反向代理 IP 或 CIDR（可重复）", func(value string) error {
		if !trustedProxyFlagSeen {
			result.TrustedProxies = nil
			trustedProxyFlagSeen = true
		}
		result.TrustedProxies = append(result.TrustedProxies, value)
		return nil
	})
	flags.Func("allowed-host", "允许的 HTTP Host（可重复）", func(value string) error {
		if !allowedHostFlagSeen {
			result.AllowedHosts = nil
			allowedHostFlagSeen = true
		}
		result.AllowedHosts = append(result.AllowedHosts, value)
		return nil
	})
	flags.StringVar(&result.CanonicalExternalURL, "canonical-external-url", result.CanonicalExternalURL, "对外访问的规范 URL")
	if err := flags.Parse(arguments); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("未知位置参数: %v", flags.Args())
	}
	if result.RunTimeoutGrace <= 0 {
		return Config{}, fmt.Errorf("Run 超时强杀宽限必须大于零")
	}
	if result.UpdateInterval < time.Hour || result.UpdateInterval > 168*time.Hour {
		return Config{}, fmt.Errorf("更新检查间隔必须在 1 到 168 小时之间")
	}
	if result.AdminPasswordFile != "" && !filepath.IsAbs(result.AdminPasswordFile) {
		return Config{}, errors.New("admin_password_file must be an absolute path")
	}
	for extension, chain := range result.ExecutorChains {
		if extension == "" || extension[0] != '.' || len(chain) == 0 {
			return Config{}, fmt.Errorf("执行器链 %q 无效", extension)
		}
		for _, executable := range chain {
			if !filepath.IsAbs(executable) {
				return Config{}, fmt.Errorf("执行器路径必须为绝对路径: %s", executable)
			}
		}
	}
	for _, trusted := range result.TrustedProxies {
		if net.ParseIP(trusted) == nil {
			if _, _, err := net.ParseCIDR(trusted); err != nil {
				return Config{}, fmt.Errorf("可信代理 %q 无效", trusted)
			}
		}
	}
	if err := finalizeHostSecurity(&result); err != nil {
		return Config{}, err
	}
	return result, nil
}

func defaults() Config {
	if runtime.GOOS == "windows" {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		base := filepath.Join(programData, "ScriptBoard")
		return Config{
			StateRoot:       filepath.Join(base, "state"),
			Listen:          "127.0.0.1:8787",
			TrustedProxies:  nil,
			RunTimeoutGrace: 30 * time.Second,
			UpdateCheck:     true,
			UpdateInterval:  6 * time.Hour,
			ConfigPath:      filepath.Join(base, "config.yaml"),
		}
	}
	return Config{
		StateRoot:       "/var/lib/scriptboard/state",
		Listen:          "127.0.0.1:8787",
		TrustedProxies:  nil,
		RunTimeoutGrace: 30 * time.Second,
		UpdateCheck:     true,
		UpdateInterval:  6 * time.Hour,
		ConfigPath:      "/etc/scriptboard/config.yaml",
	}
}

func requestedConfigPath(arguments []string, fallback string) (string, bool) {
	for index, argument := range arguments {
		if argument == "--config" && index+1 < len(arguments) {
			return arguments[index+1], true
		}
		const prefix = "--config="
		if len(argument) > len(prefix) && argument[:len(prefix)] == prefix {
			return argument[len(prefix):], true
		}
	}
	return fallback, false
}

func applyYAML(result *Config, values yamlConfig) {
	if values.StateRoot != "" {
		result.StateRoot = values.StateRoot
	}
	if values.Listen != "" {
		result.Listen = values.Listen
	}
	if values.TLSCert != "" {
		result.TLSCert = values.TLSCert
	}
	if values.TLSKey != "" {
		result.TLSKey = values.TLSKey
	}
	if values.ExecutorChains != nil {
		result.ExecutorChains = values.ExecutorChains
	}
	if values.AdminUsername != "" {
		result.AdminUsername = values.AdminUsername
	}
	if values.AdminPasswordFile != "" {
		result.AdminPasswordFile = values.AdminPasswordFile
	}
	if values.TrustedProxies != nil {
		result.TrustedProxies = append([]string(nil), values.TrustedProxies...)
	}
	if values.AllowedHosts != nil {
		result.AllowedHosts = append([]string(nil), values.AllowedHosts...)
	}
	if values.CanonicalExternalURL != "" {
		result.CanonicalExternalURL = values.CanonicalExternalURL
	}
	if values.RunTimeoutGraceSeconds != nil {
		result.RunTimeoutGrace = time.Duration(*values.RunTimeoutGraceSeconds) * time.Second
	}
	if values.UpdateCheck != nil {
		result.UpdateCheck = *values.UpdateCheck
	}
	if values.UpdateIntervalHours != nil {
		result.UpdateInterval = time.Duration(*values.UpdateIntervalHours) * time.Hour
	}
}

func applyEnvironment(result *Config, getenv func(string) string) {
	if value := getenv("SCRIPTBOARD_STATE_ROOT"); value != "" {
		result.StateRoot = value
	}
	if value := getenv("SCRIPTBOARD_LISTEN"); value != "" {
		result.Listen = value
	}
	if value := getenv("SCRIPTBOARD_TLS_CERT"); value != "" {
		result.TLSCert = value
	}
	if value := getenv("SCRIPTBOARD_TLS_KEY"); value != "" {
		result.TLSKey = value
	}
	if value := getenv("SCRIPTBOARD_RUN_TIMEOUT_GRACE_SECONDS"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			result.RunTimeoutGrace = time.Duration(seconds) * time.Second
		}
	}
	if value := getenv("SCRIPTBOARD_UPDATE_CHECK"); value != "" {
		if enabled, err := strconv.ParseBool(value); err == nil {
			result.UpdateCheck = enabled
		}
	}
	if value := getenv("SCRIPTBOARD_UPDATE_CHECK_INTERVAL_HOURS"); value != "" {
		if hours, err := strconv.Atoi(value); err == nil {
			result.UpdateInterval = time.Duration(hours) * time.Hour
		}
	}
	if value := getenv("SCRIPTBOARD_ADMIN_USERNAME"); value != "" {
		result.AdminUsername = value
	}
	if value := getenv("SCRIPTBOARD_ADMIN_PASSWORD_FILE"); value != "" {
		result.AdminPasswordFile = value
	}
	if value := getenv("SCRIPTBOARD_TRUSTED_PROXIES"); value != "" {
		result.TrustedProxies = strings.Split(value, ",")
		for index := range result.TrustedProxies {
			result.TrustedProxies[index] = strings.TrimSpace(result.TrustedProxies[index])
		}
	}
	if value := getenv("SCRIPTBOARD_ALLOWED_HOSTS"); value != "" {
		result.AllowedHosts = splitCommaList(value)
	}
	if value := getenv("SCRIPTBOARD_CANONICAL_EXTERNAL_URL"); value != "" {
		result.CanonicalExternalURL = strings.TrimSpace(value)
	}
}

func splitCommaList(value string) []string {
	result := strings.Split(value, ",")
	for index := range result {
		result[index] = strings.TrimSpace(result[index])
	}
	return result
}

func finalizeHostSecurity(result *Config) error {
	listenHost, _, err := net.SplitHostPort(result.Listen)
	if err != nil {
		return fmt.Errorf("监听地址 %q 无效: %w", result.Listen, err)
	}
	if len(result.AllowedHosts) == 0 {
		listenIP := net.ParseIP(listenHost)
		if listenIP != nil && listenIP.IsLoopback() {
			result.AllowedHosts = []string{listenHost, "localhost"}
			if listenIP.To4() != nil {
				result.AllowedHosts = append(result.AllowedHosts, "::1")
			} else {
				result.AllowedHosts = append(result.AllowedHosts, "127.0.0.1")
			}
		} else if strings.EqualFold(listenHost, "localhost") {
			result.AllowedHosts = []string{"localhost", "127.0.0.1", "::1"}
		} else {
			return errors.New("非回环或通配监听必须显式配置 allowed_hosts")
		}
	}
	seen := make(map[string]bool, len(result.AllowedHosts))
	for _, allowed := range result.AllowedHosts {
		normalized, err := normalizeConfiguredHost(allowed)
		if err != nil {
			return fmt.Errorf("allowed host %q 无效: %w", allowed, err)
		}
		if seen[normalized] {
			return fmt.Errorf("allowed host %q 重复", allowed)
		}
		seen[normalized] = true
	}
	if result.CanonicalExternalURL == "" {
		scheme := "http"
		if result.TLSCert != "" {
			scheme = "https"
		}
		result.CanonicalExternalURL = scheme + "://" + result.Listen
	}
	canonical, err := url.Parse(result.CanonicalExternalURL)
	if err != nil || (canonical.Scheme != "http" && canonical.Scheme != "https") || canonical.Host == "" || canonical.User != nil || canonical.RawQuery != "" || canonical.Fragment != "" || (canonical.Path != "" && canonical.Path != "/") {
		return fmt.Errorf("canonical_external_url %q 无效", result.CanonicalExternalURL)
	}
	if result.TLSCert != "" && canonical.Scheme != "https" {
		return fmt.Errorf("canonical_external_url 在启用 TLS 时必须使用 https")
	}
	canonicalHost, err := normalizeConfiguredHost(canonical.Host)
	if err != nil || !seen[canonicalHost] {
		return fmt.Errorf("canonical_external_url host 必须包含在 allowed_hosts 中")
	}
	result.CanonicalExternalURL = strings.TrimSuffix(canonical.String(), "/")
	return nil
}

func normalizeConfiguredHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if host, rawPort, err := net.SplitHostPort(value); err == nil {
		port, portErr := strconv.ParseUint(rawPort, 10, 16)
		if portErr != nil || port == 0 {
			return "", errors.New("端口格式无效")
		}
		value = host
	} else if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	} else if strings.Contains(value, ":") && net.ParseIP(value) == nil {
		return "", errors.New("端口格式无效")
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if value == "" || strings.ContainsAny(value, "/\\,@ \t\r\n") {
		return "", errors.New("主机名包含无效字符")
	}
	for _, character := range value {
		if character > unicode.MaxASCII || unicode.IsControl(character) || unicode.IsSpace(character) {
			return "", errors.New("主机名必须是无控制字符的 ASCII")
		}
	}
	if net.ParseIP(value) == nil {
		for _, label := range strings.Split(value, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", errors.New("DNS 标签无效")
			}
			for _, character := range label {
				if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
					return "", errors.New("DNS 标签包含无效字符")
				}
			}
		}
	}
	return value, nil
}
