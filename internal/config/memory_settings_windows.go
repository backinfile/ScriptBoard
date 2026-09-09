//go:build windows

package config

import (
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

var replaceMemoryFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func replaceMemoryConfig(temporary, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.Rename(temporary, path)
	} else if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	replacement, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	// ReplaceFile preserves the existing configuration ACL, including service read access.
	ok, _, callErr := replaceMemoryFile.Call(uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(replacement)), 0, 0, 0, 0)
	if ok == 0 {
		return callErr
	}
	return nil
}
