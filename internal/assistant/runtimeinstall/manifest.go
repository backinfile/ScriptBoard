// Package runtimeinstall owns the signed Pi runtime supply chain and the
// private, versioned installation beneath ScriptBoard's State Root.
package runtimeinstall

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"scriptboard/internal/buildinfo"
)

const (
	ManifestSchema    = 1
	Product           = "scriptboard-assistant-runtime"
	RPCContract       = 1
	BrokerContract    = 1
	ManifestFilename  = "ASSISTANT-RUNTIME.json"
	SignatureFilename = "ASSISTANT-RUNTIME.json.sig"
	MaxManifestBytes  = 256 << 10
	MaxSignatureBytes = 16 << 10
	MaxArchiveBytes   = 256 << 20
	MaxUnpackedBytes  = 512 << 20
)

var (
	stableVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	keyIDPattern         = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	runtimeSignDomain    = []byte("ScriptBoard/assistant-runtime-manifest/v1\x00")
)

type Compatibility struct {
	ScriptBoardVersion string
	ScriptBoardTag     string
}

type Manifest struct {
	Schema             int     `json:"schema"`
	Product            string  `json:"product"`
	Repository         string  `json:"repository"`
	ScriptBoardVersion string  `json:"scriptboard_version"`
	ScriptBoardTag     string  `json:"scriptboard_tag"`
	PiVersion          string  `json:"pi_version"`
	RPCContract        int     `json:"rpc_contract"`
	BrokerContract     int     `json:"broker_contract"`
	Assets             []Asset `json:"assets"`
}

type Asset struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Name         string `json:"name"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	UnpackedSize int64  `json:"unpacked_size"`
}

type SignatureDocument struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

func DecodeManifest(raw []byte, compatibility Compatibility) (Manifest, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return Manifest{}, errors.New("runtime manifest size is invalid")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode runtime manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(compatibility); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate(compatibility Compatibility) error {
	if manifest.Schema != ManifestSchema || manifest.Product != Product || manifest.Repository != buildinfo.Repository {
		return errors.New("runtime manifest product, repository, or schema is incompatible")
	}
	if !stableVersionPattern.MatchString(manifest.ScriptBoardVersion) || manifest.ScriptBoardTag != "v"+manifest.ScriptBoardVersion {
		return errors.New("runtime manifest has an invalid ScriptBoard release")
	}
	if compatibility.ScriptBoardVersion != "" && (manifest.ScriptBoardVersion != compatibility.ScriptBoardVersion || manifest.ScriptBoardTag != compatibility.ScriptBoardTag) {
		return errors.New("runtime manifest is for a different ScriptBoard release")
	}
	if !stableVersionPattern.MatchString(manifest.PiVersion) {
		return errors.New("runtime manifest has an invalid Pi version")
	}
	if manifest.RPCContract != RPCContract || manifest.BrokerContract != BrokerContract {
		return errors.New("runtime manifest protocol is incompatible")
	}
	if len(manifest.Assets) != 4 {
		return errors.New("runtime manifest must contain four platform assets")
	}
	seen := make(map[string]struct{}, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		if err := asset.Validate(manifest.PiVersion); err != nil {
			return err
		}
		key := asset.OS + "/" + asset.Arch
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate runtime asset for %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (manifest Manifest) AssetFor(goos, goarch string) (Asset, bool) {
	for _, asset := range manifest.Assets {
		if asset.OS == goos && asset.Arch == goarch {
			return asset, true
		}
	}
	return Asset{}, false
}

func (asset Asset) Validate(piVersion string) error {
	if asset.OS != "windows" && asset.OS != "linux" {
		return fmt.Errorf("unsupported runtime operating system %q", asset.OS)
	}
	if asset.Arch != "amd64" && asset.Arch != "arm64" {
		return fmt.Errorf("unsupported runtime architecture %q", asset.Arch)
	}
	extension := "tar.gz"
	if asset.OS == "windows" {
		extension = "zip"
	}
	want := fmt.Sprintf("scriptboard-pi-runtime-%s-%s-%s.%s", piVersion, asset.OS, asset.Arch, extension)
	if asset.Name != want {
		return fmt.Errorf("runtime asset name %q does not match %q", asset.Name, want)
	}
	if len(asset.SHA256) != 64 || asset.SHA256 != strings.ToLower(asset.SHA256) {
		return fmt.Errorf("runtime asset %q has an invalid SHA-256", asset.Name)
	}
	if _, err := hex.DecodeString(asset.SHA256); err != nil {
		return fmt.Errorf("runtime asset %q has an invalid SHA-256", asset.Name)
	}
	if asset.Size <= 0 || asset.Size > MaxArchiveBytes || asset.UnpackedSize <= 0 || asset.UnpackedSize > MaxUnpackedBytes {
		return fmt.Errorf("runtime asset %q size is outside the supported range", asset.Name)
	}
	return nil
}

func SignManifest(raw []byte, keyID string, privateKey ed25519.PrivateKey) ([]byte, error) {
	if !keyIDPattern.MatchString(keyID) || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("runtime signing key is invalid")
	}
	signature := ed25519.Sign(privateKey, domainPayload(raw))
	return json.Marshal(SignatureDocument{KeyID: keyID, Algorithm: "ed25519", Signature: base64.StdEncoding.EncodeToString(signature)})
}

func VerifyManifestSignature(raw, signatureRaw []byte, keyID string, publicKey ed25519.PublicKey) (Manifest, error) {
	document, signature, err := decodeSignature(signatureRaw)
	if err != nil {
		return Manifest{}, err
	}
	if document.KeyID != keyID || len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, domainPayload(raw), signature) {
		return Manifest{}, errors.New("runtime manifest signature verification failed")
	}
	return DecodeManifest(raw, Compatibility{})
}

func VerifyTrustedManifest(raw, signatureRaw []byte, compatibility Compatibility) (Manifest, error) {
	document, signature, err := decodeSignature(signatureRaw)
	if err != nil {
		return Manifest{}, err
	}
	keys := []struct{ id, encoded string }{
		{buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64},
		{buildinfo.UpdateNextKeyID, buildinfo.UpdateNextKeyBase64},
	}
	for _, key := range keys {
		if key.id != document.KeyID || key.encoded == "" {
			continue
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(key.encoded)
		if decodeErr == nil && len(decoded) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(decoded), domainPayload(raw), signature) {
			return DecodeManifest(raw, compatibility)
		}
	}
	return Manifest{}, errors.New("runtime manifest signature key is not trusted")
}

func domainPayload(raw []byte) []byte {
	payload := make([]byte, 0, len(runtimeSignDomain)+len(raw))
	payload = append(payload, runtimeSignDomain...)
	return append(payload, raw...)
}

func decodeSignature(raw []byte) (SignatureDocument, []byte, error) {
	if len(raw) == 0 || len(raw) > MaxSignatureBytes {
		return SignatureDocument{}, nil, errors.New("runtime signature document size is invalid")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return SignatureDocument{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document SignatureDocument
	if err := decoder.Decode(&document); err != nil {
		return SignatureDocument{}, nil, fmt.Errorf("decode runtime signature: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return SignatureDocument{}, nil, err
	}
	if !keyIDPattern.MatchString(document.KeyID) || document.Algorithm != "ed25519" {
		return SignatureDocument{}, nil, errors.New("runtime signature key or algorithm is invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(document.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return SignatureDocument{}, nil, errors.New("runtime signature is not valid Ed25519 data")
	}
	return document, signature, nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := consumeJSONValue(decoder); err != nil {
		return fmt.Errorf("validate runtime JSON: %w", err)
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
				return errors.New("JSON object key is not a string")
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			keys[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("runtime JSON contains trailing data")
}
