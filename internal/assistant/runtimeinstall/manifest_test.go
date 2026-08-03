package runtimeinstall

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"testing"
)

func TestRuntimeManifestUsesIndependentSignatureDomain(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := validManifestForTest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := SignManifest(raw, "release-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := VerifyManifestSignature(raw, signature, "release-key", publicKey)
	if err != nil {
		t.Fatalf("verify runtime manifest: %v", err)
	}
	if decoded.PiVersion != manifest.PiVersion {
		t.Fatalf("Pi version = %q, want %q", decoded.PiVersion, manifest.PiVersion)
	}
	if ed25519.Verify(publicKey, raw, mustDecodeSignature(t, signature)) {
		t.Fatal("runtime signature unexpectedly verifies without the runtime product domain")
	}
}

func TestRuntimeManifestRejectsWrongScriptBoardRelease(t *testing.T) {
	manifest := validManifestForTest()
	manifest.ScriptBoardVersion = "9.9.9"
	if err := manifest.Validate(Compatibility{ScriptBoardVersion: "1.2.3", ScriptBoardTag: "v1.2.3"}); err == nil {
		t.Fatal("wrong ScriptBoard release was accepted")
	}
}

func validManifestForTest() Manifest {
	manifest := Manifest{
		Schema: 1, Product: Product, Repository: "backinfile/ScriptBoard",
		ScriptBoardVersion: "1.2.3", ScriptBoardTag: "v1.2.3", PiVersion: "0.83.0",
		RPCContract: 1, BrokerContract: 1,
	}
	for _, platform := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		extension := "tar.gz"
		if platform[0] == "windows" {
			extension = "zip"
		}
		manifest.Assets = append(manifest.Assets, Asset{
			OS: platform[0], Arch: platform[1],
			Name:   "scriptboard-pi-runtime-0.83.0-" + platform[0] + "-" + platform[1] + "." + extension,
			SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Size:   100, UnpackedSize: 200,
		})
	}
	if _, ok := manifest.AssetFor(runtime.GOOS, runtime.GOARCH); !ok {
		panic("test platform is unsupported")
	}
	return manifest
}

func mustDecodeSignature(t *testing.T, raw []byte) []byte {
	t.Helper()
	var document SignatureDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(document.Signature)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
