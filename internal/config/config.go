package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	StateRoot         string              `yaml:"state_root"`
	Listen            string              `yaml:"listen"`
	TLSCert           string              `yaml:"tls_cert"`
	TLSKey            string              `yaml:"tls_key"`
	ExecutorChains    map[string][]string `yaml:"executor_chains"`
	AdminUsername     string              `yaml:"admin_username"`
	AdminPassword     string              `yaml:"admin_password"`
	AdminPasswordFile string              `yaml:"admin_password_file"`
	TrustedProxies    []string            `yaml:"trusted_proxies"`
	RunTimeoutGrace   time.Duration       `yaml:"-"`
	UpdateCheck       bool                `yaml:"update_check"`
	UpdateInterval    time.Duration       `yaml:"-"`
	ConfigPath        string              `yaml:"-"`
}

type yamlConfig struct {
	StateRoot              string              `yaml:"state_root"`
	Listen                 string              `yaml:"listen"`
	TLSCert                string              `yaml:"tls_cert"`
	TLSKey                 string              `yaml:"tls_key"`
	ExecutorChains         map[string][]string `yaml:"executor_chains"`
	AdminUsername          string              `yaml:"admin_username"`
	AdminPassword          string              `yaml:"admin_password"`
	AdminPasswordFile      string              `yaml:"admin_password_file"`
	TrustedProxies         []string            `yaml:"trusted_proxies"`
	RunTimeoutGraceSeconds *int                `yaml:"run_timeout_grace_seconds"`
	UpdateCheck            *bool               `yaml:"update_check"`
	UpdateIntervalHours    *int                `yaml:"update_check_interval_hours"`
	RemovedManagedRoot     yaml.Node           `yaml:"managed_root"`
	RemovedGitExecutable   yaml.Node           `yaml:"git_executable"`
}

func Load(arguments []string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, legacy := range []string{"SCRIPTBOARD_MANAGED_ROOT", "SCRIPTBOARD_GIT_EXECUTABLE"} {
		if strings.TrimSpace(getenv(legacy)) != "" {
			return Config{}, fmt.Errorf("%s was removed; create a new configuration and State Root for this version", legacy)
		}
	}
	for _, argument := range arguments {
		for _, legacy := range []string{"--managed-root", "--here", "--git-executable"} {
			if argument == legacy || strings.HasPrefix(argument, legacy+"=") {
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
	flags.StringVar(&result.AdminPassword, "admin-password", result.AdminPassword, "权威管理员密码覆盖")
	flags.StringVar(&result.AdminPasswordFile, "admin-password-file", result.AdminPasswordFile, "权威管理员密码文件")
	trustedProxyFlagSeen := false
	flags.Func("trusted-proxy", "可信反向代理 IP 或 CIDR（可重复）", func(value string) error {
		if !trustedProxyFlagSeen {
			result.TrustedProxies = nil
			trustedProxyFlagSeen = true
		}
		result.TrustedProxies = append(result.TrustedProxies, value)
		return nil
	})
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
			StateRoot: filepath.Join(base, "state"),
			Listen:    "127.0.0.1:8787", RunTimeoutGrace: 30 * time.Second,
			UpdateCheck: true, UpdateInterval: 6 * time.Hour, ConfigPath: filepath.Join(base, "config.yaml"),
		}
	}
	return Config{
		StateRoot: "/var/lib/scriptboard/state",
		Listen:    "127.0.0.1:8787", RunTimeoutGrace: 30 * time.Second,
		UpdateCheck: true, UpdateInterval: 6 * time.Hour, ConfigPath: "/etc/scriptboard/config.yaml",
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
	if values.AdminPassword != "" {
		result.AdminPassword = values.AdminPassword
	}
	if values.AdminPasswordFile != "" {
		result.AdminPasswordFile = values.AdminPasswordFile
	}
	if values.TrustedProxies != nil {
		result.TrustedProxies = append([]string(nil), values.TrustedProxies...)
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
	if value := getenv("SCRIPTBOARD_ADMIN_PASSWORD"); value != "" {
		result.AdminPassword = value
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
	if result.AdminPassword != "" && result.AdminPasswordFile != "" {
		result.AdminPassword = ""
	}
}
