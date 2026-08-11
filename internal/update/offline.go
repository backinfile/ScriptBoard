package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"scriptboard/internal/installation"
)

type VerifiedOfflinePackage struct {
	Version         string `json:"version"`
	Tag             string `json:"tag"`
	Commit          string `json:"commit"`
	KeyID           string `json:"key_id"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	Archive         string `json:"archive"`
	ArchiveSHA256   string `json:"archive_sha256"`
	ArchiveBytes    int64  `json:"archive_bytes"`
	UnpackedBytes   int64  `json:"unpacked_bytes"`
	DatabaseSchema  int    `json:"database_schema"`
	UpdaterProtocol int    `json:"updater_protocol"`
}

// VerifyOfflinePackage validates a release archive against a trusted signed
// manifest without consulting a network source or changing an installation.
func VerifyOfflinePackage(archivePath, manifestPath, signaturePath string) (VerifiedOfflinePackage, error) {
	for label, path := range map[string]string{"archive": archivePath, "manifest": manifestPath, "signature": signaturePath} {
		if !filepath.IsAbs(path) {
			return VerifiedOfflinePackage{}, fmt.Errorf("%s path must be absolute", label)
		}
	}
	manifestRaw, err := readLimitedFile(manifestPath, MaxManifestBytes)
	if err != nil {
		return VerifiedOfflinePackage{}, fmt.Errorf("read release manifest: %w", err)
	}
	signatureRaw, err := readLimitedFile(signaturePath, MaxSignatureBytes)
	if err != nil {
		return VerifiedOfflinePackage{}, fmt.Errorf("read release signature: %w", err)
	}
	manifest, signerKeyID, err := verifyTrustedManifest(manifestRaw, signatureRaw)
	if err != nil {
		return VerifiedOfflinePackage{}, err
	}
	asset, ok := manifest.AssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return VerifiedOfflinePackage{}, errors.New("signed manifest does not contain this platform")
	}
	if filepath.Base(archivePath) != asset.Name {
		return VerifiedOfflinePackage{}, fmt.Errorf("archive name %q does not match signed asset %q", filepath.Base(archivePath), asset.Name)
	}
	archiveInfo, err := os.Lstat(archivePath)
	if err != nil {
		return VerifiedOfflinePackage{}, err
	}
	if !archiveInfo.Mode().IsRegular() || archiveInfo.Size() != asset.Size {
		return VerifiedOfflinePackage{}, errors.New("archive is not a regular file with the signed size")
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return VerifiedOfflinePackage{}, err
	}
	digest := sha256.New()
	written, copyErr := io.Copy(digest, io.LimitReader(archive, asset.Size+1))
	closeErr := archive.Close()
	if copyErr != nil {
		return VerifiedOfflinePackage{}, copyErr
	}
	if closeErr != nil {
		return VerifiedOfflinePackage{}, closeErr
	}
	archiveSHA256 := hex.EncodeToString(digest.Sum(nil))
	if written != asset.Size || archiveSHA256 != asset.SHA256 {
		return VerifiedOfflinePackage{}, errors.New("archive SHA-256 does not match the signed manifest")
	}
	measured, _, err := MeasureArchive(archivePath)
	if err != nil {
		return VerifiedOfflinePackage{}, fmt.Errorf("validate archive entries: %w", err)
	}
	if measured != asset.UnpackedSize {
		return VerifiedOfflinePackage{}, errors.New("archive unpacked size does not match the signed manifest")
	}
	temporaryRoot, err := os.MkdirTemp("", "scriptboard-offline-verify-*")
	if err != nil {
		return VerifiedOfflinePackage{}, err
	}
	defer os.RemoveAll(temporaryRoot)
	extractedRoot := filepath.Join(temporaryRoot, "release")
	if err := ExtractArchive(archivePath, extractedRoot, asset.UnpackedSize); err != nil {
		return VerifiedOfflinePackage{}, fmt.Errorf("extract verified archive: %w", err)
	}
	if err := installation.ValidateReleaseSource(extractedRoot, targetBuild(Operation{Manifest: manifest})); err != nil {
		return VerifiedOfflinePackage{}, fmt.Errorf("validate release contents: %w", err)
	}
	return VerifiedOfflinePackage{
		Version: manifest.Version, Tag: manifest.Tag, Commit: manifest.Commit, KeyID: signerKeyID,
		OS: asset.OS, Arch: asset.Arch, Archive: asset.Name, ArchiveSHA256: archiveSHA256,
		ArchiveBytes: asset.Size, UnpackedBytes: asset.UnpackedSize, DatabaseSchema: manifest.DatabaseSchema,
		UpdaterProtocol: manifest.UpdaterProtocol,
	}, nil
}
