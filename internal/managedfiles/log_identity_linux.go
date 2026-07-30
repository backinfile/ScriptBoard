//go:build linux

package managedfiles

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("unsupported Linux file identity")
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
