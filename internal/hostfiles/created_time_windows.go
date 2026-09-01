//go:build windows

package hostfiles

import (
	"os"
	"syscall"
	"time"
)

func fileCreatedAt(_ string, info os.FileInfo) time.Time {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, attributes.CreationTime.Nanoseconds()).UTC()
}
