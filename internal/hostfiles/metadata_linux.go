//go:build linux

package hostfiles

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func copyPlatformMetadata(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("source ownership metadata is unavailable")
	}
	if err := os.Chown(destination, int(stat.Uid), int(stat.Gid)); err != nil {
		return fmt.Errorf("copy ownership: %w", err)
	}
	attributes, err := readExtendedAttributes(source)
	if err != nil {
		return err
	}
	for name, value := range attributes {
		if err := unix.Setxattr(destination, name, value, 0); err != nil {
			return fmt.Errorf("copy extended attribute %q: %w", name, err)
		}
	}
	return nil
}

func verifyCopiedMetadata(source, destination string, expected moveManifestEntry) error {
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != expected.mode.Perm() {
		return fmt.Errorf("copied permissions do not match source")
	}
	if delta := info.ModTime().Sub(expected.modified); delta < -time.Millisecond || delta > time.Millisecond {
		return fmt.Errorf("copied modified time does not match source")
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	sourceStat, sourceOK := sourceInfo.Sys().(*syscall.Stat_t)
	destinationStat, destinationOK := info.Sys().(*syscall.Stat_t)
	if !sourceOK || !destinationOK || sourceStat.Uid != destinationStat.Uid || sourceStat.Gid != destinationStat.Gid {
		return fmt.Errorf("copied ownership does not match source")
	}
	sourceAttributes, err := readExtendedAttributes(source)
	if err != nil {
		return err
	}
	destinationAttributes, err := readExtendedAttributes(destination)
	if err != nil {
		return err
	}
	if !equalExtendedAttributes(sourceAttributes, destinationAttributes) {
		return fmt.Errorf("copied extended attributes do not match source")
	}
	return nil
}

func readExtendedAttributes(path string) (map[string][]byte, error) {
	size, err := unix.Listxattr(path, nil)
	if err != nil {
		if err == unix.ENOTSUP {
			return map[string][]byte{}, nil
		}
		return nil, fmt.Errorf("list extended attributes: %w", err)
	}
	if size == 0 {
		return map[string][]byte{}, nil
	}
	buffer := make([]byte, size)
	read, err := unix.Listxattr(path, buffer)
	if err != nil {
		return nil, fmt.Errorf("list extended attributes: %w", err)
	}
	result := make(map[string][]byte)
	for _, name := range strings.Split(strings.TrimRight(string(buffer[:read]), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		valueSize, err := unix.Getxattr(path, name, nil)
		if err != nil {
			return nil, fmt.Errorf("read extended attribute %q: %w", name, err)
		}
		value := make([]byte, valueSize)
		if valueSize > 0 {
			if _, err := unix.Getxattr(path, name, value); err != nil {
				return nil, fmt.Errorf("read extended attribute %q: %w", name, err)
			}
		}
		result[name] = value
	}
	return result, nil
}

func equalExtendedAttributes(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	names := make([]string, 0, len(left))
	for name := range left {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !bytes.Equal(left[name], right[name]) {
			return false
		}
	}
	return true
}
