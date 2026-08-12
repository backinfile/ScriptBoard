package runnerhost

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"
)

type TransportOptions struct {
	StateRoot              string
	Endpoint               string
	AllowedIdentity        string
	DevelopmentCurrentUser bool
}

type Transport struct {
	net.Listener
	Endpoint   string
	VerifyPeer func(net.Conn) error
	cleanup    func()
}

func (transport *Transport) Close() error {
	err := transport.Listener.Close()
	if transport.cleanup != nil {
		transport.cleanup()
	}
	return err
}

func stateRootID(stateRoot string) (string, error) {
	absolute, err := filepath.Abs(stateRoot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return hex.EncodeToString(digest[:8]), nil
}
