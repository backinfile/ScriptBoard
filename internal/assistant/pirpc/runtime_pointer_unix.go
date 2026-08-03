//go:build !windows

package pirpc

import "os"

func replaceRuntimePointer(source, destination string) error {
	return os.Rename(source, destination)
}
