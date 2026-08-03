//go:build !windows

package hostfiles

import "path/filepath"

func canonicalComparisonPath(path string) string {
	return filepath.Clean(path)
}
