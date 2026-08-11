//go:build linux

package privilegebroker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func DefaultEndpoint(stateRoot string) (string, error) {
	id, err := stateRootID(stateRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join("/run/scriptboard", "privileged-broker-"+id+".sock"), nil
}

func Listen(options TransportOptions) (*Transport, error) {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		var err error
		endpoint, err = DefaultEndpoint(options.StateRoot)
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(endpoint) || len(endpoint) >= 100 {
		return nil, errors.New("privileged Broker Unix socket path must be a short absolute path")
	}
	allowedUID, err := allowedLinuxUID(options)
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(endpoint)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create privileged Broker socket directory: %w", err)
	}
	if info, err := os.Lstat(endpoint); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("privileged Broker endpoint exists and is not a socket")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() {
			return nil, errors.New("privileged Broker endpoint is not owned by the current service identity")
		}
		if err := os.Remove(endpoint); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.Remove(endpoint) }
	if err := os.Chown(endpoint, allowedUID, -1); err != nil {
		_ = listener.Close()
		cleanup()
		return nil, fmt.Errorf("assign privileged Broker socket owner: %w", err)
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		cleanup()
		return nil, fmt.Errorf("protect privileged Broker socket: %w", err)
	}
	verify := func(connection net.Conn) error {
		unixConnection, ok := connection.(*net.UnixConn)
		if !ok {
			return errors.New("privileged Broker peer is not a Unix socket")
		}
		raw, err := unixConnection.SyscallConn()
		if err != nil {
			return err
		}
		var credential *unix.Ucred
		var credentialErr error
		if err := raw.Control(func(fd uintptr) {
			credential, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		}); err != nil {
			return err
		}
		if credentialErr != nil {
			return credentialErr
		}
		if credential == nil || int(credential.Uid) != allowedUID {
			return errors.New("privileged Broker peer UID is not authorized")
		}
		return nil
	}
	return &Transport{Listener: listener, Endpoint: endpoint, VerifyPeer: verify, cleanup: cleanup}, nil
}

func allowedLinuxUID(options TransportOptions) (int, error) {
	if options.DevelopmentCurrentUser {
		return os.Geteuid(), nil
	}
	identity := strings.TrimSpace(options.AllowedIdentity)
	if identity == "" {
		identity = "scriptboard-web"
	}
	account, err := user.Lookup(identity)
	if err != nil {
		return 0, fmt.Errorf("look up privileged Broker Web identity %q: %w", identity, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil || uid < 0 {
		return 0, fmt.Errorf("privileged Broker Web identity %q has an invalid UID", identity)
	}
	return uid, nil
}

func Dial(endpoint string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", endpoint)
	}
}
