package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"scriptboard/internal/buildinfo"
)

type SignatureDocument struct {
	KeyID      string                   `json:"key_id,omitempty"`
	Algorithm  string                   `json:"algorithm,omitempty"`
	Signature  string                   `json:"signature,omitempty"`
	Signatures []ManifestSignatureEntry `json:"signatures,omitempty"`
}

type ManifestSignatureEntry struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

type ManifestSigningKey struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

type decodedManifestSignature struct {
	entry     ManifestSignatureEntry
	signature []byte
}

var signatureKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func SignManifest(raw []byte, keyID string, privateKey ed25519.PrivateKey) ([]byte, error) {
	if !signatureKeyIDPattern.MatchString(keyID) {
		return nil, errors.New("signature key ID is invalid")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key")
	}
	document := SignatureDocument{
		KeyID: keyID, Algorithm: SignatureAlgorithm,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, raw)),
	}
	return json.Marshal(document)
}

func SignManifestWithKeys(raw []byte, keys []ManifestSigningKey) ([]byte, error) {
	if len(keys) < 1 || len(keys) > 2 {
		return nil, errors.New("manifest requires one or two signing keys")
	}
	document := SignatureDocument{Signatures: make([]ManifestSignatureEntry, 0, len(keys))}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !signatureKeyIDPattern.MatchString(key.KeyID) || len(key.PrivateKey) != ed25519.PrivateKeySize {
			return nil, errors.New("manifest signing key is invalid")
		}
		if _, duplicate := seen[key.KeyID]; duplicate {
			return nil, errors.New("manifest signing key IDs are duplicated")
		}
		seen[key.KeyID] = struct{}{}
		document.Signatures = append(document.Signatures, ManifestSignatureEntry{
			KeyID: key.KeyID, Algorithm: SignatureAlgorithm,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key.PrivateKey, raw)),
		})
	}
	return json.Marshal(document)
}

func VerifyManifestSignature(raw, signatureRaw []byte, keyID string, publicKey ed25519.PublicKey) error {
	_, signatures, err := decodeSignatureDocument(signatureRaw)
	if err != nil {
		return err
	}
	for _, signature := range signatures {
		if signature.entry.KeyID == keyID && len(publicKey) == ed25519.PublicKeySize && ed25519.Verify(publicKey, raw, signature.signature) {
			return nil
		}
	}
	return errors.New("manifest signature verification failed")
}

func decodeSignatureDocument(signatureRaw []byte) (SignatureDocument, []decodedManifestSignature, error) {
	if len(signatureRaw) == 0 || len(signatureRaw) > MaxSignatureBytes {
		return SignatureDocument{}, nil, errors.New("signature document size is invalid")
	}
	if err := rejectDuplicateJSONKeys(signatureRaw); err != nil {
		return SignatureDocument{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(signatureRaw))
	decoder.DisallowUnknownFields()
	var document SignatureDocument
	if err := decoder.Decode(&document); err != nil {
		return SignatureDocument{}, nil, fmt.Errorf("decode signature document: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return SignatureDocument{}, nil, err
	}
	legacy := document.KeyID != "" || document.Algorithm != "" || document.Signature != ""
	if legacy == (len(document.Signatures) != 0) {
		return SignatureDocument{}, nil, errors.New("signature document must use exactly one supported format")
	}
	entries := document.Signatures
	if legacy {
		entries = []ManifestSignatureEntry{{KeyID: document.KeyID, Algorithm: document.Algorithm, Signature: document.Signature}}
	}
	if len(entries) < 1 || len(entries) > 2 {
		return SignatureDocument{}, nil, errors.New("signature document must contain one or two signatures")
	}
	seen := make(map[string]struct{}, len(entries))
	decoded := make([]decodedManifestSignature, 0, len(entries))
	for _, entry := range entries {
		if !signatureKeyIDPattern.MatchString(entry.KeyID) || entry.Algorithm != SignatureAlgorithm {
			return SignatureDocument{}, nil, errors.New("signature key or algorithm is not trusted")
		}
		if _, duplicate := seen[entry.KeyID]; duplicate {
			return SignatureDocument{}, nil, errors.New("signature key IDs are duplicated")
		}
		seen[entry.KeyID] = struct{}{}
		signature, err := base64.StdEncoding.DecodeString(entry.Signature)
		if err != nil || len(signature) != ed25519.SignatureSize {
			return SignatureDocument{}, nil, errors.New("signature is not valid base64 Ed25519 data")
		}
		decoded = append(decoded, decodedManifestSignature{entry: entry, signature: signature})
	}
	return document, decoded, nil
}

func VerifyTrustedManifest(raw, signatureRaw []byte) (Manifest, error) {
	manifest, _, err := verifyTrustedManifest(raw, signatureRaw)
	return manifest, err
}

func verifyTrustedManifest(raw, signatureRaw []byte) (Manifest, string, error) {
	_, signatures, err := decodeSignatureDocument(signatureRaw)
	if err != nil {
		return Manifest{}, "", err
	}
	keys := []struct {
		id      string
		encoded string
	}{
		{buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64},
		{buildinfo.UpdateNextKeyID, buildinfo.UpdateNextKeyBase64},
	}
	configured := false
	seen := make(map[string]struct{}, len(keys))
	trusted := make(map[string]ed25519.PublicKey, len(keys))
	for _, key := range keys {
		if key.id == "" && key.encoded == "" {
			continue
		}
		configured = true
		if !signatureKeyIDPattern.MatchString(key.id) || key.encoded == "" {
			return Manifest{}, "", errors.New("embedded update signing key is incomplete")
		}
		if _, duplicate := seen[key.id]; duplicate {
			return Manifest{}, "", errors.New("embedded update signing key IDs are duplicated")
		}
		seen[key.id] = struct{}{}
		decoded, decodeErr := base64.StdEncoding.DecodeString(key.encoded)
		if decodeErr != nil || len(decoded) != ed25519.PublicKeySize {
			return Manifest{}, "", errors.New("embedded update signing public key is invalid")
		}
		trusted[key.id] = ed25519.PublicKey(decoded)
	}
	if !configured {
		return Manifest{}, "", errors.New("this build does not contain an update signing public key")
	}
	revoked, err := revokedUpdateKeys()
	if err != nil {
		return Manifest{}, "", err
	}
	sawTrusted, sawRevoked := false, false
	for _, signature := range signatures {
		if _, isRevoked := revoked[signature.entry.KeyID]; isRevoked {
			sawRevoked = true
			continue
		}
		publicKey, ok := trusted[signature.entry.KeyID]
		if !ok {
			continue
		}
		sawTrusted = true
		if ed25519.Verify(publicKey, raw, signature.signature) {
			manifest, err := DecodeManifest(raw)
			return manifest, signature.entry.KeyID, err
		}
	}
	if sawTrusted {
		return Manifest{}, "", errors.New("manifest signature verification failed")
	}
	if sawRevoked {
		return Manifest{}, "", errors.New("manifest signature key is revoked")
	}
	return Manifest{}, "", errors.New("signature key is not trusted")
}

func revokedUpdateKeys() (map[string]struct{}, error) {
	revoked := make(map[string]struct{})
	for _, value := range strings.Split(buildinfo.UpdateRevokedKeyIDs, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !signatureKeyIDPattern.MatchString(value) {
			return nil, errors.New("embedded update signing key revocation list is invalid")
		}
		if _, duplicate := revoked[value]; duplicate {
			return nil, errors.New("embedded update signing key revocation list contains duplicates")
		}
		revoked[value] = struct{}{}
	}
	return revoked, nil
}
