//go:build windows

package diskspace

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func Available(path string) (uint64, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	pointer, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return 0, err
	}
	var available uint64
	err = windows.GetDiskFreeSpaceEx(pointer, &available, nil, nil)
	return available, err
}
