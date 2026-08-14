package hostfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrProtected = errors.New("path is protected by ScriptBoard")
var ErrConflict = errors.New("file was modified outside ScriptBoard")

var knownBinaryMagic = [][]byte{
	{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
	{0xff, 0xd8, 0xff},
	[]byte("GIF87a"), []byte("GIF89a"), []byte("%PDF-"),
	{'P', 'K', 0x03, 0x04}, {'P', 'K', 0x05, 0x06}, {'P', 'K', 0x07, 0x08},
	{0x1f, 0x8b}, {0x37, 0x7a, 0xbc, 0xaf, 0x27, 0x1c},
	[]byte("Rar!\x1a\x07"), {0x7f, 'E', 'L', 'F'},
	{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1},
	[]byte("SQLite format 3\x00"), {0x00, 'a', 's', 'm'},
}

type Kind string

const (
	Directory  Kind = "directory"
	Regular    Kind = "regular"
	Restricted Kind = "restricted"
)

type Entry struct {
	Name       string
	Path       string
	Kind       Kind
	VolumeType string
	Size       int64
	ModifiedAt time.Time
	Hidden     bool
}

type TextDocument struct {
	Content string
	Digest  string
}

type Breadcrumb struct {
	Label string
	Path  string
}

type Options struct {
	ProtectedPaths []string
	InstanceID     string
	Topology       Topology
}

type Manager struct {
	protected   []string
	protectedMu sync.RWMutex
	appendMu    sync.Mutex
	instanceID  string
	topology    Topology
	leaseMu     sync.Mutex
	leases      map[string][]string
}

type Topology interface {
	Roots() ([]Entry, error)
	FilesystemRoot(path string) (string, error)
	Restricted(path string) bool
}

// InitialBrowsePath is the canonical landing location for the host file page.
// Windows starts above its discovered volumes; Unix hosts enter / directly.
func (m *Manager) InitialBrowsePath() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return string(filepath.Separator)
}

func Open(options Options) (*Manager, error) {
	instanceID := strings.TrimSpace(options.InstanceID)
	if instanceID == "" {
		instanceID = "default"
	}
	topology := options.Topology
	if topology == nil {
		topology = systemTopology{}
	}
	manager := &Manager{instanceID: instanceID, topology: topology, leases: make(map[string][]string)}
	for _, path := range options.ProtectedPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve protected path %q: %w", path, err)
		}
		absolute = filepath.Clean(absolute)
		manager.addProtectedPath(absolute)
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			manager.addProtectedPath(resolved)
		} else if !unresolvableProtectedPath(err) {
			return nil, fmt.Errorf("resolve protected path %q: %w", path, err)
		}
	}
	return manager, nil
}

func (m *Manager) addProtectedPath(path string) {
	m.protectedMu.Lock()
	defer m.protectedMu.Unlock()
	m.addProtectedPathLocked(path)
}

func (m *Manager) addProtectedPathLocked(path string) {
	path = filepath.Clean(path)
	key := ComparisonKey(path)
	for _, existing := range m.protected {
		if ComparisonKey(existing) == key {
			return
		}
	}
	m.protected = append(m.protected, path)
}

// Protect adds a runtime-managed private path to the host-filesystem seam.
// Both its lexical and resolved forms are protected from reads and mutation.
func (m *Manager) Protect(path string) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("protected host path must be absolute")
	}
	absolute := filepath.Clean(path)
	m.addProtectedPath(absolute)
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		m.addProtectedPath(resolved)
	} else if !unresolvableProtectedPath(err) {
		return fmt.Errorf("resolve protected path %q: %w", path, err)
	}
	return nil
}

func unresolvableProtectedPath(err error) bool {
	// A Broker-only path is intentionally unreadable to the Web service. Its
	// lexical path (and Windows comparison key, which expands accessible 8.3
	// ancestors) remains protected while the OS ACL supplies the hard boundary.
	return os.IsNotExist(err) || errors.Is(err, os.ErrPermission)
}

func (m *Manager) List(path string) ([]Entry, error) {
	if strings.TrimSpace(path) == "" {
		return m.Roots()
	}
	directory, err := m.resolveDirectory(path)
	if err != nil {
		return nil, err
	}
	handle, err := os.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	defer handle.Close()
	directoryEntries, err := handle.ReadDir(100_001)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	if len(directoryEntries) > 100_000 {
		return nil, fmt.Errorf("directory contains more than 100,000 entries")
	}

	entries := make([]Entry, 0, len(directoryEntries))
	for _, directoryEntry := range directoryEntries {
		candidate := filepath.Join(directory, directoryEntry.Name())
		if m.isInsideProtected(candidate) || reservedPath(candidate) {
			continue
		}
		info, err := os.Lstat(candidate)
		if err != nil {
			entries = append(entries, Entry{Name: directoryEntry.Name(), Path: candidate, Kind: Restricted})
			continue
		}
		kind := Restricted
		switch {
		case info.Mode().IsRegular():
			kind = Regular
		case info.IsDir() && !restrictedEntry(candidate, info) && !m.topology.Restricted(candidate):
			kind = Directory
		}
		entries = append(entries, Entry{
			Name: directoryEntry.Name(), Path: candidate, Kind: kind,
			Size: info.Size(), ModifiedAt: info.ModTime(), Hidden: entryHidden(candidate, info),
		})
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].Kind == Directory && entries[right].Kind != Directory {
			return true
		}
		if entries[left].Kind != Directory && entries[right].Kind == Directory {
			return false
		}
		return strings.ToLower(entries[left].Name) < strings.ToLower(entries[right].Name)
	})
	return entries, nil
}

func (m *Manager) ReadText(path string, maxBytes int64) (TextDocument, error) {
	file, _, err := m.OpenRegular(path)
	if err != nil {
		return TextDocument{}, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return TextDocument{}, fmt.Errorf("read text file: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return TextDocument{}, fmt.Errorf("file exceeds %d byte text limit", maxBytes)
	}
	if !likelyTextContent(content) {
		return TextDocument{}, fmt.Errorf("file is not safe UTF-8 text")
	}
	digest := sha256.Sum256(content)
	return TextDocument{Content: string(content), Digest: hex.EncodeToString(digest[:])}, nil
}

// IsLikelyText samples a regular file and reports whether the sampled bytes are
// well-formed UTF-8 whose contents are predominantly printable text. It is a
// bounded hint for offering previews; ReadText remains the final validation.
func (m *Manager) IsLikelyText(path string, sampleBytes int64) (bool, error) {
	if sampleBytes <= 0 {
		return false, fmt.Errorf("text sample limit must be positive")
	}
	file, info, err := m.OpenRegular(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, sampleBytes))
	if err != nil {
		return false, fmt.Errorf("sample text file: %w", err)
	}
	if info.Size() > sampleBytes && !utf8.Valid(content) {
		for trim := 1; trim < utf8.UTFMax && trim < len(content); trim++ {
			candidate := content[:len(content)-trim]
			if utf8.Valid(candidate) {
				return likelyTextContent(candidate), nil
			}
		}
	}
	return likelyTextContent(content), nil
}

func likelyTextContent(content []byte) bool {
	if hasKnownBinaryMagic(content) || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return false
	}
	controlCount := 0
	runeCount := 0
	for _, value := range string(content) {
		runeCount++
		if unicode.IsControl(value) && value != '\t' && value != '\n' && value != '\r' && value != '\f' {
			controlCount++
		}
	}
	return runeCount == 0 || controlCount*100 <= runeCount
}

// IsLikelyTextContent applies the bounded content check used by Host Files
// previews without requiring callers to read an entire file first.
func IsLikelyTextContent(content []byte) bool {
	return likelyTextContent(content)
}

func hasKnownBinaryMagic(content []byte) bool {
	for _, signature := range knownBinaryMagic {
		if bytes.HasPrefix(content, signature) {
			return true
		}
	}
	return false
}

func (m *Manager) OpenRegular(path string) (*os.File, os.FileInfo, error) {
	target, info, err := m.resolveEntry(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("path is not a regular file")
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nil, fmt.Errorf("open file: %w", err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("inspect opened file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("file changed while it was being opened")
	}
	return file, openedInfo, nil
}

func (m *Manager) resolveDirectory(path string) (string, error) {
	target, info, err := m.resolve(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || restrictedEntry(target, info) {
		return "", fmt.Errorf("path is not an enterable directory")
	}
	return target, nil
}

func (m *Manager) resolveEntry(path string) (string, os.FileInfo, error) {
	return m.resolve(path)
}

func (m *Manager) resolve(path string) (string, os.FileInfo, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", nil, fmt.Errorf("host path must be absolute")
	}
	target := filepath.Clean(path)
	if m.isInsideProtected(target) || reservedPath(target) {
		return "", nil, ErrProtected
	}
	root := filepath.VolumeName(target) + string(filepath.Separator)
	if runtime.GOOS != "windows" {
		root = string(filepath.Separator)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("host path is outside its filesystem root")
	}
	current := filepath.Clean(root)
	info, err := os.Lstat(current)
	if err != nil {
		return "", nil, err
	}
	if relative == "." {
		return current, info, nil
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", nil, fmt.Errorf("host path contains an invalid component")
		}
		current = filepath.Join(current, part)
		if m.isInsideProtected(current) || reservedPath(current) {
			return "", nil, ErrProtected
		}
		info, err = os.Lstat(current)
		if err != nil {
			return "", nil, err
		}
		if restrictedEntry(current, info) {
			return "", nil, fmt.Errorf("restricted link cannot be followed")
		}
		if info.IsDir() && m.topology.Restricted(current) {
			return "", nil, fmt.Errorf("restricted virtual filesystem cannot be entered")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", nil, fmt.Errorf("host path ancestor is not a directory")
		}
	}
	return current, info, nil
}

// CanonicalExisting returns the normalized absolute path of an accessible
// host entry without following links or reparse points.
func (m *Manager) CanonicalExisting(path string) (string, error) {
	target, _, err := m.resolveEntry(path)
	return target, err
}

// CanonicalDirectory returns the normalized absolute path of an enterable
// host directory without following links or entering a protected filesystem.
func (m *Manager) CanonicalDirectory(path string) (string, error) {
	return m.resolveDirectory(path)
}

// CanonicalDestination normalizes a not-yet-existing child below an accessible
// directory and applies the same protection policy used by mutations.
func (m *Manager) CanonicalDestination(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("host path must be absolute")
	}
	clean := filepath.Clean(path)
	return m.Destination(filepath.Dir(clean), filepath.Base(clean))
}

// Destination returns the canonical absolute path for a named child of an
// accessible host directory. Callers never need to concatenate host paths.
func (m *Manager) Destination(directory, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	parent, err := m.resolveDirectory(directory)
	if err != nil {
		return "", err
	}
	target := filepath.Join(parent, name)
	if err := m.ensureMutationAllowed(target); err != nil {
		return "", err
	}
	return target, nil
}

// Parent returns the canonical lexical parent of an absolute host path.
func Parent(path string) (string, bool) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.Clean(path)
	parent := filepath.Dir(clean)
	if parent == clean {
		return "", false
	}
	return parent, true
}

// Base returns the final component of a normalized host path.
func Base(path string) string {
	return filepath.Base(filepath.Clean(path))
}

// Extension returns the final extension of a normalized host path.
func Extension(path string) string {
	return filepath.Ext(filepath.Clean(path))
}

// Rebase moves an absolute descendant path from one absolute tree prefix to
// another using platform path semantics.
func Rebase(source, destination, path string) (string, error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || !filepath.IsAbs(path) {
		return "", fmt.Errorf("rebased host paths must be absolute")
	}
	if !pathContains(source, path) {
		return "", fmt.Errorf("host path is outside the source tree")
	}
	// Use the same canonical spellings as the containment check. On Windows an
	// existing ancestor may arrive once as an 8.3 alias and once as its long
	// name; a lexical Rel between those spellings would incorrectly escape the
	// source even after the security boundary accepted it.
	relative, err := filepath.Rel(canonicalComparisonPath(source), canonicalComparisonPath(path))
	if err != nil {
		return "", err
	}
	if relative == "." {
		return filepath.Clean(destination), nil
	}
	return filepath.Join(filepath.Clean(destination), relative), nil
}

// Breadcrumbs describes every absolute prefix from a filesystem or volume
// root to path. URL construction remains a presentation concern.
func Breadcrumbs(path string) []Breadcrumb {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return nil
	}
	clean := filepath.Clean(path)
	root := string(filepath.Separator)
	label := root
	if volume := filepath.VolumeName(clean); volume != "" {
		root = volume + string(filepath.Separator)
		label = volume
	}
	items := []Breadcrumb{{Label: label, Path: filepath.Clean(root)}}
	relative, err := filepath.Rel(root, clean)
	if err != nil || relative == "." {
		return items
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		items = append(items, Breadcrumb{Label: component, Path: current})
	}
	return items
}

// ComparisonKey is the platform comparison form persisted alongside a host
// path. Windows paths are case-insensitive; Unix paths retain case.
func ComparisonKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

// Contains reports whether child is the same path as parent or is below it
// using the host platform's path comparison rules.
func Contains(parent, child string) bool {
	return pathContains(parent, child)
}

// CanMutate reports whether an existing entry can be edited, moved, deleted,
// or overwritten under the centralized host protection policy.
func (m *Manager) CanMutate(path string) bool {
	target, info, err := m.resolveEntry(path)
	if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return false
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return false
	}
	root, err := m.topology.FilesystemRoot(target)
	return err == nil && ComparisonKey(root) != ComparisonKey(target)
}

// IsFilesystemRoot reports whether path is the root of its actual volume or
// mounted filesystem. Unlike a lexical parent check, it also recognizes Unix
// mount points such as /mnt/data.
func IsFilesystemRoot(path string) (bool, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return false, fmt.Errorf("host path must be absolute")
	}
	target := filepath.Clean(path)
	root, err := filesystemRoot(target)
	if err != nil {
		return false, err
	}
	return ComparisonKey(root) == ComparisonKey(target), nil
}

func (m *Manager) isInsideProtected(path string) bool {
	m.protectedMu.RLock()
	defer m.protectedMu.RUnlock()
	for _, protected := range m.protected {
		if pathContains(protected, path) {
			return true
		}
	}
	return false
}

func (m *Manager) ensureMutationAllowed(path string) error {
	if reservedPath(path) {
		return ErrProtected
	}
	m.protectedMu.RLock()
	defer m.protectedMu.RUnlock()
	for _, protected := range m.protected {
		if pathContains(protected, path) || pathContains(path, protected) {
			return ErrProtected
		}
	}
	return nil
}

func pathContains(parent, child string) bool {
	parent = canonicalComparisonPath(parent)
	child = canonicalComparisonPath(child)
	if runtime.GOOS == "windows" {
		parent = strings.ToLower(parent)
		child = strings.ToLower(child)
	}
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
