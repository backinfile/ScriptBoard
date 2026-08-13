//go:build linux

package runmanager

import (
	"fmt"
	"os"
	"syscall"
)

func validateExecutorOwnership(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect executor owner: %s", path)
	}
	if stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("executor is not owned by root or the service identity: %s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("executor is group- or world-writable: %s", path)
	}
	return nil
}
