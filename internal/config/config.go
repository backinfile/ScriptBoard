package config

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	ManagedRoot     string        `yaml:"managed_root"`
	StateRoot       string        `yaml:"state_root"`
	Listen          string        `yaml:"listen"`
	GitExecutable   string        `yaml:"git_executable"`
	RunTimeoutGrace time.Duration `yaml:"-"`
	ConfigPath      string        `yaml:"-"`
}

type yamlConfig struct {
	ManagedRoot            string `yaml:"managed_root"`
	StateRoot              string `yaml:"state_root"`
	Listen                 string `yaml:"listen"`
	GitExecutable          string `yaml:"git_executable"`
	RunTimeoutGraceSeconds *int   `yaml:"run_timeout_grace_seconds"`
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
	flags.DurationVar(&result.RunTimeoutGrace, "run-timeout-grace", result.RunTimeoutGrace, "自动超时强杀宽限")
	if err := flags.Parse(arguments); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("未知位置参数: %v", flags.Args())
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
	if value := getenv("SCRIPTBOARD_RUN_TIMEOUT_GRACE_SECONDS"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			result.RunTimeoutGrace = time.Duration(seconds) * time.Second
		}
	}
}
