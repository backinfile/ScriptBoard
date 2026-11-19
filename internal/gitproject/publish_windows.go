//go:build windows

package gitproject

import "golang.org/x/sys/windows"

func publishDirectory(from, to string) error {
	oldPath, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	newPath, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFile(oldPath, newPath)
}
