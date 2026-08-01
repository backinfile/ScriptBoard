package pirpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrRuntimeUnavailable = errors.New("the managed Pi runtime is not installed")
	ErrRuntimeInvalid     = errors.New("the managed Pi runtime pointer is invalid")
)

const supportedRPCContract = 1

type ActiveRuntime struct {
	Version, RollbackVersion, Executable, Extension string
	RPCContract                                     int
}

type ActivePointer struct {
	Version         string `json:"version"`
	RollbackVersion string `json:"rollbackVersion,omitempty"`
	RPCContract     int    `json:"rpcContract"`
	Executable      string `json:"executable"`
	Extension       string `json:"extension,omitempty"`
}

// ResolveActiveRuntime never consults PATH. The active pointer may select only
// a regular file inside State Root/assistant/runtime/versions/<version>.
func ResolveActiveRuntime(stateRoot string) (ActiveRuntime, error) {
	absoluteStateRoot, err := filepath.Abs(strings.TrimSpace(stateRoot))
	if err != nil || strings.TrimSpace(stateRoot) == "" {
		return ActiveRuntime{}, fmt.Errorf("%w: State Root is unavailable", ErrRuntimeInvalid)
	}
	runtimeRoot := filepath.Join(absoluteStateRoot, "assistant", "runtime")
	pointerPath := filepath.Join(runtimeRoot, "active.json")
	pointerInfo, err := os.Lstat(pointerPath)
	if errors.Is(err, os.ErrNotExist) {
		return ActiveRuntime{}, ErrRuntimeUnavailable
	}
	if err != nil {
		return ActiveRuntime{}, fmt.Errorf("%w: inspect active pointer: %v", ErrRuntimeInvalid, err)
	}
	if !pointerInfo.Mode().IsRegular() || pointerInfo.Mode()&os.ModeSymlink != 0 || pointerInfo.Size() > 16<<10 {
		return ActiveRuntime{}, fmt.Errorf("%w: active pointer is not a bounded regular file", ErrRuntimeInvalid)
	}
	body, err := os.ReadFile(pointerPath)
	if err != nil {
		return ActiveRuntime{}, fmt.Errorf("%w: read active pointer: %v", ErrRuntimeInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var document ActivePointer
	if err := decoder.Decode(&document); err != nil {
		return ActiveRuntime{}, fmt.Errorf("%w: decode active pointer: %v", ErrRuntimeInvalid, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ActiveRuntime{}, fmt.Errorf("%w: decode active pointer: %v", ErrRuntimeInvalid, err)
	}
	version, err := safeRuntimeVersion(document.Version)
	if err != nil || document.RPCContract != supportedRPCContract {
		return ActiveRuntime{}, fmt.Errorf("%w: unsupported version or RPC contract", ErrRuntimeInvalid)
	}
	executableName := strings.TrimSpace(document.Executable)
	if executableName == "" || filepath.Base(executableName) != executableName || executableName != runtimeExecutableName() {
		return ActiveRuntime{}, fmt.Errorf("%w: unexpected executable name", ErrRuntimeInvalid)
	}
	versionRoot := filepath.Join(runtimeRoot, "versions", version)
	executable := filepath.Join(versionRoot, executableName)
	if err := validateRuntimeFile(versionRoot, executable); err != nil {
		return ActiveRuntime{}, err
	}
	extension := ""
	if document.Extension != "" {
		extensionName := strings.TrimSpace(document.Extension)
		if filepath.Base(extensionName) != extensionName {
			return ActiveRuntime{}, fmt.Errorf("%w: unexpected extension name", ErrRuntimeInvalid)
		}
		extension = filepath.Join(versionRoot, extensionName)
		if err := validateRuntimeFile(versionRoot, extension); err != nil {
			return ActiveRuntime{}, err
		}
	}
	rollbackVersion := ""
	if document.RollbackVersion != "" {
		rollbackVersion, err = safeRuntimeVersion(document.RollbackVersion)
		if err != nil || rollbackVersion == version {
			return ActiveRuntime{}, fmt.Errorf("%w: invalid rollback version", ErrRuntimeInvalid)
		}
		rollbackExecutable := filepath.Join(runtimeRoot, "versions", rollbackVersion, runtimeExecutableName())
		if err := validateRuntimeFile(filepath.Dir(rollbackExecutable), rollbackExecutable); err != nil {
			return ActiveRuntime{}, fmt.Errorf("%w: rollback runtime is invalid", ErrRuntimeInvalid)
		}
	}
	return ActiveRuntime{Version: version, RollbackVersion: rollbackVersion, Executable: executable, Extension: extension, RPCContract: document.RPCContract}, nil
}

// WriteActiveRuntime replaces the single active/rollback pointer on the same
// volume. It validates both targets before publishing either version.
func WriteActiveRuntime(stateRoot string, pointer ActivePointer) error {
	absoluteStateRoot, err := filepath.Abs(strings.TrimSpace(stateRoot))
	if err != nil || strings.TrimSpace(stateRoot) == "" {
		return fmt.Errorf("%w: State Root is unavailable", ErrRuntimeInvalid)
	}
	version, err := safeRuntimeVersion(pointer.Version)
	if err != nil || pointer.RPCContract != supportedRPCContract || pointer.Executable != runtimeExecutableName() {
		return fmt.Errorf("%w: active runtime pointer is incompatible", ErrRuntimeInvalid)
	}
	runtimeRoot := filepath.Join(absoluteStateRoot, "assistant", "runtime")
	versionRoot := filepath.Join(runtimeRoot, "versions", version)
	if err := validateRuntimeFile(versionRoot, filepath.Join(versionRoot, pointer.Executable)); err != nil {
		return err
	}
	if pointer.Extension != "" {
		if filepath.Base(pointer.Extension) != pointer.Extension {
			return fmt.Errorf("%w: unexpected extension name", ErrRuntimeInvalid)
		}
		if err := validateRuntimeFile(versionRoot, filepath.Join(versionRoot, pointer.Extension)); err != nil {
			return err
		}
	}
	if pointer.RollbackVersion != "" {
		rollback, rollbackErr := safeRuntimeVersion(pointer.RollbackVersion)
		if rollbackErr != nil || rollback == version {
			return fmt.Errorf("%w: invalid rollback version", ErrRuntimeInvalid)
		}
		rollbackRoot := filepath.Join(runtimeRoot, "versions", rollback)
		if err := validateRuntimeFile(rollbackRoot, filepath.Join(rollbackRoot, runtimeExecutableName())); err != nil {
			return err
		}
	}
	pointer.Version = version
	payload, err := json.Marshal(pointer)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(runtimeRoot, ".active-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceRuntimePointer(temporaryPath, filepath.Join(runtimeRoot, "active.json"))
}

func safeRuntimeVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || strings.Contains(value, "..") || strings.ContainsAny(value, "/\\\x00") {
		return "", fmt.Errorf("unsafe runtime version")
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.') {
			return "", fmt.Errorf("unsafe runtime version")
		}
	}
	return value, nil
}

func validateRuntimeFile(versionRoot, path string) error {
	if !pathWithin(versionRoot, path) {
		return fmt.Errorf("%w: runtime file escapes its version", ErrRuntimeInvalid)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: inspect runtime file: %v", ErrRuntimeInvalid, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: runtime file is not regular", ErrRuntimeInvalid)
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("unexpected trailing JSON value")
}
