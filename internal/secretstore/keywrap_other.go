//go:build !windows

package secretstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var rawKeyMagic = []byte("SBRAWKEY1")

func wrapKey(raw []byte) ([]byte, error) {
	return append(append([]byte(nil), rawKeyMagic...), raw...), nil
}

func unwrapKey(body []byte) ([]byte, error) {
	if !bytes.HasPrefix(body, rawKeyMagic) {
		return nil, errors.New("credential key is not a ScriptBoard Unix key")
	}
	return append([]byte(nil), body[len(rawKeyMagic):]...), nil
}

func validateKeyPath(path string) error {
	for _, candidate := range []struct {
		path      string
		directory bool
	}{{filepath.Dir(path), true}, {path, false}} {
		info, err := os.Lstat(candidate.path)
		if err != nil {
			return err
		}
		if candidate.directory && !info.IsDir() || !candidate.directory && !info.Mode().IsRegular() {
			return errors.New("credential master path has an unsafe file type")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("credential master path %s grants group or other permissions", candidate.path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() {
			return fmt.Errorf("credential master path %s is not owned by the service identity", candidate.path)
		}
	}
	return nil
}
