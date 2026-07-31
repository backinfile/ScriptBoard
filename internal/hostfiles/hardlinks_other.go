//go:build !linux && !windows

package hostfiles

import "os"

func regularFileHasMultipleLinks(string, os.FileInfo) (bool, error) {
	return false, nil
}
