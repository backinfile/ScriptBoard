//go:build !windows

package hoststatus

func committedMemory() (uint64, uint64) { return 0, 0 }
