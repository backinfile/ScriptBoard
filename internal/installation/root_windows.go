//go:build windows

package installation

import (
	"os"
	"path/filepath"
)

func defaultRoot() string {
	root := os.Getenv("ProgramFiles")
	if root == "" {
		root = `C:\Program Files`
	}
	return filepath.Join(root, "ScriptBoard")
}

func installStableFiles(sourceRoot, installRoot string) error {
	source := filepath.Join(sourceRoot, "scriptboard-tray-launcher.exe")
	temporary := filepath.Join(installRoot, ".scriptboard-tray-launcher.exe.tmp")
	_ = os.Remove(temporary)
	if err := copyFile(source, temporary, 0o755); err != nil {
		return err
	}
	target := filepath.Join(installRoot, "scriptboard-tray-launcher.exe")
	_ = os.Remove(target)
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	return syncDirectory(installRoot)
}

func activateVersion(_, _ string) error {
	return nil
}
