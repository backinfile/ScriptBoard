//go:build !windows

package runmanager

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

func grantFlowWorkspace(path string) error {
	group, err := user.LookupGroup("scriptboard-runner")
	if err != nil {
		return nil
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	for _, p := range []string{filepath.Dir(path), path} {
		if err = os.Chown(p, -1, gid); err != nil {
			return err
		}
		if err = os.Chmod(p, os.ModeSetgid|0770); err != nil {
			return err
		}
	}
	return nil
}
