//go:build windows

package appstatus

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func snapshotThreadCounts(ctx context.Context) (map[int32]int32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return map[int32]int32{}, nil
		}
		return nil, err
	}

	counts := make(map[int32]int32)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		counts[int32(entry.ProcessID)] = int32(entry.Threads)

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return counts, nil
			}
			return nil, err
		}
	}
}
