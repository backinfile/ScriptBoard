//go:build !windows

package statebackup

import "os"

func renamePrivateState(source, destination string) error {
	return os.Rename(source, destination)
}
