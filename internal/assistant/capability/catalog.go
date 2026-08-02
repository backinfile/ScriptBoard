// Package capability validates and resolves the fixed, signed-runtime
// guidance that ScriptBoard may add to a managed Pi session.
package capability

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidBundle = errors.New("assistant capability bundle is invalid")
	ErrNotFound      = errors.New("assistant capability is not available")
)

const maximumManifestBytes = 64 << 10

type manifest struct {
	Schema                 int        `json:"schema"`
	BundleVersion          string     `json:"bundleVersion"`
	BrokerContract         int        `json:"brokerContract"`
	MaximumInjectionBytes  int64      `json:"maximumInjectionBytes"`
	MaximumEstimatedTokens int64      `json:"maximumEstimatedTokens"`
	Resources              []resource `json:"resources"`
}

type resource struct {
	ID, Version, Type, Path, SHA256, LabelKey, DescriptionKey string
	Size                                                      int64
	Roles, RequiredReadTools                                  []string
	AutomaticSelection                                        bool
}

func (item *resource) UnmarshalJSON(data []byte) error {
	type document struct {
		ID                 string   `json:"id"`
		Version            string   `json:"version"`
		Type               string   `json:"type"`
		Path               string   `json:"path"`
		Size               int64    `json:"size"`
		SHA256             string   `json:"sha256"`
		LabelKey           string   `json:"labelKey"`
		DescriptionKey     string   `json:"descriptionKey"`
		Roles              []string `json:"roles"`
		RequiredReadTools  []string `json:"requiredReadTools"`
		AutomaticSelection bool     `json:"automaticSelection"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value document
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := ensureEOF(decoder); err != nil {
		return err
	}
	*item = resource{
		ID: value.ID, Version: value.Version, Type: value.Type, Path: value.Path, Size: value.Size,
		SHA256: value.SHA256, LabelKey: value.LabelKey, DescriptionKey: value.DescriptionKey,
		Roles: value.Roles, RequiredReadTools: value.RequiredReadTools, AutomaticSelection: value.AutomaticSelection,
	}
	return nil
}

type Summary struct {
	ID, Version, LabelKey, DescriptionKey string
	Roles, RequiredReadTools              []string
	AutomaticSelection                    bool
}

type Playbook struct {
	Summary
	Guidance string
}

type Catalog struct {
	version   string
	resources map[string]Playbook
}

func Load(root string) (*Catalog, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || strings.TrimSpace(root) == "" {
		return nil, invalid("bundle root is unavailable")
	}
	if info, statErr := os.Lstat(root); statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, invalid("bundle root is unsafe")
	}
	manifestPath := filepath.Join(root, "capabilities.json")
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximumManifestBytes {
		return nil, invalid("manifest is missing or unsafe")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, invalid(err.Error())
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return nil, invalid("manifest has duplicate or malformed JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document manifest
	if err := decoder.Decode(&document); err != nil || ensureEOF(decoder) != nil {
		return nil, invalid("manifest does not match the capability schema")
	}
	if document.Schema != 1 || document.BrokerContract != 1 || !validVersion(document.BundleVersion) ||
		document.MaximumInjectionBytes <= 0 || document.MaximumInjectionBytes > 32<<10 ||
		document.MaximumEstimatedTokens <= 0 || document.MaximumEstimatedTokens > 8000 ||
		len(document.Resources) == 0 || len(document.Resources) > 32 {
		return nil, invalid("manifest metadata is incompatible")
	}
	catalog := &Catalog{version: document.BundleVersion, resources: make(map[string]Playbook, len(document.Resources))}
	var totalBytes int64
	for _, item := range document.Resources {
		if err := validateResource(root, item); err != nil {
			return nil, err
		}
		if _, duplicate := catalog.resources[item.ID]; duplicate {
			return nil, invalid("resource ID is duplicated")
		}
		path := filepath.Join(root, filepath.FromSlash(item.Path))
		body, err := os.ReadFile(path)
		if err != nil || int64(len(body)) != item.Size || !utf8.Valid(body) {
			return nil, invalid("resource content is invalid")
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != item.SHA256 {
			return nil, invalid("resource digest does not match")
		}
		totalBytes += item.Size
		if totalBytes > document.MaximumInjectionBytes {
			return nil, invalid("resource injection budget is exceeded")
		}
		catalog.resources[item.ID] = Playbook{Summary: Summary{
			ID: item.ID, Version: item.Version, LabelKey: item.LabelKey, DescriptionKey: item.DescriptionKey,
			Roles: append([]string(nil), item.Roles...), RequiredReadTools: append([]string(nil), item.RequiredReadTools...),
			AutomaticSelection: item.AutomaticSelection,
		}, Guidance: string(body)}
	}
	return catalog, nil
}

func (catalog *Catalog) BundleVersion() string { return catalog.version }

func (catalog *Catalog) List() []Summary {
	result := make([]Summary, 0, len(catalog.resources))
	for _, item := range catalog.resources {
		item.Guidance = ""
		result = append(result, item.Summary)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func (catalog *Catalog) Resolve(id, version string) (Playbook, error) {
	item, exists := catalog.resources[strings.TrimSpace(id)]
	if !exists || item.Version != strings.TrimSpace(version) {
		return Playbook{}, ErrNotFound
	}
	item.Roles = append([]string(nil), item.Roles...)
	item.RequiredReadTools = append([]string(nil), item.RequiredReadTools...)
	return item, nil
}

func validateResource(root string, item resource) error {
	if !validID(item.ID) || !validVersion(item.Version) || item.Type != "playbook" ||
		item.Path != "playbooks/"+item.ID+".md" || filepath.IsAbs(item.Path) || strings.Contains(item.Path, "\\") ||
		strings.Contains(item.Path, "..") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(item.Path))) != item.Path ||
		item.Size <= 0 || item.Size > 8<<10 || len(item.SHA256) != 64 || item.SHA256 != strings.ToLower(item.SHA256) ||
		!strings.HasPrefix(item.LabelKey, "assistant.profile.") || !strings.HasPrefix(item.DescriptionKey, "assistant.playbook.") ||
		item.AutomaticSelection || !validList(item.Roles, allowedRoles) || !validList(item.RequiredReadTools, allowedReadTools) {
		return invalid("resource metadata is invalid")
	}
	if _, err := hex.DecodeString(item.SHA256); err != nil {
		return invalid("resource digest is invalid")
	}
	path := filepath.Join(root, filepath.FromSlash(item.Path))
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return invalid("resource path escapes the bundle")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != item.Size {
		return invalid("resource is missing or unsafe")
	}
	return nil
}

var allowedRoles = map[string]bool{"viewer": true, "operator": true, "maintainer": true, "administrator": true}
var allowedReadTools = map[string]bool{
	"get_host_status": true, "list_applications": true, "get_application": true, "read_source_log": true,
	"list_website_monitors": true, "get_website_incident": true, "list_runs": true, "get_run": true,
	"read_run_log": true, "list_quick_runs": true, "list_schedules": true, "read_managed_text": true,
	"search_run_log": true, "read_run_log_window": true, "compare_runs": true, "search_source_log": true,
	"get_schedule_history": true, "list_audit_events": true,
}

func validList(values []string, allowed map[string]bool) bool {
	if len(values) == 0 || len(values) > len(allowed) {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !allowed[value] || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func validID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character != '-' && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func validVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate JSON key")
				}
				seen[name] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("unexpected JSON delimiter")
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("unexpected trailing JSON value")
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalidBundle, message) }
