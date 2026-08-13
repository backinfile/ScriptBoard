//go:build !windows

package secretstore

import (
	"bytes"
	"errors"
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
