//go:build !windows

package toolbroker

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func listenEndpoint(stateRoot string) (net.Listener, string, string, func(), error) {
	root := filepath.Join(stateRoot, "assistant", "ipc")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, "", "", func() {}, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, "", "", func() {}, err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", "", func() {}, err
	}
	endpoint := filepath.Join(root, hex.EncodeToString(random[:])+".sock")
	if len(endpoint) >= 100 {
		return nil, "", "", func() {}, fmt.Errorf("Tool Broker Unix socket path is too long")
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, "", "", func() {}, err
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(endpoint)
		return nil, "", "", func() {}, err
	}
	return listener, "unix", endpoint, func() { _ = os.Remove(endpoint) }, nil
}

func dialEndpoint(_ string, endpoint string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", endpoint, timeout)
}
