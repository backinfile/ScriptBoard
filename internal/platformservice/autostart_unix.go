//go:build !windows

package platformservice

func InstallTrayAutostart(_, _ string) error { return nil }
func RemoveTrayAutostart() error             { return nil }
