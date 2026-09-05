package hostfiles

import (
	"os"
	"time"
)

type createdFileInfo struct {
	os.FileInfo
	createdAt time.Time
}

func (info createdFileInfo) CreatedAt() time.Time { return info.createdAt }

// CreatedAt returns the filesystem birth time when available, including metadata carried over the broker.
func CreatedAt(info os.FileInfo) time.Time {
	if value, ok := info.(interface{ CreatedAt() time.Time }); ok {
		return value.CreatedAt()
	}
	return time.Time{}
}
