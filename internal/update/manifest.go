package update

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"scriptboard/internal/buildinfo"
)

const (
	ManifestSchema       = 1
	MaxManifestBytes     = 256 << 10
	MaxSignatureBytes    = 16 << 10
	MaxArchiveBytes      = 256 << 20
	MaxUnpackedBytes     = 512 << 20
	MaxArchiveFileCount  = 256
	SignatureAlgorithm   = "ed25519"
	ManifestFilename     = "release-manifest.json"
	SignatureFilename    = "release-manifest.json.sig"
	ReleaseInfoFilename  = buildinfo.ReleaseInfoFilename
	DefaultCheckInterval = 6 * time.Hour
)

var stableVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Manifest struct {
	Schema                 int     `json:"schema"`
	Product                string  `json:"product"`
	Repository             string  `json:"repository"`
	Version                string  `json:"version"`
	Tag                    string  `json:"tag"`
	Commit                 string  `json:"commit"`
	PublishedAt            string  `json:"published_at"`
	DatabaseSchema         int     `json:"database_schema"`
	UpdaterProtocol        int     `json:"updater_protocol"`
	MinimumUpdaterProtocol int     `json:"minimum_updater_protocol"`
	Assets                 []Asset `json:"assets"`
}

type Asset struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Name         string `json:"name"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	UnpackedSize int64  `json:"unpacked_size"`
}

func DecodeManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("manifest size must be between 1 and %d bytes", MaxManifestBytes)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.Schema != ManifestSchema {
		return fmt.Errorf("unsupported manifest schema %d", m.Schema)
	}
	if m.Product != "scriptboard" || m.Repository != buildinfo.Repository {
		return errors.New("manifest product or repository does not match ScriptBoard")
	}
	if !stableVersionPattern.MatchString(m.Version) || m.Tag != "v"+m.Version {
		return errors.New("manifest version must be a stable vX.Y.Z release")
	}
	if len(m.Commit) != 40 {
		return errors.New("manifest commit must be a full SHA-1")
	}
	if _, err := hex.DecodeString(m.Commit); err != nil {
		return errors.New("manifest commit is not hexadecimal")
	}
	published, err := time.Parse(time.RFC3339, m.PublishedAt)
	if err != nil || published.Location() != time.UTC {
		return errors.New("manifest published_at must be UTC RFC3339")
	}
	if m.DatabaseSchema < 1 || m.UpdaterProtocol < 1 || m.MinimumUpdaterProtocol < 1 {
		return errors.New("manifest schema and updater protocol values must be positive")
	}
	if m.MinimumUpdaterProtocol > m.UpdaterProtocol {
		return errors.New("minimum updater protocol exceeds release updater protocol")
	}
	if len(m.Assets) != 4 {
		return errors.New("manifest must contain exactly four platform assets")
	}
	seen := make(map[string]struct{}, len(m.Assets))
	for _, asset := range m.Assets {
		if err := asset.Validate(m.Version); err != nil {
			return err
		}
		key := asset.OS + "/" + asset.Arch
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate asset for %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (m Manifest) AssetFor(goos, goarch string) (Asset, bool) {
	for _, asset := range m.Assets {
		if asset.OS == goos && asset.Arch == goarch {
			return asset, true
		}
	}
	return Asset{}, false
}

func (a Asset) Validate(version string) error {
	if a.OS != "windows" && a.OS != "linux" {
		return fmt.Errorf("unsupported asset operating system %q", a.OS)
	}
	if a.Arch != "amd64" && a.Arch != "arm64" {
		return fmt.Errorf("unsupported asset architecture %q", a.Arch)
	}
	extension := "tar.gz"
	if a.OS == "windows" {
		extension = "zip"
	}
	wantName := fmt.Sprintf("scriptboard-v%s-%s-%s.%s", version, a.OS, a.Arch, extension)
	if a.Name != wantName {
		return fmt.Errorf("asset name %q does not match %q", a.Name, wantName)
	}
	if len(a.SHA256) != 64 || strings.ToLower(a.SHA256) != a.SHA256 {
		return fmt.Errorf("asset %q has an invalid SHA-256", a.Name)
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil {
		return fmt.Errorf("asset %q has an invalid SHA-256", a.Name)
	}
	if a.Size <= 0 || a.Size > MaxArchiveBytes {
		return fmt.Errorf("asset %q size is outside the supported range", a.Name)
	}
	if a.UnpackedSize <= 0 || a.UnpackedSize > MaxUnpackedBytes {
		return fmt.Errorf("asset %q unpacked size is outside the supported range", a.Name)
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := consumeJSONValue(decoder); err != nil {
		return fmt.Errorf("validate manifest JSON: %w", err)
	}
	return ensureJSONEOF(decoder)
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			keys[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains trailing data")
		}
		return fmt.Errorf("read JSON trailing data: %w", err)
	}
	return nil
}
