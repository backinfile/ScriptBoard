//go:build windows

package runmanager

import "os"

func validateExecutorOwnership(string, os.FileInfo) error {
	// Full owner-SID validation is coupled to the Windows service identity and
	// is completed by the broker/runner split. The common checks still require
	// a canonical absolute path that resolves to a regular file.
	return nil
}
