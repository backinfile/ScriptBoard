package instancelock

import (
	"fmt"
	"os"
	"path/filepath"
)

type Lock struct {
	file *os.File
}

func Acquire(stateRoot string) (*Lock, error) {
	path := filepath.Join(stateRoot, "instance.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开实例锁: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("另一个 ScriptBoard 实例正在使用该 State Root: %w", err)
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
