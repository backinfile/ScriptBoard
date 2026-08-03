package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"scriptboard/internal/assistant/capability"
	"scriptboard/internal/assistant/runtimeinstall"
	"scriptboard/internal/buildinfo"
	updatepkg "scriptboard/internal/update"
)

type runtimeLock struct {
	Schema     int                `json:"schema"`
	Repository string             `json:"repository"`
	Version    string             `json:"version"`
	Tag        string             `json:"tag"`
	Commit     string             `json:"commit"`
	License    runtimeLockedFile  `json:"license"`
	Assets     []runtimeLockAsset `json:"assets"`
}

type runtimeLockedFile struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type runtimeLockAsset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func generateRuntimePackage(arguments []string) error {
	flags := flag.NewFlagSet("runtime-package", flag.ContinueOnError)
	lockPath := flags.String("lock", "runtime/pi-runtime-lock.json", "pinned Pi runtime lock")
	upstreamPath := flags.String("upstream", "", "downloaded upstream Pi archive")
	licensePath := flags.String("license", "", "downloaded Pi LICENSE")
	extensionPath := flags.String("extension", "runtime/scriptboard-extension.ts", "fixed ScriptBoard extension")
	capabilitiesPath := flags.String("capabilities", "runtime/capabilities.json", "fixed ScriptBoard capability manifest")
	goos := flags.String("os", "", "target operating system")
	goarch := flags.String("arch", "", "target architecture")
	output := flags.String("output", "", "output ScriptBoard runtime archive")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || *upstreamPath == "" || *licensePath == "" || *goos == "" || *goarch == "" || *output == "" {
		return errors.New("runtime-package requires --upstream, --license, --os, --arch, and --output")
	}
	lock, err := readRuntimeLock(*lockPath)
	if err != nil {
		return err
	}
	asset, ok := lock.assetFor(*goos, *goarch)
	if !ok {
		return errors.New("runtime lock has no asset for the requested platform")
	}
	if err := verifyLockedFile(*upstreamPath, asset.Size, asset.SHA256); err != nil {
		return fmt.Errorf("verify pinned upstream Pi archive: %w", err)
	}
	if err := verifyLockedFile(*licensePath, lock.License.Size, lock.License.SHA256); err != nil {
		return fmt.Errorf("verify pinned Pi license: %w", err)
	}
	if err := requireRegularFile(*extensionPath, 1<<20); err != nil {
		return fmt.Errorf("verify fixed ScriptBoard extension: %w", err)
	}
	capabilityRoot := filepath.Dir(*capabilitiesPath)
	catalog, err := capability.Load(capabilityRoot)
	if err != nil || filepath.Base(*capabilitiesPath) != "capabilities.json" {
		return fmt.Errorf("verify fixed ScriptBoard capability bundle: %w", err)
	}
	outputName := runtimeAssetName(lock.Version, *goos, *goarch)
	if filepath.Base(*output) != outputName {
		return fmt.Errorf("runtime output must be named %q", outputName)
	}
	parent := filepath.Dir(*output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".runtime-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	unpacked, _, err := updatepkg.MeasureArchive(*upstreamPath)
	if err != nil {
		return fmt.Errorf("validate upstream Pi archive: %w", err)
	}
	if err := updatepkg.ExtractArchive(*upstreamPath, filepath.Join(stage, "payload"), unpacked); err != nil {
		return fmt.Errorf("extract upstream Pi archive: %w", err)
	}
	payload := filepath.Join(stage, "payload")
	executable := "pi"
	if *goos == "windows" {
		executable = "pi.exe"
	}
	if err := normalizeRuntimePermissions(payload, executable); err != nil {
		return fmt.Errorf("normalize Pi Runtime permissions: %w", err)
	}
	if err := requireRegularFile(filepath.Join(payload, executable), runtimeinstall.MaxUnpackedBytes); err != nil {
		return fmt.Errorf("upstream Pi executable is missing: %w", err)
	}
	if err := verifyPiPackageVersion(payload, lock.Version); err != nil {
		return err
	}
	licenseRaw, err := os.ReadFile(*licensePath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(payload, "LICENSE"), licenseRaw, 0o600); err != nil {
		return err
	}
	extensionRaw, err := os.ReadFile(*extensionPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(payload, "scriptboard-extension.ts"), extensionRaw, 0o600); err != nil {
		return err
	}
	capabilitiesRaw, err := os.ReadFile(*capabilitiesPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(payload, "capabilities.json"), capabilitiesRaw, 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(payload, "playbooks"), 0o700); err != nil {
		return err
	}
	for _, item := range catalog.List() {
		body, err := os.ReadFile(filepath.Join(capabilityRoot, "playbooks", item.ID+".md"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(payload, "playbooks", item.ID+".md"), body, 0o600); err != nil {
			return err
		}
	}
	metadata := runtimeinstall.RuntimeMetadata{
		Schema: 1, Product: runtimeinstall.Product, PiVersion: lock.Version,
		RPCContract: runtimeinstall.RPCContract, BrokerContract: runtimeinstall.BrokerContract,
		Executable: executable, Extension: "scriptboard-extension.ts", Capabilities: "capabilities.json",
		Upstream:       "https://github.com/" + lock.Repository + "/releases/tag/" + lock.Tag,
		UpstreamCommit: lock.Commit,
	}
	metadataRaw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(payload, "runtime.json"), append(metadataRaw, '\n'), 0o600); err != nil {
		return err
	}
	if err := packRuntimeArchive(payload, *output, *goos); err != nil {
		_ = os.Remove(*output)
		return err
	}
	return nil
}

func generateRuntimeManifest(arguments []string) error {
	flags := flag.NewFlagSet("runtime-manifest", flag.ContinueOnError)
	scriptBoardVersion := flags.String("scriptboard-version", "", "ScriptBoard stable version without v")
	scriptBoardTag := flags.String("scriptboard-tag", "", "ScriptBoard release tag")
	piVersion := flags.String("pi-version", "", "pinned Pi version")
	assetsDirectory := flags.String("assets", "dist", "runtime asset directory")
	output := flags.String("output", "", "runtime manifest output")
	unsigned := flags.Bool("unsigned", false, "omit detached signature")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	manifest, err := buildRuntimeManifest(*scriptBoardVersion, *scriptBoardTag, *piVersion, *assetsDirectory)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	outputPath := *output
	if outputPath == "" {
		outputPath = filepath.Join(*assetsDirectory, runtimeinstall.ManifestFilename)
	}
	if err := os.WriteFile(outputPath, raw, 0o644); err != nil {
		return err
	}
	if *unsigned {
		return nil
	}
	keyID := strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_KEY_ID"))
	privateRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_SIGNING_KEY")))
	if err != nil {
		return errors.New("SCRIPTBOARD_UPDATE_SIGNING_KEY is not valid base64")
	}
	if len(privateRaw) == ed25519.SeedSize {
		privateRaw = ed25519.NewKeyFromSeed(privateRaw)
	}
	publicRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_PUBLIC_KEY")))
	if err != nil || len(publicRaw) != ed25519.PublicKeySize || len(privateRaw) != ed25519.PrivateKeySize ||
		!bytes.Equal(ed25519.PrivateKey(privateRaw).Public().(ed25519.PublicKey), publicRaw) {
		return errors.New("runtime signing key does not match the embedded update public key")
	}
	signature, err := runtimeinstall.SignManifest(raw, keyID, ed25519.PrivateKey(privateRaw))
	if err != nil {
		return err
	}
	if _, err := runtimeinstall.VerifyManifestSignature(raw, signature, keyID, ed25519.PublicKey(publicRaw)); err != nil {
		return fmt.Errorf("verify generated runtime signature: %w", err)
	}
	return os.WriteFile(filepath.Join(filepath.Dir(outputPath), runtimeinstall.SignatureFilename), append(signature, '\n'), 0o644)
}

func buildRuntimeManifest(scriptBoardVersion, scriptBoardTag, piVersion, assetsDirectory string) (runtimeinstall.Manifest, error) {
	manifest := runtimeinstall.Manifest{
		Schema: runtimeinstall.ManifestSchema, Product: runtimeinstall.Product, Repository: buildinfo.Repository,
		ScriptBoardVersion: scriptBoardVersion, ScriptBoardTag: scriptBoardTag, PiVersion: piVersion,
		RPCContract: runtimeinstall.RPCContract, BrokerContract: runtimeinstall.BrokerContract,
	}
	for _, platform := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		name := runtimeAssetName(piVersion, platform[0], platform[1])
		assetPath := filepath.Join(assetsDirectory, name)
		info, err := os.Stat(assetPath)
		if err != nil || !info.Mode().IsRegular() {
			return runtimeinstall.Manifest{}, fmt.Errorf("runtime asset %q is missing", name)
		}
		hash, err := hashFile(assetPath)
		if err != nil {
			return runtimeinstall.Manifest{}, err
		}
		unpacked, _, err := updatepkg.MeasureArchive(assetPath)
		if err != nil {
			return runtimeinstall.Manifest{}, fmt.Errorf("measure runtime asset %q: %w", name, err)
		}
		manifest.Assets = append(manifest.Assets, runtimeinstall.Asset{
			OS: platform[0], Arch: platform[1], Name: name, SHA256: hash, Size: info.Size(), UnpackedSize: unpacked,
		})
	}
	if err := manifest.Validate(runtimeinstall.Compatibility{ScriptBoardVersion: scriptBoardVersion, ScriptBoardTag: scriptBoardTag}); err != nil {
		return runtimeinstall.Manifest{}, err
	}
	return manifest, nil
}

func readRuntimeLock(path string) (runtimeLock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return runtimeLock{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var lock runtimeLock
	if err := decoder.Decode(&lock); err != nil {
		return runtimeLock{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return runtimeLock{}, errors.New("Pi runtime lock contains trailing JSON")
	}
	if lock.Schema != 1 || lock.Repository != "earendil-works/pi" || lock.Tag != "v"+lock.Version ||
		!regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(lock.Commit) || len(lock.Assets) != 4 {
		return runtimeLock{}, errors.New("Pi runtime lock is invalid")
	}
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(lock.Version) {
		return runtimeLock{}, errors.New("Pi runtime lock version is invalid")
	}
	if lock.License.URL != "https://raw.githubusercontent.com/"+lock.Repository+"/"+lock.Tag+"/LICENSE" || !validLockedDigest(lock.License.SHA256) || lock.License.Size <= 0 {
		return runtimeLock{}, errors.New("Pi runtime lock license is invalid")
	}
	seen := make(map[string]struct{}, 4)
	for _, asset := range lock.Assets {
		if (asset.OS != "windows" && asset.OS != "linux") || (asset.Arch != "amd64" && asset.Arch != "arm64") || asset.Size <= 0 || asset.Size > runtimeinstall.MaxArchiveBytes || !validLockedDigest(asset.SHA256) {
			return runtimeLock{}, errors.New("Pi runtime lock asset is invalid")
		}
		key := asset.OS + "/" + asset.Arch
		expectedName := map[string]string{
			"windows/amd64": "pi-windows-x64.zip", "windows/arm64": "pi-windows-arm64.zip",
			"linux/amd64": "pi-linux-x64.tar.gz", "linux/arm64": "pi-linux-arm64.tar.gz",
		}[key]
		if asset.Name != expectedName {
			return runtimeLock{}, errors.New("Pi runtime lock asset name is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return runtimeLock{}, errors.New("Pi runtime lock contains duplicate platforms")
		}
		seen[key] = struct{}{}
	}
	return lock, nil
}

func normalizeRuntimePermissions(root, executable string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe Pi Runtime entry %q", entry.Name())
		}
		mode := os.FileMode(0o600)
		if info.IsDir() || path == filepath.Join(root, executable) {
			mode = 0o700
		}
		return os.Chmod(path, mode)
	})
}

func (lock runtimeLock) assetFor(goos, goarch string) (runtimeLockAsset, bool) {
	for _, asset := range lock.Assets {
		if asset.OS == goos && asset.Arch == goarch {
			return asset, true
		}
	}
	return runtimeLockAsset{}, false
}

func validLockedDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func verifyLockedFile(path string, size int64, digest string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != size {
		return errors.New("file size or type does not match the lock")
	}
	actual, err := hashFile(path)
	if err != nil {
		return err
	}
	if actual != digest {
		return errors.New("file SHA-256 does not match the lock")
	}
	return nil
}

func requireRegularFile(path string, maximum int64) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return errors.New("expected a bounded regular file")
	}
	return nil
}

func verifyPiPackageVersion(root, version string) error {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil || len(raw) > 64<<10 {
		return errors.New("upstream Pi package metadata is missing")
	}
	var document struct {
		Name, Version string
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil || document.Name != "@earendil-works/pi-coding-agent" || document.Version != version {
		return errors.New("upstream Pi package metadata does not match the lock")
	}
	return nil
}

func runtimeAssetName(version, goos, goarch string) string {
	extension := "tar.gz"
	if goos == "windows" {
		extension = "zip"
	}
	return fmt.Sprintf("scriptboard-pi-runtime-%s-%s-%s.%s", version, goos, goarch, extension)
}

func packRuntimeArchive(root, output, goos string) error {
	if goos == "windows" {
		return packRuntimeZIP(root, output)
	}
	return packRuntimeTarGZ(root, output)
}

func runtimeArchiveFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		return nil
	})
	sort.Strings(files)
	return files, err
}

func packRuntimeZIP(root, output string) error {
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	entries, err := runtimeArchiveFiles(root)
	if err == nil {
		for _, relative := range entries {
			info, statErr := os.Stat(filepath.Join(root, relative))
			if statErr != nil {
				err = statErr
				break
			}
			header, headerErr := zip.FileInfoHeader(info)
			if headerErr != nil {
				err = headerErr
				break
			}
			header.Name = filepath.ToSlash(relative)
			if info.IsDir() {
				header.Name += "/"
			}
			header.Method = zip.Deflate
			destination, createErr := writer.CreateHeader(header)
			if createErr != nil {
				err = createErr
				break
			}
			if info.Mode().IsRegular() {
				source, openErr := os.Open(filepath.Join(root, relative))
				if openErr != nil {
					err = openErr
					break
				}
				_, copyErr := io.Copy(destination, source)
				_ = source.Close()
				if copyErr != nil {
					err = copyErr
					break
				}
			}
		}
	}
	closeArchiveErr := writer.Close()
	closeFileErr := file.Close()
	if err != nil {
		return err
	}
	if closeArchiveErr != nil {
		return closeArchiveErr
	}
	return closeFileErr
}

func packRuntimeTarGZ(root, output string) error {
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	entries, err := runtimeArchiveFiles(root)
	if err == nil {
		for _, relative := range entries {
			info, statErr := os.Stat(filepath.Join(root, relative))
			if statErr != nil {
				err = statErr
				break
			}
			header, headerErr := tar.FileInfoHeader(info, "")
			if headerErr != nil {
				err = headerErr
				break
			}
			header.Name = filepath.ToSlash(relative)
			if info.IsDir() {
				header.Name += "/"
			}
			if writeErr := writer.WriteHeader(header); writeErr != nil {
				err = writeErr
				break
			}
			if info.Mode().IsRegular() {
				source, openErr := os.Open(filepath.Join(root, relative))
				if openErr != nil {
					err = openErr
					break
				}
				_, copyErr := io.Copy(writer, source)
				_ = source.Close()
				if copyErr != nil {
					err = copyErr
					break
				}
			}
		}
	}
	closeTarErr := writer.Close()
	closeGzipErr := compressed.Close()
	closeFileErr := file.Close()
	if err != nil {
		return err
	}
	if closeTarErr != nil {
		return closeTarErr
	}
	if closeGzipErr != nil {
		return closeGzipErr
	}
	return closeFileErr
}
