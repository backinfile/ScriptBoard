//go:build windows

package hostfiles

import (
	"os"

	"golang.org/x/sys/windows"
)

func regularFileHasMultipleLinks(path string, _ os.FileInfo) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return false, err
	}
	return information.NumberOfLinks > 1, nil
}
