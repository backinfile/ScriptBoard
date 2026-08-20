package kubeconfigmanager

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const MaxFileSize = 2 << 20

type Context struct {
	Name      string
	Cluster   string
	User      string
	Namespace string
	Current   bool
}

type Snapshot struct {
	Path       string
	Exists     bool
	Writable   bool
	ModifiedAt time.Time
	Size       int64
	Contexts   []Context
	Current    string
}

type ImportPreview struct {
	Clusters       int      `json:"clusters"`
	Users          int      `json:"users"`
	Contexts       int      `json:"contexts"`
	Conflicts      []string `json:"conflicts"`
	CurrentContext string   `json:"currentContext"`
}

type document struct {
	root *yaml.Node
}

func DefaultPath() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("KUBECONFIG")); configured != "" {
		for _, candidate := range filepath.SplitList(configured) {
			if strings.TrimSpace(candidate) != "" {
				absolute, err := filepath.Abs(candidate)
				if err != nil {
					return "", err
				}
				return filepath.Clean(absolute), nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// SuggestedPaths returns common kubeconfig locations without probing the host.
// The caller still decides when to read or save a selected path.
func SuggestedPaths() ([]string, error) {
	defaultPath, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return suggestedPaths(runtime.GOOS, defaultPath), nil
}

func suggestedPaths(goos, defaultPath string) []string {
	paths := []string{
		defaultPath,
		"/etc/rancher/k3s/k3s.yaml",
		"/etc/rancher/rke2/rke2.yaml",
		"/etc/kubernetes/admin.conf",
		"/var/snap/microk8s/current/credentials/client.config",
		"/var/lib/k0s/pki/admin.conf",
	}
	result := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		if strings.HasPrefix(candidate, "/") {
			candidate = path.Clean(candidate)
		} else {
			candidate = filepath.Clean(candidate)
		}
		if candidate == "." {
			continue
		}
		key := candidate
		if goos == "windows" {
			key = strings.ToLower(candidate)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func Inspect(path string) (Snapshot, error) {
	path, err := cleanAbsolutePath(path)
	if err != nil {
		return Snapshot{}, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{Path: path, Writable: directoryWritable(filepath.Dir(path))}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect kubeconfig: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxFileSize {
		return Snapshot{}, errors.New("kubeconfig must be a regular file no larger than 2 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read kubeconfig: %w", err)
	}
	parsed, err := parse(raw)
	if err != nil {
		return Snapshot{}, err
	}
	current := scalarValue(parsed.root, "current-context")
	contexts, err := parsed.contexts(current)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Path: path, Exists: true, Writable: fileWritable(path), ModifiedAt: info.ModTime(), Size: info.Size(), Contexts: contexts, Current: current}, nil
}

func Download(path string) ([]byte, error) {
	path, err := cleanAbsolutePath(path)
	if err != nil {
		return nil, err
	}
	return readBounded(path)
}

func DownloadContext(path, name string) ([]byte, error) {
	parsed, err := load(path)
	if err != nil {
		return nil, err
	}
	contextNode := findNamed(parsed.root, "contexts", name)
	if contextNode == nil {
		return nil, fmt.Errorf("context %q was not found", name)
	}
	value := mappingValue(contextNode, "context")
	clusterName, userName := scalarValue(value, "cluster"), scalarValue(value, "user")
	out := newDocument()
	appendNamed(out.root, "clusters", findNamed(parsed.root, "clusters", clusterName))
	appendNamed(out.root, "users", findNamed(parsed.root, "users", userName))
	appendNamed(out.root, "contexts", contextNode)
	setScalar(out.root, "current-context", name)
	return out.marshal()
}

func PreviewImport(path string, incoming []byte) (ImportPreview, error) {
	target, err := loadOrNew(path)
	if err != nil {
		return ImportPreview{}, err
	}
	source, err := parseBounded(incoming)
	if err != nil {
		return ImportPreview{}, err
	}
	preview := ImportPreview{CurrentContext: scalarValue(source.root, "current-context")}
	for _, section := range []string{"clusters", "users", "contexts"} {
		entries, err := namedEntries(source.root, section)
		if err != nil {
			return ImportPreview{}, err
		}
		switch section {
		case "clusters":
			preview.Clusters = len(entries)
		case "users":
			preview.Users = len(entries)
		case "contexts":
			preview.Contexts = len(entries)
		}
		for _, entry := range entries {
			name := scalarValue(entry, "name")
			if findNamed(target.root, section, name) != nil {
				preview.Conflicts = append(preview.Conflicts, section+"/"+name)
			}
		}
	}
	return preview, nil
}

func Import(path string, incoming []byte, useImportedCurrent bool) (ImportPreview, error) {
	preview, err := PreviewImport(path, incoming)
	if err != nil {
		return ImportPreview{}, err
	}
	target, err := loadOrNew(path)
	if err != nil {
		return ImportPreview{}, err
	}
	source, err := parseBounded(incoming)
	if err != nil {
		return ImportPreview{}, err
	}
	for _, section := range []string{"clusters", "users", "contexts"} {
		entries, _ := namedEntries(source.root, section)
		for _, entry := range entries {
			replaceNamed(target.root, section, entry)
		}
	}
	if useImportedCurrent && preview.CurrentContext != "" {
		if findNamed(target.root, "contexts", preview.CurrentContext) == nil {
			return ImportPreview{}, errors.New("imported current-context does not reference an imported context")
		}
		setScalar(target.root, "current-context", preview.CurrentContext)
	}
	return preview, write(path, target)
}

func UseContext(path, name string) error {
	parsed, err := load(path)
	if err != nil {
		return err
	}
	if findNamed(parsed.root, "contexts", name) == nil {
		return fmt.Errorf("context %q was not found", name)
	}
	setScalar(parsed.root, "current-context", name)
	return write(path, parsed)
}

func UpdateContext(path, name, cluster, user, namespace string) error {
	parsed, err := load(path)
	if err != nil {
		return err
	}
	entry := findNamed(parsed.root, "contexts", name)
	if entry == nil {
		return fmt.Errorf("context %q was not found", name)
	}
	value := mappingValue(entry, "context")
	if value == nil || value.Kind != yaml.MappingNode {
		return fmt.Errorf("context %q is invalid", name)
	}
	if cluster != "" {
		if findNamed(parsed.root, "clusters", cluster) == nil {
			return fmt.Errorf("cluster %q was not found", cluster)
		}
		setScalar(value, "cluster", cluster)
	}
	if user != "" {
		if findNamed(parsed.root, "users", user) == nil {
			return fmt.Errorf("user %q was not found", user)
		}
		setScalar(value, "user", user)
	}
	setScalar(value, "namespace", strings.TrimSpace(namespace))
	return write(path, parsed)
}

func RenameContext(path, oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" || strings.ContainsAny(newName, "\r\n\x00") {
		return errors.New("new context name is invalid")
	}
	parsed, err := load(path)
	if err != nil {
		return err
	}
	entry := findNamed(parsed.root, "contexts", oldName)
	if entry == nil {
		return fmt.Errorf("context %q was not found", oldName)
	}
	if oldName != newName && findNamed(parsed.root, "contexts", newName) != nil {
		return fmt.Errorf("context %q already exists", newName)
	}
	setScalar(entry, "name", newName)
	if scalarValue(parsed.root, "current-context") == oldName {
		setScalar(parsed.root, "current-context", newName)
	}
	return write(path, parsed)
}

func DeleteContext(path, name string) error {
	parsed, err := load(path)
	if err != nil {
		return err
	}
	sequence := mappingValue(parsed.root, "contexts")
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return fmt.Errorf("context %q was not found", name)
	}
	found := false
	kept := sequence.Content[:0]
	for _, entry := range sequence.Content {
		if scalarValue(entry, "name") == name {
			found = true
			continue
		}
		kept = append(kept, entry)
	}
	if !found {
		return fmt.Errorf("context %q was not found", name)
	}
	sequence.Content = kept
	if scalarValue(parsed.root, "current-context") == name {
		setScalar(parsed.root, "current-context", "")
	}
	return write(path, parsed)
}

func cleanAbsolutePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", errors.New("kubeconfig path must be absolute")
	}
	return filepath.Clean(path), nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxFileSize {
		return nil, errors.New("kubeconfig must be a regular file no larger than 2 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxFileSize+1))
	if err != nil || len(raw) > MaxFileSize {
		return nil, errors.New("unable to read bounded kubeconfig")
	}
	return raw, nil
}

func load(path string) (*document, error) {
	path, err := cleanAbsolutePath(path)
	if err != nil {
		return nil, err
	}
	raw, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	return parse(raw)
}

func loadOrNew(path string) (*document, error) {
	parsed, err := load(path)
	if errors.Is(unwrapPathError(err), os.ErrNotExist) {
		return newDocument(), nil
	}
	return parsed, err
}

func unwrapPathError(err error) error {
	for err != nil {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			return err
		}
		err = unwrapped
	}
	return nil
}

func parseBounded(raw []byte) (*document, error) {
	if len(raw) == 0 || len(raw) > MaxFileSize {
		return nil, errors.New("kubeconfig must be between 1 byte and 2 MiB")
	}
	return parse(raw)
}

func parse(raw []byte) (*document, error) {
	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&node); err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("kubeconfig root must be a mapping")
	}
	root := node.Content[0]
	for _, section := range []string{"clusters", "users", "contexts"} {
		if _, err := namedEntries(root, section); err != nil {
			return nil, err
		}
	}
	return &document{root: root}, nil
}

func newDocument() *document {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setScalar(root, "apiVersion", "v1")
	setScalar(root, "kind", "Config")
	for _, section := range []string{"preferences", "clusters", "users", "contexts"} {
		kind, tag := yaml.SequenceNode, "!!seq"
		if section == "preferences" {
			kind, tag = yaml.MappingNode, "!!map"
		}
		root.Content = append(root.Content, scalarNode(section), &yaml.Node{Kind: kind, Tag: tag})
	}
	setScalar(root, "current-context", "")
	return &document{root: root}
}

func (d *document) contexts(current string) ([]Context, error) {
	entries, err := namedEntries(d.root, "contexts")
	if err != nil {
		return nil, err
	}
	result := make([]Context, 0, len(entries))
	for _, entry := range entries {
		name := scalarValue(entry, "name")
		value := mappingValue(entry, "context")
		if name == "" || value == nil || value.Kind != yaml.MappingNode {
			return nil, errors.New("kubeconfig contains an invalid context entry")
		}
		result = append(result, Context{Name: name, Cluster: scalarValue(value, "cluster"), User: scalarValue(value, "user"), Namespace: scalarValue(value, "namespace"), Current: name == current})
	}
	return result, nil
}

func (d *document) marshal() ([]byte, error) {
	documentNode := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{d.root}}
	return yaml.Marshal(documentNode)
}

func write(path string, parsed *document) error {
	path, err := cleanAbsolutePath(path)
	if err != nil {
		return err
	}
	raw, err := parsed.marshal()
	if err != nil {
		return fmt.Errorf("encode kubeconfig: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create kubeconfig directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".scriptboard-kubeconfig-*")
	if err != nil {
		return fmt.Errorf("create temporary kubeconfig: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
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
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish kubeconfig: %w", err)
	}
	return nil
}

func namedEntries(root *yaml.Node, section string) ([]*yaml.Node, error) {
	sequence := mappingValue(root, section)
	if sequence == nil {
		return nil, nil
	}
	if sequence.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("kubeconfig %s must be a sequence", section)
	}
	for _, entry := range sequence.Content {
		if entry.Kind != yaml.MappingNode || scalarValue(entry, "name") == "" {
			return nil, fmt.Errorf("kubeconfig %s contains an invalid named entry", section)
		}
	}
	return sequence.Content, nil
}

func findNamed(root *yaml.Node, section, name string) *yaml.Node {
	entries, _ := namedEntries(root, section)
	for _, entry := range entries {
		if scalarValue(entry, "name") == name {
			return entry
		}
	}
	return nil
}

func replaceNamed(root *yaml.Node, section string, incoming *yaml.Node) {
	sequence := ensureSequence(root, section)
	name := scalarValue(incoming, "name")
	for index, entry := range sequence.Content {
		if scalarValue(entry, "name") == name {
			sequence.Content[index] = cloneNode(incoming)
			return
		}
	}
	sequence.Content = append(sequence.Content, cloneNode(incoming))
}

func appendNamed(root *yaml.Node, section string, entry *yaml.Node) {
	if entry != nil {
		ensureSequence(root, section).Content = append(ensureSequence(root, section).Content, cloneNode(entry))
	}
}

func ensureSequence(root *yaml.Node, key string) *yaml.Node {
	if value := mappingValue(root, key); value != nil && value.Kind == yaml.SequenceNode {
		return value
	}
	value := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	setMappingValue(root, key, value)
	return value
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func scalarValue(mapping *yaml.Node, key string) string {
	value := mappingValue(mapping, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(value.Value)
}

func setScalar(mapping *yaml.Node, key, value string) {
	setMappingValue(mapping, key, scalarNode(value))
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, scalarNode(key), value)
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func cloneNode(source *yaml.Node) *yaml.Node {
	if source == nil {
		return nil
	}
	copy := *source
	copy.Content = make([]*yaml.Node, len(source.Content))
	for index, child := range source.Content {
		copy.Content[index] = cloneNode(child)
	}
	return &copy
}

func fileWritable(path string) bool {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func directoryWritable(path string) bool {
	for {
		info, err := os.Stat(path)
		if err == nil {
			return info.IsDir() && info.Mode().Perm()&0o200 != 0
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
}
