//go:build windows

package managedfiles

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func fileIdentity(file *os.File) (string, error) {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%08x:%08x%08x",
		information.VolumeSerialNumber,
		information.FileIndexHigh,
		information.FileIndexLow,
	), nil
}
