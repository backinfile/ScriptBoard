//go:build !windows

package privatepath

import "os"

func ProtectDirectory(path string) error {
	return os.Chmod(path, 0o700)
}
