package update

import (
	"fmt"
	"os"
	"path/filepath"
)

type operationLock struct {
	file *os.File
}

func acquireOperationLock(stateRoot string) (*operationLock, error) {
	root := filepath.Join(stateRoot, "updates")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, "operation.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockOperationFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another update helper or recovery command is active: %w", err)
	}
	return &operationLock{file: file}, nil
}

func (lock *operationLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unlockOperationFile(lock.file)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
