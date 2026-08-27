//go:build windows

package statebackup

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func renamePrivateState(source, destination string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.Rename(source, destination)
		if err == nil || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		// Windows 扫描器可能短暂占用刚关闭的数据库；有界等待后继续原子移动，不改写恢复事务。
		time.Sleep(25 * time.Millisecond)
	}
}
