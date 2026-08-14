package pathidentity

import "path/filepath"

// Absolute returns a stable absolute spelling suitable for deriving persistent
// identities from a path. Platform-specific normalization expands aliases that
// can otherwise make one directory appear to have multiple identities.
func Absolute(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return platformCanonical(filepath.Clean(absolute)), nil
}
