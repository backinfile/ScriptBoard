//go:build linux

package systemdactivation

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const firstActivationFD = 3

// Listener adopts the single systemd socket assigned to this service. It
// rejects ambiguous descriptor sets and verifies the Unix endpoint before the
// caller begins accepting authenticated peers.
func Listener(name, endpoint string) (net.Listener, bool, error) {
	fds := strings.TrimSpace(os.Getenv("LISTEN_FDS"))
	pid := strings.TrimSpace(os.Getenv("LISTEN_PID"))
	if fds == "" && pid == "" {
		return nil, false, nil
	}
	parsedPID, pidErr := strconv.Atoi(pid)
	parsedFDs, fdsErr := strconv.Atoi(fds)
	if pidErr != nil || fdsErr != nil || parsedPID != os.Getpid() || parsedFDs != 1 {
		return nil, false, errors.New("systemd socket activation descriptor set is invalid")
	}
	if names := strings.TrimSpace(os.Getenv("LISTEN_FDNAMES")); names != "" && names != name {
		return nil, false, fmt.Errorf("systemd socket activation name %q does not match %q", names, name)
	}
	file := os.NewFile(firstActivationFD, "systemd-"+name)
	if file == nil {
		return nil, false, errors.New("systemd socket activation descriptor is unavailable")
	}
	listener, err := net.FileListener(file)
	_ = file.Close()
	if err != nil {
		return nil, false, fmt.Errorf("adopt systemd socket activation listener: %w", err)
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok || unixListener.Addr() == nil || filepath.Clean(unixListener.Addr().String()) != filepath.Clean(endpoint) {
		_ = listener.Close()
		return nil, false, errors.New("systemd socket activation endpoint does not match the configured IPC endpoint")
	}
	for _, key := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = os.Unsetenv(key)
	}
	return listener, true, nil
}
