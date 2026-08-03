//go:build linux

package hostfiles

import "os"

func openLogFile(path string) (*os.File, error) {
	return os.Open(path)
}
