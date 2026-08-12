package pirpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const privateProviderName = "scriptboard-provider"

type LaunchInput struct {
	StateRoot, Executable, Extension                string
	UserID, ConversationID                          string
	Provider, Model, Endpoint, APIKey, SystemPrompt string
	BrokerEndpoint, BrokerCapability                string
	ParentEnvironment                               []string
	SupportsImages                                  bool
}

type LaunchSpec struct {
	Executable, PiHome, SessionDir, Workspace, ModelConfigPath string
	Args, Env                                                  []string
}

func PrepareLaunch(input LaunchInput) (LaunchSpec, error) {
	stateRoot, err := filepath.Abs(strings.TrimSpace(input.StateRoot))
	if err != nil || strings.TrimSpace(input.StateRoot) == "" {
		return LaunchSpec{}, fmt.Errorf("private State Root is required")
	}
	executable := filepath.Clean(strings.TrimSpace(input.Executable))
	if !filepath.IsAbs(executable) {
		return LaunchSpec{}, fmt.Errorf("Pi runtime executable must use an absolute path")
	}
	runtimeRoot := filepath.Join(stateRoot, "assistant", "runtime", "versions")
	if !pathWithin(runtimeRoot, executable) {
		return LaunchSpec{}, fmt.Errorf("Pi runtime executable is outside the private runtime directory")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return LaunchSpec{}, fmt.Errorf("inspect Pi runtime executable: %w", err)
	}
	if strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.ConversationID) == "" {
		return LaunchSpec{}, fmt.Errorf("user and conversation IDs are required")
	}
	provider := strings.TrimSpace(input.Provider)
	model := strings.TrimSpace(input.Model)
	endpoint := strings.TrimSpace(input.Endpoint)
	credential := strings.TrimSpace(input.APIKey)
	if model == "" || credential == "" {
		return LaunchSpec{}, fmt.Errorf("model and credential are required")
	}
	api, err := providerAPI(provider)
	if err != nil {
		return LaunchSpec{}, err
	}
	if err := validateEndpoint(endpoint); err != nil {
		return LaunchSpec{}, err
	}

	userDirectory, err := safeDirectoryComponent(input.UserID)
	if err != nil {
		return LaunchSpec{}, err
	}
	conversationDirectory, err := safeDirectoryComponent(input.ConversationID)
	if err != nil {
		return LaunchSpec{}, err
	}
	assistantRoot := filepath.Join(stateRoot, "assistant")
	piHome := filepath.Join(assistantRoot, "pi-home", userDirectory, conversationDirectory)
	sessionDir := filepath.Join(assistantRoot, "sessions", userDirectory, conversationDirectory)
	workspace := filepath.Join(assistantRoot, "workspaces", userDirectory, conversationDirectory)
	for _, directory := range []string{piHome, sessionDir, workspace} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return LaunchSpec{}, fmt.Errorf("prepare private Pi directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
			return LaunchSpec{}, fmt.Errorf("protect private Pi directory: %w", err)
		}
	}
	resumeSession, err := hasSavedPiSession(sessionDir)
	if err != nil {
		return LaunchSpec{}, err
	}

	modelConfigPath := filepath.Join(piHome, "models.json")
	if err := writeModelConfig(modelConfigPath, endpoint, api, model, input.SupportsImages); err != nil {
		return LaunchSpec{}, err
	}

	args := []string{
		"--mode", "rpc",
		"--provider", privateProviderName,
		"--model", model,
		"--session-dir", sessionDir,
	}
	if resumeSession {
		args = append(args, "--continue")
	}
	args = append(args,
		"--name", "ScriptBoard "+conversationDirectory,
		"--no-extensions",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--no-context-files",
		"--no-approve",
	)
	if systemPrompt := strings.TrimSpace(input.SystemPrompt); systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	parentEnvironment := input.ParentEnvironment
	if parentEnvironment == nil {
		parentEnvironment = os.Environ()
	}
	environment := filteredEnvironment(parentEnvironment)
	if extension := strings.TrimSpace(input.Extension); extension != "" {
		extension = filepath.Clean(extension)
		if !filepath.IsAbs(extension) || !pathWithin(filepath.Dir(executable), extension) {
			return LaunchSpec{}, fmt.Errorf("Pi extension is outside the selected runtime version")
		}
		if extensionInfo, statErr := os.Stat(extension); statErr != nil || !extensionInfo.Mode().IsRegular() {
			return LaunchSpec{}, fmt.Errorf("inspect Pi extension: %w", statErr)
		}
		brokerEndpoint := strings.TrimSpace(input.BrokerEndpoint)
		brokerCapability := strings.TrimSpace(input.BrokerCapability)
		if brokerEndpoint == "" || len(brokerEndpoint) > 512 || strings.ContainsAny(brokerEndpoint, "\r\n\x00") || len(brokerCapability) < 16 || len(brokerCapability) > 256 || strings.ContainsAny(brokerCapability, "\r\n\x00=") {
			return LaunchSpec{}, fmt.Errorf("process-bound Tool Broker capability is required")
		}
		args = append(args, "--no-builtin-tools", "-e", extension)
		environment = append(environment,
			"SCRIPTBOARD_BROKER_ENDPOINT="+brokerEndpoint,
			"SCRIPTBOARD_BROKER_CAPABILITY="+brokerCapability,
		)
	} else {
		if strings.TrimSpace(input.BrokerEndpoint) != "" || strings.TrimSpace(input.BrokerCapability) != "" {
			return LaunchSpec{}, fmt.Errorf("Tool Broker cannot be enabled without the fixed extension")
		}
		args = append(args, "--no-tools")
	}

	environment = append(environment,
		"PI_CODING_AGENT_DIR="+piHome,
		"PI_OFFLINE=1",
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
		"SCRIPTBOARD_PI_API_KEY="+credential,
	)
	return LaunchSpec{
		Executable: executable, PiHome: piHome, SessionDir: sessionDir, Workspace: workspace,
		ModelConfigPath: modelConfigPath, Args: args, Env: environment,
	}, nil
}

func hasSavedPiSession(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, fmt.Errorf("inspect private Pi sessions: %w", err)
	}
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return false, fmt.Errorf("inspect private Pi session: %w", infoErr)
		}
		if info.Mode().IsRegular() && info.Size() > 0 {
			return true, nil
		}
	}
	return false, nil
}

func runtimeExecutableName() string {
	if runtime.GOOS == "windows" {
		return "pi.exe"
	}
	return "pi"
}

func safeDirectoryComponent(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") {
		return "", fmt.Errorf("unsafe assistant identity component")
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_') {
			return "", fmt.Errorf("unsafe assistant identity component")
		}
	}
	return value, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func providerAPI(provider string) (string, error) {
	switch provider {
	case "openai":
		return "openai-responses", nil
	case "anthropic":
		return "anthropic-messages", nil
	case "openai-compatible":
		return "openai-completions", nil
	default:
		return "", fmt.Errorf("unsupported Pi provider %q", provider)
	}
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid provider endpoint")
	}
	// Keep runtime validation aligned with the persisted provider transport choice.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("provider endpoint must use HTTP or HTTPS")
	}
	return nil
}

func filteredEnvironment(parent []string) []string {
	allowed := map[string]bool{
		"SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "TEMP": true, "TMP": true, "TMPDIR": true,
		"LANG": true, "LC_ALL": true,
	}
	result := make([]string, 0, len(allowed))
	for _, entry := range parent {
		name, _, found := strings.Cut(entry, "=")
		if found && allowed[strings.ToUpper(name)] {
			result = append(result, entry)
		}
	}
	return result
}

func writeModelConfig(path, endpoint, api, model string, supportsImages bool) error {
	document := struct {
		Providers map[string]struct {
			BaseURL string `json:"baseUrl"`
			API     string `json:"api"`
			APIKey  string `json:"apiKey"`
			Models  []struct {
				ID    string   `json:"id"`
				Input []string `json:"input"`
			} `json:"models"`
		} `json:"providers"`
	}{Providers: make(map[string]struct {
		BaseURL string `json:"baseUrl"`
		API     string `json:"api"`
		APIKey  string `json:"apiKey"`
		Models  []struct {
			ID    string   `json:"id"`
			Input []string `json:"input"`
		} `json:"models"`
	})}
	provider := struct {
		BaseURL string `json:"baseUrl"`
		API     string `json:"api"`
		APIKey  string `json:"apiKey"`
		Models  []struct {
			ID    string   `json:"id"`
			Input []string `json:"input"`
		} `json:"models"`
	}{BaseURL: endpoint, API: api, APIKey: "$SCRIPTBOARD_PI_API_KEY"}
	provider.Models = append(provider.Models, struct {
		ID    string   `json:"id"`
		Input []string `json:"input"`
	}{ID: model, Input: func() []string {
		if supportsImages {
			return []string{"text", "image"}
		}
		return []string{"text"}
	}()})
	document.Providers[privateProviderName] = provider
	payload, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode private Pi model configuration: %w", err)
	}
	payload = append(payload, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".models-*.tmp")
	if err != nil {
		return fmt.Errorf("create private Pi model configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect private Pi model configuration: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write private Pi model configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync private Pi model configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close private Pi model configuration: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace private Pi model configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("activate private Pi model configuration: %w", err)
	}
	return nil
}
