//go:build !windows

package hoststatus

func committedMemory() (uint64, uint64, bool) { return 0, 0, false }
