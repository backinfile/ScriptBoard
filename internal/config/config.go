package config

import (
	"bytes"
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
	ManagedRoot       string              `yaml:"managed_root"`
	StateRoot         string              `yaml:"state_root"`
	Listen            string              `yaml:"listen"`
	GitExecutable     string              `yaml:"git_executable"`
	TLSCert           string              `yaml:"tls_cert"`
	TLSKey            string              `yaml:"tls_key"`
	ExecutorChains    map[string][]string `yaml:"executor_chains"`
	AdminUsername     string              `yaml:"admin_username"`
	AdminPassword     string              `yaml:"admin_password"`
	AdminPasswordFile string              `yaml:"admin_password_file"`
	TrustedProxies    []string            `yaml:"trusted_proxies"`
	RunTimeoutGrace   time.Duration       `yaml:"-"`
	ConfigPath        string              `yaml:"-"`
}

type yamlConfig struct {
	ManagedRoot            string              `yaml:"managed_root"`
	StateRoot              string              `yaml:"state_root"`
	Listen                 string              `yaml:"listen"`
	GitExecutable          string              `yaml:"git_executable"`
	TLSCert                string              `yaml:"tls_cert"`
	TLSKey                 string              `yaml:"tls_key"`
	ExecutorChains         map[string][]string `yaml:"executor_chains"`
	AdminUsername          string              `yaml:"admin_username"`
	AdminPassword          string              `yaml:"admin_password"`
	AdminPasswordFile      string              `yaml:"admin_password_file"`
	TrustedProxies         []string            `yaml:"trusted_proxies"`
	RunTimeoutGraceSeconds *int                `yaml:"run_timeout_grace_seconds"`
}

func Load(arguments []string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
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
		applyYAML(&result, values)
	} else if explicit || !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("读取配置文件 %q: %w", configPath, err)
	}
	applyEnvironment(&result, getenv)

	flags := flag.NewFlagSet("scriptboard", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&result.ConfigPath, "config", result.ConfigPath, "YAML 配置文件")
	flags.StringVar(&result.ManagedRoot, "managed-root", result.ManagedRoot, "受管根目录")
	flags.StringVar(&result.StateRoot, "state-root", result.StateRoot, "内部状态目录")
	flags.StringVar(&result.Listen, "listen", result.Listen, "HTTP 监听地址")
	flags.StringVar(&result.GitExecutable, "git-executable", result.GitExecutable, "Git CLI 绝对路径")
	flags.StringVar(&result.TLSCert, "tls-cert", result.TLSCert, "TLS 证书路径")
	flags.StringVar(&result.TLSKey, "tls-key", result.TLSKey, "TLS 私钥路径")
	flags.DurationVar(&result.RunTimeoutGrace, "run-timeout-grace", result.RunTimeoutGrace, "自动超时强杀宽限")
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
			ManagedRoot: filepath.Join(base, "managed"), StateRoot: filepath.Join(base, "state"),
			Listen: "127.0.0.1:8787", RunTimeoutGrace: 30 * time.Second, ConfigPath: filepath.Join(base, "config.yaml"),
		}
	}
	return Config{
		ManagedRoot: "/var/lib/scriptboard/managed", StateRoot: "/var/lib/scriptboard/state",
		Listen: "127.0.0.1:8787", RunTimeoutGrace: 30 * time.Second, ConfigPath: "/etc/scriptboard/config.yaml",
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
	if values.ManagedRoot != "" {
		result.ManagedRoot = values.ManagedRoot
	}
	if values.StateRoot != "" {
		result.StateRoot = values.StateRoot
	}
	if values.Listen != "" {
		result.Listen = values.Listen
	}
	if values.GitExecutable != "" {
		result.GitExecutable = values.GitExecutable
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
}

func applyEnvironment(result *Config, getenv func(string) string) {
	if value := getenv("SCRIPTBOARD_MANAGED_ROOT"); value != "" {
		result.ManagedRoot = value
	}
	if value := getenv("SCRIPTBOARD_STATE_ROOT"); value != "" {
		result.StateRoot = value
	}
	if value := getenv("SCRIPTBOARD_LISTEN"); value != "" {
		result.Listen = value
	}
	if value := getenv("SCRIPTBOARD_GIT_EXECUTABLE"); value != "" {
		result.GitExecutable = value
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
