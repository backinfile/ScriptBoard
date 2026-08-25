package bootstrap

import (
	"fmt"
	"path/filepath"
	"strings"
)

func absoluteStateRoot(value, component string) (string, error) {
	trimmed := strings.TrimSpace(value)
	absolute, err := filepath.Abs(trimmed)
	if err != nil || trimmed == "" || !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%s requires an absolute --state-root", component)
	}
	return absolute, nil
}
