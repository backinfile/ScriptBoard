//go:build linux

package hostfiles

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

func (m *Manager) platformPermissions(target string, info os.FileInfo) (Permissions, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Permissions{}, fmt.Errorf("read Linux file ownership")
	}
	return Permissions{
		Platform: "linux", Path: target, Directory: info.IsDir(), Mode: uint32(info.Mode().Perm()),
		Owner: linuxPrincipal(strconv.FormatUint(uint64(stat.Uid), 10), true),
		Group: linuxPrincipal(strconv.FormatUint(uint64(stat.Gid), 10), false),
	}, nil
}

func linuxPrincipal(id string, owner bool) Principal {
	name := id
	kind := "group"
	if owner {
		kind = "user"
		if value, err := user.LookupId(id); err == nil {
			name = value.Username
		}
	} else if value, err := user.LookupGroupId(id); err == nil {
		name = value.Name
	}
	return Principal{Name: name, ID: id, Type: kind}
}

func (m *Manager) setPlatformPermissions(target string, _ os.FileInfo, change PermissionChange) error {
	if change.Owner != "" || change.ReplaceChildOwners || change.Principal != "" || change.AccessMask != nil || change.RemoveRule || change.ApplyRuleToChildren || change.InheritanceEnabled != nil {
		return fmt.Errorf("Windows ownership and ACL fields are not valid on Linux")
	}
	if change.Mode == nil {
		return fmt.Errorf("Linux permission mode is required")
	}
	if *change.Mode > 0o777 {
		return fmt.Errorf("Linux permission mode must be between 0000 and 0777")
	}
	paths := []string{target}
	if change.Recursive {
		paths = paths[:0]
		err := filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if len(paths) >= maxRecursivePermissionEntries {
				return fmt.Errorf("recursive permission change exceeds %d entries", maxRecursivePermissionEntries)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("recursive permission change encountered a restricted entry")
			}
			if _, _, err := m.resolveEntry(path); err != nil {
				return err
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			return err
		}
	}
	previous := make([]fs.FileMode, len(paths))
	for index, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		previous[index] = info.Mode().Perm()
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := os.Chmod(paths[index], fs.FileMode(*change.Mode)); err != nil {
			for rollback := index + 1; rollback < len(paths); rollback++ {
				_ = os.Chmod(paths[rollback], previous[rollback])
			}
			return err
		}
	}
	return nil
}
