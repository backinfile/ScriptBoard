//go:build !windows

package hostfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func filesystemRoot(path string) (string, error) {
	current := filepath.Clean(path)
	info, err := os.Stat(current)
	if err != nil {
		current = filepath.Dir(current)
		info, err = os.Stat(current)
		if err != nil {
			return "", err
		}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("filesystem identity is unavailable")
	}
	device := stat.Dev
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return current, nil
		}
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return current, nil
		}
		parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
		if !ok || parentStat.Dev != device {
			return current, nil
		}
		current = parent
	}
}

func entryHidden(path string, _ os.FileInfo) bool {
	return strings.HasPrefix(filepath.Base(path), ".")
}

func restrictedEntry(_ string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
