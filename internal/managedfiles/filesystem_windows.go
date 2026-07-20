//go:build windows

package managedfiles

import (
	"strings"

	"golang.org/x/sys/windows"
)

func sameFilesystem(rootPath, candidatePath string) bool {
	rootVolume, rootErr := volumeName(rootPath)
	candidateVolume, candidateErr := volumeName(candidatePath)
	return rootErr == nil && candidateErr == nil && strings.EqualFold(rootVolume, candidateVolume)
}

func volumeName(path string) (string, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	volumePath := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(pathPointer, &volumePath[0], uint32(len(volumePath))); err != nil {
		return "", err
	}
	volumePathPointer := &volumePath[0]
	volumeName := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeNameForVolumeMountPoint(volumePathPointer, &volumeName[0], uint32(len(volumeName))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(volumeName), nil
}
