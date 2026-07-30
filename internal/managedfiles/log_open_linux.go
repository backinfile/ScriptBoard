//go:build linux

package managedfiles

import "os"

func openLogFile(path string) (*os.File, error) {
	return os.Open(path)
}
