//go:build !linux && !windows

package hostfiles

import (
	"fmt"
	"os"
)

func (m *Manager) platformPermissions(_ string, _ os.FileInfo) (Permissions, error) {
	return Permissions{}, fmt.Errorf("file permission management is unsupported on this platform")
}

func (m *Manager) setPlatformPermissions(_ string, _ os.FileInfo, _ PermissionChange) error {
	return fmt.Errorf("file permission management is unsupported on this platform")
}
