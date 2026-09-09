package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"go.yaml.in/yaml/v3"
	"scriptboard/internal/resourcelimits"
)

var ErrMemorySettingsConflict = errors.New("configuration changed; reload before saving")
var memorySettingsMu sync.Mutex

type MemorySettings struct {
	Memory   resourcelimits.Memory
	Revision string
}

// ReadMemorySettings exposes only memory values, never the rest of the configuration.
func ReadMemorySettings(path string) (MemorySettings, error) {
	_, _, snapshot, err := readMemoryDocument(path)
	return snapshot, err
}

func readMemoryDocument(path string) ([]byte, *yaml.Node, MemorySettings, error) {
	if path == "" {
		return nil, nil, MemorySettings{}, errors.New("configuration path unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, MemorySettings{}, err
	}
	if err == nil && (!info.Mode().IsRegular() || info.Size() > 1<<20) {
		return nil, nil, MemorySettings{}, errors.New("configuration must be a regular file under 1 MiB")
	}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, MemorySettings{}, err
	}
	var document yaml.Node
	if len(bytes.TrimSpace(body)) == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	} else {
		decoder := yaml.NewDecoder(bytes.NewReader(body))
		decoder.KnownFields(true)
		var values yamlConfig
		if err := decoder.Decode(&values); err != nil {
			return nil, nil, MemorySettings{}, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, nil, MemorySettings{}, errors.New("configuration must contain one YAML document")
		}
		if err := yaml.Unmarshal(body, &document); err != nil {
			return nil, nil, MemorySettings{}, err
		}
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, nil, MemorySettings{}, errors.New("configuration must be a YAML mapping")
	}
	var values yamlConfig
	if err := document.Decode(&values); err != nil {
		return nil, nil, MemorySettings{}, err
	}
	for _, n := range document.Content[0].Content {
		if n.Kind == yaml.AliasNode || n.Tag == "!!merge" || n.Anchor != "" {
			return nil, nil, MemorySettings{}, errors.New("configuration aliases are not editable")
		}
	}
	if err := values.Memory.Validate(); err != nil {
		return nil, nil, MemorySettings{}, err
	}
	hash := sha256.Sum256(body)
	return body, &document, MemorySettings{Memory: values.Memory.Resolved(), Revision: hex.EncodeToString(hash[:])}, nil
}

// SaveMemorySettings preserves unrelated YAML and rejects stale forms. Only four fixed keys are writable.
func SaveMemorySettings(path, revision string, memory resourcelimits.Memory) error {
	if err := memory.Validate(); err != nil {
		return err
	}
	memory = memory.Resolved()
	memorySettingsMu.Lock()
	defer memorySettingsMu.Unlock()
	_, document, current, err := readMemoryDocument(path)
	if err != nil {
		return err
	}
	if revision != current.Revision {
		return ErrMemorySettingsConflict
	}
	mapping := document.Content[0]
	for _, field := range []struct{ key, value string }{{"runner_memory_limit", memory.Total}, {"run_memory_limit", memory.PerRun}, {"runner_process_memory_limit", memory.Process}, {"runner_swap_limit", memory.Swap}} {
		found := false
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == field.key {
				n := mapping.Content[i+1]
				n.Kind = yaml.ScalarNode
				n.Tag = "!!str"
				n.Value = field.value
				n.Content = nil
				n.Alias = nil
				found = true
				break
			}
		}
		if !found {
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field.key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field.value})
		}
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".memory-config-*")
	if err != nil {
		return err
	}
	temporary := temp.Name()
	defer os.Remove(temporary)
	if _, err = temp.Write(output.Bytes()); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	// Recheck after staging so a configuration edit is not silently overwritten.
	latest, err := ReadMemorySettings(path)
	if err != nil {
		return err
	}
	if latest.Revision != revision {
		return ErrMemorySettingsConflict
	}
	return replaceMemoryConfig(temporary, path)
}
