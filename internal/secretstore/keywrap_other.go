//go:build !windows

package secretstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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
	return validateKeyPathOwner(path, os.Geteuid())
}

func validateKeyPathForIdentity(path, identity string) error {
	account, err := user.Lookup(identity)
	if err != nil {
		return fmt.Errorf("resolve credential key service identity %q: %w", identity, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse credential key service identity UID: %w", err)
	}
	return validateKeyPathOwner(path, uid)
}

func validateKeyPathOwner(path string, expectedUID int) error {
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
		if !ok || int(stat.Uid) != expectedUID {
			return fmt.Errorf("credential master path %s is not owned by the service identity", candidate.path)
		}
	}
	return nil
}

func readWrappedKeyForIdentity(path, identity string) ([]byte, error) {
	if err := validateKeyPathForIdentity(path, identity); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
