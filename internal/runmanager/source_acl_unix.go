//go:build !windows

package runmanager

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

func protectOneTimeSourceForRunner(path string) error {
	group, err := user.LookupGroup("scriptboard-runner")
	if err != nil {
		// Portable installations do not provision the managed Runner identity.
		return os.Chmod(path, 0o400)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("parse Runner group ID: %w", err)
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return err
	}
	return os.Chmod(path, 0o440)
}

func protectOneTimeRunDirectory(path string) error {
	group, err := user.LookupGroup("scriptboard-runner")
	if err != nil {
		return nil
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o710); err != nil {
		return err
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return err
	}
	return os.Chmod(path, 0o750)
}
