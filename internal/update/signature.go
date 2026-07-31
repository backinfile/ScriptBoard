package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"scriptboard/internal/buildinfo"
)

type SignatureDocument struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
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

func VerifyManifestSignature(raw, signatureRaw []byte, keyID string, publicKey ed25519.PublicKey) error {
	document, signature, err := decodeSignatureDocument(signatureRaw)
	if err != nil {
		return err
	}
	if document.KeyID != keyID {
		return errors.New("signature key is not trusted")
	}
	if len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, raw, signature) {
		return errors.New("manifest signature verification failed")
	}
	return nil
}

func decodeSignatureDocument(signatureRaw []byte) (SignatureDocument, []byte, error) {
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
	if !signatureKeyIDPattern.MatchString(document.KeyID) || document.Algorithm != SignatureAlgorithm {
		return SignatureDocument{}, nil, errors.New("signature key or algorithm is not trusted")
	}
	signature, err := base64.StdEncoding.DecodeString(document.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return SignatureDocument{}, nil, errors.New("signature is not valid base64 Ed25519 data")
	}
	return document, signature, nil
}

func VerifyTrustedManifest(raw, signatureRaw []byte) (Manifest, error) {
	document, signature, err := decodeSignatureDocument(signatureRaw)
	if err != nil {
		return Manifest{}, err
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
			return Manifest{}, errors.New("embedded update signing key is incomplete")
		}
		if _, duplicate := seen[key.id]; duplicate {
			return Manifest{}, errors.New("embedded update signing key IDs are duplicated")
		}
		seen[key.id] = struct{}{}
		decoded, decodeErr := base64.StdEncoding.DecodeString(key.encoded)
		if decodeErr != nil || len(decoded) != ed25519.PublicKeySize {
			return Manifest{}, errors.New("embedded update signing public key is invalid")
		}
		trusted[key.id] = ed25519.PublicKey(decoded)
	}
	if !configured {
		return Manifest{}, errors.New("this build does not contain an update signing public key")
	}
	publicKey, ok := trusted[document.KeyID]
	if !ok {
		return Manifest{}, errors.New("signature key is not trusted")
	}
	if !ed25519.Verify(publicKey, raw, signature) {
		return Manifest{}, errors.New("manifest signature verification failed")
	}
	return DecodeManifest(raw)
}
