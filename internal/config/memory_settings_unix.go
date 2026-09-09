//go:build !windows

package config

import (
	"os"
	"syscall"
)

func replaceMemoryConfig(temporary, path string) error {
	info, err := os.Stat(path)
	if err == nil {
		stat := info.Sys().(*syscall.Stat_t)
		if err := os.Chown(temporary, int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}
		if err := os.Chmod(temporary, info.Mode().Perm()); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(temporary, path)
}
