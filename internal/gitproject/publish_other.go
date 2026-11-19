//go:build !linux && !windows

package gitproject

import "errors"

func publishDirectory(_, _ string) error {
	return errors.New("atomic Git checkout publication is unavailable on this operating system")
}
