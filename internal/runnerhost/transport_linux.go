//go:build linux

package runnerhost

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

	"golang.org/x/sys/unix"

	"scriptboard/internal/systemdactivation"
)

func DefaultEndpoint(stateRoot string) (string, error) {
	id, err := stateRootID(stateRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join("/run/scriptboard-runner", "runner-"+id+".sock"), nil
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
		return nil, errors.New("Runner Host Unix socket path must be a short absolute path")
	}
	allowedUID, err := allowedLinuxUID(options)
	if err != nil {
		return nil, err
	}
	listener, activated, err := systemdactivation.Listener("scriptboard-runner", endpoint)
	if err != nil {
		return nil, err
	}
	if !activated {
		listener, err = listenRunnerUnix(endpoint)
		if err != nil {
			return nil, err
		}
	}
	cleanup := func() {}
	if !activated {
		cleanup = func() { _ = os.Remove(endpoint) }
	}
	verify := func(connection net.Conn) error {
		unixConnection, ok := connection.(*net.UnixConn)
		if !ok {
			return errors.New("Runner Host peer is not a Unix socket")
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
			return errors.New("Runner Host peer UID is not authorized")
		}
		return nil
	}
	return &Transport{Listener: listener, Endpoint: endpoint, VerifyPeer: verify, cleanup: cleanup}, nil
}

func listenRunnerUnix(endpoint string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(endpoint), 0o750); err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(endpoint); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("Runner Host endpoint exists and is not a socket")
		}
		if err := os.Remove(endpoint); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(endpoint, 0o660); err != nil {
		_ = listener.Close()
		_ = os.Remove(endpoint)
		return nil, err
	}
	return listener, nil
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
		return 0, fmt.Errorf("look up Runner Host Web identity %q: %w", identity, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil || uid < 0 {
		return 0, errors.New("Runner Host Web identity has an invalid UID")
	}
	return uid, nil
}

func Dial(endpoint string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", endpoint)
	}
}
