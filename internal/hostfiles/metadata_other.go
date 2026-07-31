//go:build !windows && !linux

package hostfiles

import (
	"fmt"
	"os"
	"time"
)

func copyPlatformMetadata(_, _ string) error { return nil }

func verifyCopiedMetadata(_ string, destination string, expected moveManifestEntry) error {
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != expected.mode.Perm() {
		return fmt.Errorf("copied permissions do not match source")
	}
	if delta := info.ModTime().Sub(expected.modified); delta < -2*time.Second || delta > 2*time.Second {
		return fmt.Errorf("copied modified time does not match source")
	}
	return nil
}
