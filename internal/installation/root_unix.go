//go:build !windows

package installation

import (
	"os"
	"path/filepath"
)

func defaultRoot() string {
	return "/opt/scriptboard"
}

func installStableFiles(sourceRoot, installRoot string) error {
	source := filepath.Join(sourceRoot, "scriptboard-updater")
	temporary := filepath.Join(installRoot, ".scriptboard-updater.tmp")
	_ = os.Remove(temporary)
	if err := copyFile(source, temporary, 0o755); err != nil {
		return err
	}
	target := filepath.Join(installRoot, "scriptboard-updater")
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(installRoot)
}

func activateVersion(installRoot, versionRoot string) error {
	relative, err := filepath.Rel(installRoot, versionRoot)
	if err != nil {
		return err
	}
	temporary := filepath.Join(installRoot, ".current.tmp")
	_ = os.Remove(temporary)
	if err := os.Symlink(relative, temporary); err != nil {
		return err
	}
	current := filepath.Join(installRoot, "current")
	if err := os.Rename(temporary, current); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(installRoot)
}
