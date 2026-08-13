package privilegebroker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strings"
)

func stateRootID(stateRoot string) (string, error) {
	absolute, err := filepath.Abs(stateRoot)
	if err != nil {
		return "", fmt.Errorf("resolve privileged Broker State Root: %w", err)
	}
	identity := filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:8]), nil
}

type TransportOptions struct {
	StateRoot              string
	Endpoint               string
	AllowedIdentity        string
	DevelopmentCurrentUser bool
}

type Transport struct {
	Listener   net.Listener
	Endpoint   string
	VerifyPeer func(net.Conn) error
	cleanup    func()
}

func (transport *Transport) Close() error {
	if transport == nil {
		return nil
	}
	err := transport.Listener.Close()
	if transport.cleanup != nil {
		transport.cleanup()
	}
	return err
}
