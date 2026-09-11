//go:build !windows

package memorysettings

import (
	"os"
	"os/user"
	"strconv"
)

func shareWithRunner(root string) error {
	if account, err := user.Current(); err == nil && os.Geteuid() != 0 && account.Username != "scriptboard-web" {
		return os.Chmod(Path(root), 0600)
	}
	group, err := user.LookupGroup("scriptboard-runner")
	if err != nil {
		return os.Chmod(Path(root), 0600)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	// Only the managed Runner group gains traversal and read access to the non-secret memory policy.
	if err = os.Chown(root, -1, gid); err != nil {
		return err
	}
	if err = os.Chmod(root, 0710); err != nil {
		return err
	}
	if err = os.Chown(Path(root), -1, gid); err != nil {
		return err
	}
	return os.Chmod(Path(root), 0640)
}
