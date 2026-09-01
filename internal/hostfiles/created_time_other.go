//go:build !windows && !linux

package hostfiles

import (
	"os"
	"time"
)

func fileCreatedAt(_ string, _ os.FileInfo) time.Time {
	return time.Time{}
}
