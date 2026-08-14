//go:build !windows

package pathidentity

func platformCanonical(path string) string { return path }
