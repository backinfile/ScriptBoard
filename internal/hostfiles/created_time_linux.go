//go:build linux

package hostfiles

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func fileCreatedAt(path string, _ os.FileInfo) time.Time {
	var metadata unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &metadata); err != nil || metadata.Mask&unix.STATX_BTIME == 0 {
		return time.Time{}
	}
	return time.Unix(metadata.Btime.Sec, int64(metadata.Btime.Nsec)).UTC()
}
