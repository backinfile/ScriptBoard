package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"scriptboard/internal/pathsecurity"
)

func ExtractArchive(archivePath, destination string, expectedSize int64) error {
	if expectedSize <= 0 || expectedSize > MaxUnpackedBytes {
		return errors.New("invalid expected unpacked size")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		if err == nil {
			return errors.New("archive destination already exists")
		}
		return err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	var extracted int64
	var count int
	seen := make(map[string]struct{})
	var err error
	switch {
	case strings.HasSuffix(strings.ToLower(archivePath), ".zip"):
		extracted, count, err = extractZIP(archivePath, destination, expectedSize, seen)
	case strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz"):
		extracted, count, err = extractTarGZ(archivePath, destination, expectedSize, seen)
	default:
		err = errors.New("unsupported release archive format")
	}
	if err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	if count == 0 || extracted != expectedSize {
		_ = os.RemoveAll(destination)
		return fmt.Errorf("archive unpacked size is %d bytes, expected %d", extracted, expectedSize)
	}
	return nil
}

// MeasureArchive validates archive entry names and types while calculating the
// total number of regular-file bytes represented by the archive.
func MeasureArchive(archivePath string) (int64, int, error) {
	seen := make(map[string]struct{})
	switch {
	case strings.HasSuffix(strings.ToLower(archivePath), ".zip"):
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return 0, 0, err
		}
		defer reader.Close()
		var total int64
		var entries int
		for _, entry := range reader.File {
			relative, err := safeArchivePath(entry.Name)
			if err != nil {
				return 0, 0, err
			}
			if relative == "" {
				continue
			}
			if entries >= MaxArchiveFileCount {
				return 0, 0, errors.New("archive contains too many entries")
			}
			entries++
			if _, exists := seen[relative]; exists {
				return 0, 0, fmt.Errorf("duplicate archive target %q", relative)
			}
			seen[relative] = struct{}{}
			mode := entry.Mode()
			if mode&os.ModeSymlink != 0 || mode&os.ModeType != 0 && !mode.IsDir() {
				return 0, 0, fmt.Errorf("archive entry %q is unsafe", entry.Name)
			}
			if mode.IsDir() {
				continue
			}
			if entry.UncompressedSize64 > uint64(MaxUnpackedBytes-total) {
				return 0, 0, errors.New("archive exceeds maximum unpacked size")
			}
			total += int64(entry.UncompressedSize64)
		}
		if entries == 0 {
			return 0, 0, errors.New("archive contains no entries")
		}
		return total, entries, nil
	case strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz"):
		file, err := os.Open(archivePath)
		if err != nil {
			return 0, 0, err
		}
		defer file.Close()
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return 0, 0, err
		}
		defer compressed.Close()
		reader := tar.NewReader(compressed)
		var total int64
		var entries int
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return 0, 0, err
			}
			relative, err := safeArchivePath(header.Name)
			if err != nil {
				return 0, 0, err
			}
			if relative == "" {
				continue
			}
			if entries >= MaxArchiveFileCount {
				return 0, 0, errors.New("archive contains too many entries")
			}
			entries++
			if _, exists := seen[relative]; exists {
				return 0, 0, fmt.Errorf("duplicate archive target %q", relative)
			}
			seen[relative] = struct{}{}
			switch header.Typeflag {
			case tar.TypeDir:
			case tar.TypeReg, tar.TypeRegA:
				if header.Size < 0 || header.Size > MaxUnpackedBytes-total {
					return 0, 0, errors.New("archive exceeds maximum unpacked size")
				}
				total += header.Size
			default:
				return 0, 0, fmt.Errorf("archive entry %q is unsafe", header.Name)
			}
		}
		if entries == 0 {
			return 0, 0, errors.New("archive contains no entries")
		}
		return total, entries, nil
	default:
		return 0, 0, errors.New("unsupported release archive format")
	}
}

func extractZIP(archivePath, destination string, limit int64, seen map[string]struct{}) (int64, int, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, 0, err
	}
	defer reader.Close()
	var total int64
	var entries int
	for _, entry := range reader.File {
		relative, err := safeArchivePath(entry.Name)
		if err != nil {
			return 0, 0, err
		}
		if relative == "" {
			continue
		}
		if entries >= MaxArchiveFileCount {
			return 0, 0, errors.New("archive contains too many entries")
		}
		entries++
		if _, exists := seen[relative]; exists {
			return 0, 0, fmt.Errorf("duplicate archive target %q", relative)
		}
		seen[relative] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeType != 0 && !mode.IsDir() {
			return 0, 0, fmt.Errorf("archive entry %q is not a regular file or directory", entry.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if mode.IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return 0, 0, err
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(limit-total) {
			return 0, 0, errors.New("archive exceeds declared unpacked size")
		}
		source, err := entry.Open()
		if err != nil {
			return 0, 0, err
		}
		written, copyErr := writeArchiveFile(target, source, mode.Perm(), limit-total)
		_ = source.Close()
		if copyErr != nil {
			return 0, 0, copyErr
		}
		if uint64(written) != entry.UncompressedSize64 {
			return 0, 0, fmt.Errorf("archive entry %q size changed while reading", entry.Name)
		}
		total += written
	}
	return total, entries, nil
}

func extractTarGZ(archivePath, destination string, limit int64, seen map[string]struct{}) (int64, int, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return 0, 0, err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var total int64
	var entries int
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, 0, err
		}
		relative, err := safeArchivePath(header.Name)
		if err != nil {
			return 0, 0, err
		}
		if relative == "" {
			continue
		}
		if entries >= MaxArchiveFileCount {
			return 0, 0, errors.New("archive contains too many entries")
		}
		entries++
		if _, exists := seen[relative]; exists {
			return 0, 0, fmt.Errorf("duplicate archive target %q", relative)
		}
		seen[relative] = struct{}{}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return 0, 0, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > limit-total {
				return 0, 0, errors.New("archive exceeds declared unpacked size")
			}
			written, err := writeArchiveFile(target, io.LimitReader(reader, header.Size), os.FileMode(header.Mode).Perm(), limit-total)
			if err != nil {
				return 0, 0, err
			}
			if written != header.Size {
				return 0, 0, fmt.Errorf("archive entry %q is truncated", header.Name)
			}
			total += written
		default:
			return 0, 0, fmt.Errorf("archive entry %q has unsafe type %d", header.Name, header.Typeflag)
		}
	}
	return total, entries, nil
}

func safeArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "//") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	for _, component := range strings.Split(cleaned, "/") {
		if unsafeWindowsArchiveComponent(component) {
			return "", fmt.Errorf("unsafe archive path %q", name)
		}
	}
	return cleaned, nil
}

func unsafeWindowsArchiveComponent(component string) bool {
	return pathsecurity.UnsafeWindowsComponent(component)
}

func writeArchiveFile(target string, source io.Reader, mode os.FileMode, remaining int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return 0, err
	}
	if mode&0o111 != 0 {
		mode = 0o700
	} else {
		mode = 0o600
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, remaining+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return written, copyErr
	}
	if written > remaining {
		return written, errors.New("archive exceeds declared unpacked size")
	}
	if syncErr != nil {
		return written, syncErr
	}
	return written, closeErr
}
