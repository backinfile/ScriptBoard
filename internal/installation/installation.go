package installation

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"scriptboard/internal/buildinfo"
)

const (
	MetadataSchema  = 1
	metadataName    = "install.json"
	installRefName  = "install-ref.json"
	updatesDirName  = "updates"
	versionsDirName = "versions"
)

type Metadata struct {
	Schema        int    `json:"schema"`
	InstallID     string `json:"install_id"`
	InstallRoot   string `json:"install_root"`
	StateRoot     string `json:"state_root"`
	ServiceName   string `json:"service_name"`
	ConfigPath    string `json:"config_path"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	ManagedLayout bool   `json:"managed_layout"`
	Current       string `json:"current_version"`
}

type Reference struct {
	Schema      int    `json:"schema"`
	InstallID   string `json:"install_id"`
	InstallRoot string `json:"install_root"`
}

type PrepareOptions struct {
	SourceRoot  string
	InstallRoot string
	StateRoot   string
	ConfigPath  string
	Build       buildinfo.Info
}

func DefaultRoot() string {
	return defaultRoot()
}

func Prepare(options PrepareOptions) (Metadata, error) {
	if !options.Build.ValidRelease() {
		return Metadata{}, errors.New("service install requires a formal ScriptBoard release build")
	}
	sourceRoot, err := canonicalDirectory(options.SourceRoot)
	if err != nil {
		return Metadata{}, fmt.Errorf("source release directory: %w", err)
	}
	installRoot, err := canonicalNewPath(options.InstallRoot)
	if err != nil {
		return Metadata{}, fmt.Errorf("install root: %w", err)
	}
	stateRootPath, err := filepath.Abs(options.StateRoot)
	if err != nil {
		return Metadata{}, fmt.Errorf("state root: %w", err)
	}
	if err := os.MkdirAll(stateRootPath, 0o700); err != nil {
		return Metadata{}, fmt.Errorf("create state root: %w", err)
	}
	stateRoot, err := canonicalDirectory(stateRootPath)
	if err != nil {
		return Metadata{}, fmt.Errorf("state root: %w", err)
	}
	configPath, err := filepath.Abs(options.ConfigPath)
	if err != nil {
		return Metadata{}, fmt.Errorf("config path: %w", err)
	}
	if pathsOverlap(installRoot, stateRoot) {
		return Metadata{}, errors.New("Install Root and State Root cannot contain one another")
	}
	if err := validateReleaseInfo(sourceRoot, options.Build); err != nil {
		return Metadata{}, err
	}
	if err := os.MkdirAll(filepath.Join(installRoot, versionsDirName), 0o755); err != nil {
		return Metadata{}, err
	}
	existing, existingErr := loadMetadata(filepath.Join(installRoot, metadataName))
	if existingErr != nil && !os.IsNotExist(existingErr) {
		return Metadata{}, fmt.Errorf("existing install metadata is invalid: %w", existingErr)
	}
	if existingErr == nil && (!existing.ManagedLayout || existing.Schema != MetadataSchema) {
		return Metadata{}, errors.New("existing Install Root is not a supported managed installation")
	}
	installID := existing.InstallID
	if installID == "" {
		installID, err = randomID()
		if err != nil {
			return Metadata{}, err
		}
	}
	versionRoot := filepath.Join(installRoot, versionsDirName, options.Build.Version)
	if err := prepareVersion(sourceRoot, versionRoot, options.Build); err != nil {
		return Metadata{}, err
	}
	if err := installStableFiles(sourceRoot, installRoot); err != nil {
		return Metadata{}, err
	}
	if err := activateVersion(installRoot, versionRoot); err != nil {
		return Metadata{}, err
	}
	metadata := Metadata{
		Schema: MetadataSchema, InstallID: installID, InstallRoot: installRoot,
		StateRoot: stateRoot, ServiceName: "ScriptBoard", ConfigPath: configPath, OS: runtime.GOOS, Arch: runtime.GOARCH,
		ManagedLayout: true, Current: options.Build.Version,
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	if err := writeJSONAtomic(filepath.Join(installRoot, metadataName), metadata, 0o644); err != nil {
		return Metadata{}, err
	}
	reference := Reference{Schema: MetadataSchema, InstallID: installID, InstallRoot: installRoot}
	if err := writeJSONAtomic(filepath.Join(stateRoot, updatesDirName, installRefName), reference, 0o600); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func Detect(stateRoot string) (Metadata, error) {
	metadata, err := Load(stateRoot)
	if err != nil {
		return Metadata{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return Metadata{}, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return Metadata{}, err
	}
	versionRoot := filepath.Join(metadata.InstallRoot, versionsDirName, metadata.Current)
	if !pathWithin(versionRoot, executable) {
		return Metadata{}, errors.New("running executable is outside the active Installed Release")
	}
	return metadata, nil
}

func Load(stateRoot string) (Metadata, error) {
	canonicalStateRoot, err := canonicalDirectory(stateRoot)
	if err != nil {
		return Metadata{}, err
	}
	referencePath := filepath.Join(canonicalStateRoot, updatesDirName, installRefName)
	var reference Reference
	if err := decodeStrictFile(referencePath, &reference); err != nil {
		return Metadata{}, err
	}
	if reference.Schema != MetadataSchema || reference.InstallID == "" || !filepath.IsAbs(reference.InstallRoot) {
		return Metadata{}, errors.New("unsupported install reference")
	}
	metadata, err := loadMetadata(filepath.Join(reference.InstallRoot, metadataName))
	if err != nil {
		return Metadata{}, err
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	if metadata.InstallID != reference.InstallID ||
		!samePath(metadata.InstallRoot, reference.InstallRoot) || !samePath(metadata.StateRoot, canonicalStateRoot) {
		return Metadata{}, errors.New("install reference does not match Install Root metadata")
	}
	return metadata, nil
}

func (m Metadata) Validate() error {
	if m.Schema != MetadataSchema || !m.ManagedLayout || m.InstallID == "" || m.ServiceName != "ScriptBoard" {
		return errors.New("unsupported install metadata")
	}
	if !filepath.IsAbs(m.InstallRoot) || !filepath.IsAbs(m.StateRoot) || !filepath.IsAbs(m.ConfigPath) {
		return errors.New("install metadata paths must be absolute")
	}
	if pathsOverlap(m.InstallRoot, m.StateRoot) {
		return errors.New("install metadata roots overlap")
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return errors.New("install metadata platform does not match this executable")
	}
	if !stableVersion(m.Current) {
		return errors.New("install metadata contains an invalid current version")
	}
	return nil
}

func VersionRoot(metadata Metadata, version string) string {
	return filepath.Join(metadata.InstallRoot, versionsDirName, version)
}

func StageVersion(metadata Metadata, sourceRoot string, info buildinfo.Info) error {
	if err := metadata.Validate(); err != nil {
		return err
	}
	if !info.ValidRelease() || info.Version == metadata.Current {
		return errors.New("target must be a different formal release")
	}
	sourceRoot, err := canonicalDirectory(sourceRoot)
	if err != nil {
		return err
	}
	if err := validateReleaseInfo(sourceRoot, info); err != nil {
		return err
	}
	return prepareVersion(sourceRoot, VersionRoot(metadata, info.Version), info)
}

func ServiceExecutable(metadata Metadata) string {
	name := "scriptboard"
	if metadata.OS == "windows" {
		name += ".exe"
	}
	return filepath.Join(VersionRoot(metadata, metadata.Current), name)
}

func ServiceEntryExecutable(metadata Metadata) string {
	if metadata.OS == "linux" {
		return filepath.Join(metadata.InstallRoot, "current", "scriptboard")
	}
	return ServiceExecutable(metadata)
}

func UpdaterExecutable(metadata Metadata) string {
	name := "scriptboard-updater"
	if metadata.OS == "windows" {
		name += ".exe"
	}
	return filepath.Join(VersionRoot(metadata, metadata.Current), name)
}

func ServiceUpdaterExecutable(metadata Metadata) string {
	if metadata.OS == "linux" {
		return filepath.Join(metadata.InstallRoot, "scriptboard-updater")
	}
	return UpdaterExecutable(metadata)
}

func SetCurrent(metadata Metadata, version string) (Metadata, error) {
	if !stableVersion(version) {
		return Metadata{}, errors.New("invalid Installed Release version")
	}
	versionRoot := VersionRoot(metadata, version)
	if info, err := os.Stat(versionRoot); err != nil || !info.IsDir() {
		return Metadata{}, errors.New("Installed Release directory does not exist")
	}
	if err := activateVersion(metadata.InstallRoot, versionRoot); err != nil {
		return Metadata{}, err
	}
	metadata.Current = version
	if err := writeJSONAtomic(filepath.Join(metadata.InstallRoot, metadataName), metadata, 0o644); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func ValidateVersion(metadata Metadata, version string, want buildinfo.Info) error {
	if err := metadata.Validate(); err != nil {
		return err
	}
	if version != want.Version || !want.ValidRelease() {
		return errors.New("expected build does not identify the Installed Release")
	}
	required := []string{"scriptboard", "scriptboard-updater", buildinfo.ReleaseInfoFilename}
	if metadata.OS == "windows" {
		required = []string{"scriptboard.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe", "scriptboard-updater.exe", buildinfo.ReleaseInfoFilename}
	}
	return validateInstalledVersion(VersionRoot(metadata, version), want, required)
}

func ReadVersionInfo(metadata Metadata, version string) (buildinfo.Info, error) {
	if err := metadata.Validate(); err != nil {
		return buildinfo.Info{}, err
	}
	if !stableVersion(version) {
		return buildinfo.Info{}, errors.New("invalid Installed Release version")
	}
	var info buildinfo.Info
	if err := decodeStrictFile(filepath.Join(VersionRoot(metadata, version), buildinfo.ReleaseInfoFilename), &info); err != nil {
		return buildinfo.Info{}, err
	}
	if !info.ValidRelease() || info.Version != version {
		return buildinfo.Info{}, errors.New("Installed Release metadata is invalid")
	}
	return info, nil
}

func validateReleaseInfo(sourceRoot string, want buildinfo.Info) error {
	var got buildinfo.Info
	if err := decodeStrictFile(filepath.Join(sourceRoot, buildinfo.ReleaseInfoFilename), &got); err != nil {
		return fmt.Errorf("read RELEASE.json: %w", err)
	}
	if got != want || !got.ValidRelease() {
		return errors.New("RELEASE.json does not match the running release binary")
	}
	return nil
}

func prepareVersion(sourceRoot, versionRoot string, info buildinfo.Info) error {
	required := []string{"scriptboard", "scriptboard-updater", buildinfo.ReleaseInfoFilename}
	optional := []string{"README.md", "README_EN.md", "LICENSE"}
	if runtime.GOOS == "windows" {
		required = []string{"scriptboard.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe", "scriptboard-updater.exe", buildinfo.ReleaseInfoFilename}
	}
	if existing, err := os.Stat(versionRoot); err == nil && existing.IsDir() {
		return validateInstalledVersion(versionRoot, info, required)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	partial := versionRoot + ".partial"
	if err := os.RemoveAll(partial); err != nil {
		return err
	}
	if err := os.MkdirAll(partial, 0o755); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(partial)
		}
	}()
	for _, name := range append(required, optional...) {
		source := filepath.Join(sourceRoot, name)
		if _, err := os.Stat(source); os.IsNotExist(err) && contains(required, name) {
			return fmt.Errorf("formal release is missing %s", name)
		} else if os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(name, "scriptboard") && name != buildinfo.ReleaseInfoFilename {
			mode = 0o755
		}
		if err := copyFile(source, filepath.Join(partial, name), mode); err != nil {
			return err
		}
	}
	if err := validateInstalledVersion(partial, info, required); err != nil {
		return err
	}
	if err := os.Rename(partial, versionRoot); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(filepath.Dir(versionRoot))
}

func validateInstalledVersion(root string, want buildinfo.Info, required []string) error {
	for _, name := range required {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Installed Release is missing regular file %s", name)
		}
	}
	var got buildinfo.Info
	if err := decodeStrictFile(filepath.Join(root, buildinfo.ReleaseInfoFilename), &got); err != nil {
		return err
	}
	if got != want {
		return errors.New("Installed Release metadata does not match the expected build")
	}
	return nil
}

func loadMetadata(path string) (Metadata, error) {
	var metadata Metadata
	if err := decodeStrictFile(path, &metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func decodeStrictFile(path string, destination any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON file contains trailing data")
	}
	return nil
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(output, input); err == nil {
		err = output.Sync()
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	return err
}

func canonicalDirectory(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func canonicalNewPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		if os.IsNotExist(err) {
			return filepath.Clean(absolute), nil
		}
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func pathsOverlap(first, second string) bool {
	return pathWithin(first, second) || pathWithin(second, first)
}

func pathWithin(parent, candidate string) bool {
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func samePath(first, second string) bool {
	return strings.EqualFold(filepath.Clean(first), filepath.Clean(second))
}

func stableVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
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

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
