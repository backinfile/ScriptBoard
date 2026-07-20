//go:build linux

package managedfiles

import (
	"os"
	"syscall"
)

func sameFilesystem(rootPath, candidatePath string) bool {
	root, rootErr := os.Stat(rootPath)
	candidate, candidateErr := os.Stat(candidatePath)
	if rootErr != nil || candidateErr != nil {
		return false
	}
	rootStat, rootOK := root.Sys().(*syscall.Stat_t)
	candidateStat, candidateOK := candidate.Sys().(*syscall.Stat_t)
	return rootOK && candidateOK && rootStat.Dev == candidateStat.Dev
}
