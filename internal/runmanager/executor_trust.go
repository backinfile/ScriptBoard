package runmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"unicode"
)

func validateExecutorTrust(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("executor path must be absolute: %s", path)
	}
	for _, character := range path {
		if character == 0 || unicode.IsControl(character) {
			return "", fmt.Errorf("executor path contains a control character")
		}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve executor path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect executor: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("executor is not a regular file: %s", resolved)
	}
	if err := validateExecutorOwnership(resolved, info); err != nil {
		return "", err
	}
	return resolved, nil
}

// ValidateExecutorTrust exposes the shared service-executable ownership and
// write-ACL policy to other fixed-domain process launchers.
func ValidateExecutorTrust(path string) (string, error) {
	return validateExecutorTrust(path)
}

func validateProcessArgument(value string) error {
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return fmt.Errorf("argument contains a control character")
		}
	}
	return nil
}
