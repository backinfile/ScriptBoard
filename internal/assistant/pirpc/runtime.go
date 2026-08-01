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
	Version, Executable, Extension string
	RPCContract                    int
}

type activeRuntimeDocument struct {
	Version     string `json:"version"`
	RPCContract int    `json:"rpcContract"`
	Executable  string `json:"executable"`
	Extension   string `json:"extension,omitempty"`
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
	var document activeRuntimeDocument
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
	return ActiveRuntime{Version: version, Executable: executable, Extension: extension, RPCContract: document.RPCContract}, nil
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
