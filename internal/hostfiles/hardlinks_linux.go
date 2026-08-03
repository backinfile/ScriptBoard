//go:build linux

package hostfiles

import (
	"fmt"
	"os"
	"syscall"
)

func regularFileHasMultipleLinks(_ string, info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("regular file link metadata is unavailable")
	}
	return stat.Nlink > 1, nil
}
