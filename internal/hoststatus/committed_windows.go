//go:build windows

package hoststatus

import "github.com/shirou/gopsutil/v4/mem"

func committedMemory() (uint64, uint64) {
	value, err := mem.NewExWindows().VirtualMemory()
	if err != nil {
		return 0, 0
	}
	return value.CommitTotal, value.CommitLimit
}
