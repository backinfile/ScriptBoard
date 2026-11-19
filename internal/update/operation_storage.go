package update

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func requireProtectedOperationStorage(stateRoot string) error {
	if runtime.GOOS == "windows" && operationStorageRoot(stateRoot) == filepath.Join(stateRoot, "updates") {
		return errors.New("Windows update recovery requires a binary from the managed Install Root")
	}
	return nil
}

func operationStorageRoot(stateRoot string) string {
	if runtime.GOOS == "windows" {
		// Web can modify State Root, so privileged update transactions and their
		// artifacts live beside the immutable installed versions. Derive this
		// path from the executing binary, not from Web-owned install-ref.json.
		executable, err := os.Executable()
		if err == nil {
			executable, err = filepath.EvalSymlinks(executable)
			if err == nil {
				release := filepath.Dir(executable)
				versions := filepath.Dir(release)
				if strings.EqualFold(filepath.Base(versions), "versions") && stableVersionPattern.MatchString(filepath.Base(release)) {
					return filepath.Join(filepath.Dir(versions), "updates")
				}
			}
		}
	}
	return filepath.Join(stateRoot, "updates")
}
