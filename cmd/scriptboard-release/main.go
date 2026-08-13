package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scriptboard/internal/buildinfo"
	"scriptboard/internal/secretredaction"
	updatepkg "scriptboard/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, secretredaction.String("error: "+err.Error()))
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: scriptboard-release keygen|info|manifest|runtime-package|runtime-manifest")
	}
	switch arguments[0] {
	case "keygen":
		return generateKey()
	case "info":
		return generateReleaseInfo(arguments[1:])
	case "manifest":
		return generateManifest(arguments[1:])
	case "runtime-package":
		return generateRuntimePackage(arguments[1:])
	case "runtime-manifest":
		return generateRuntimeManifest(arguments[1:])
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func generateReleaseInfo(arguments []string) error {
	flags := flag.NewFlagSet("info", flag.ContinueOnError)
	version := flags.String("version", "development", "version without v")
	tag := flags.String("tag", "", "Git tag")
	commit := flags.String("commit", "unknown", "full commit SHA")
	builtAt := flags.String("built-at", "", "UTC RFC3339 build time")
	release := flags.Bool("release", false, "mark as a formal release")
	output := flags.String("output", "", "output RELEASE.json")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	info := buildinfo.Info{
		Version: *version, Tag: *tag, Commit: *commit, BuiltAt: *builtAt, ReleaseBuild: *release,
		DatabaseSchemaVersion:  buildinfo.DatabaseSchemaVersion,
		UpdaterProtocolVersion: buildinfo.UpdaterProtocolVersion, Repository: buildinfo.Repository,
	}
	if *release && !info.ValidRelease() {
		return errors.New("formal release info is invalid")
	}
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if *output == "" {
		_, err = os.Stdout.Write(raw)
		return err
	}
	return os.WriteFile(*output, raw, 0o644)
}

func generateKey() error {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	output := struct {
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
	}{
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		PrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func generateManifest(arguments []string) error {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	version := flags.String("version", "", "stable version without v")
	tag := flags.String("tag", "", "Git tag")
	commit := flags.String("commit", "", "full commit SHA")
	publishedAt := flags.String("published-at", "", "UTC RFC3339 time")
	assetsDirectory := flags.String("assets", "dist", "asset directory")
	output := flags.String("output", "", "manifest output path")
	unsigned := flags.Bool("unsigned", false, "omit detached signature")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	manifest, err := buildManifest(*version, *tag, *commit, *publishedAt, *assetsDirectory)
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
		outputPath = filepath.Join(*assetsDirectory, updatepkg.ManifestFilename)
	}
	if err := os.WriteFile(outputPath, raw, 0o644); err != nil {
		return err
	}
	if *unsigned {
		return nil
	}
	keyID := strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_KEY_ID"))
	privateKeyRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_SIGNING_KEY")))
	if err != nil {
		return errors.New("SCRIPTBOARD_UPDATE_SIGNING_KEY is not valid base64")
	}
	if len(privateKeyRaw) == ed25519.SeedSize {
		privateKeyRaw = ed25519.NewKeyFromSeed(privateKeyRaw)
	}
	publicKeyRaw, publicErr := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_PUBLIC_KEY")))
	if publicErr != nil || len(publicKeyRaw) != ed25519.PublicKeySize {
		return errors.New("SCRIPTBOARD_UPDATE_PUBLIC_KEY is not valid base64 Ed25519 data")
	}
	if len(privateKeyRaw) != ed25519.PrivateKeySize ||
		!bytes.Equal(ed25519.PrivateKey(privateKeyRaw).Public().(ed25519.PublicKey), publicKeyRaw) {
		return errors.New("release signing private key does not match the embedded public key")
	}
	signingKeys := []updatepkg.ManifestSigningKey{{KeyID: keyID, PrivateKey: ed25519.PrivateKey(privateKeyRaw)}}
	verificationKeys := []struct {
		id  string
		key ed25519.PublicKey
	}{{id: keyID, key: ed25519.PublicKey(publicKeyRaw)}}
	nextPrivateEncoded := strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_NEXT_SIGNING_KEY"))
	if nextPrivateEncoded != "" {
		nextKeyID := strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_NEXT_KEY_ID"))
		nextPublicRaw, publicErr := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY")))
		nextPrivateRaw, privateErr := base64.StdEncoding.DecodeString(nextPrivateEncoded)
		if privateErr != nil {
			return errors.New("SCRIPTBOARD_UPDATE_NEXT_SIGNING_KEY is not valid base64")
		}
		if len(nextPrivateRaw) == ed25519.SeedSize {
			nextPrivateRaw = ed25519.NewKeyFromSeed(nextPrivateRaw)
		}
		if nextKeyID == "" || publicErr != nil || len(nextPublicRaw) != ed25519.PublicKeySize || len(nextPrivateRaw) != ed25519.PrivateKeySize ||
			!bytes.Equal(ed25519.PrivateKey(nextPrivateRaw).Public().(ed25519.PublicKey), nextPublicRaw) {
			return errors.New("next release signing private key does not match the configured next public key")
		}
		signingKeys = append(signingKeys, updatepkg.ManifestSigningKey{KeyID: nextKeyID, PrivateKey: ed25519.PrivateKey(nextPrivateRaw)})
		verificationKeys = append(verificationKeys, struct {
			id  string
			key ed25519.PublicKey
		}{id: nextKeyID, key: ed25519.PublicKey(nextPublicRaw)})
	}
	var signature []byte
	if len(signingKeys) == 1 {
		signature, err = updatepkg.SignManifest(raw, signingKeys[0].KeyID, signingKeys[0].PrivateKey)
	} else {
		signature, err = updatepkg.SignManifestWithKeys(raw, signingKeys)
	}
	if err != nil {
		return err
	}
	for _, verificationKey := range verificationKeys {
		if err := updatepkg.VerifyManifestSignature(raw, signature, verificationKey.id, verificationKey.key); err != nil {
			return fmt.Errorf("verify generated release signature %s: %w", verificationKey.id, err)
		}
	}
	return os.WriteFile(filepath.Join(filepath.Dir(outputPath), updatepkg.SignatureFilename), append(signature, '\n'), 0o644)
}

func buildManifest(version, tag, commit, publishedAt, assetsDirectory string) (updatepkg.Manifest, error) {
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(version) {
		return updatepkg.Manifest{}, errors.New("version must be stable X.Y.Z")
	}
	if tag != "v"+version {
		return updatepkg.Manifest{}, errors.New("tag does not match version")
	}
	if len(commit) != 40 {
		return updatepkg.Manifest{}, errors.New("commit must be a full SHA")
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return updatepkg.Manifest{}, errors.New("commit must be hexadecimal")
	}
	published, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil || published.Location() != time.UTC {
		return updatepkg.Manifest{}, errors.New("published-at must be UTC RFC3339")
	}
	pattern := regexp.MustCompile(`^scriptboard-v` + regexp.QuoteMeta(version) + `-(?:(windows)-(amd64|arm64)-setup\.(exe)|(linux)-(amd64|arm64)\.(run))$`)
	entries, err := os.ReadDir(assetsDirectory)
	if err != nil {
		return updatepkg.Manifest{}, err
	}
	var assets []updatepkg.Asset
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := pattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		assetOS, assetArch := match[1], match[2]
		if assetOS == "" {
			assetOS, assetArch = match[4], match[5]
		}
		path := filepath.Join(assetsDirectory, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return updatepkg.Manifest{}, err
		}
		hash, err := hashFile(path)
		if err != nil {
			return updatepkg.Manifest{}, err
		}
		unpackedSize, _, err := updatepkg.MeasureArchive(path)
		if err != nil {
			return updatepkg.Manifest{}, fmt.Errorf("measure %s: %w", entry.Name(), err)
		}
		assets = append(assets, updatepkg.Asset{
			OS: assetOS, Arch: assetArch, Name: entry.Name(),
			SHA256: hash, Size: info.Size(), UnpackedSize: unpackedSize,
		})
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].OS == assets[j].OS {
			return assets[i].Arch < assets[j].Arch
		}
		return assets[i].OS < assets[j].OS
	})
	if len(assets) != 4 {
		return updatepkg.Manifest{}, fmt.Errorf("found %d release assets, expected four", len(assets))
	}
	manifest := updatepkg.Manifest{
		Schema: updatepkg.ManifestSchema, Product: "scriptboard", Repository: buildinfo.Repository,
		Version: version, Tag: tag, Commit: commit, PublishedAt: publishedAt,
		DatabaseSchema:  buildinfo.DatabaseSchemaVersion,
		UpdaterProtocol: buildinfo.UpdaterProtocolVersion, MinimumUpdaterProtocol: 2,
		Assets: assets,
	}
	if err := manifest.Validate(); err != nil {
		return updatepkg.Manifest{}, err
	}
	return manifest, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
