//go:build windows

package privilegebroker

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"scriptboard/internal/windowsidentity"
)

func DefaultEndpoint(stateRoot string) (string, error) {
	id, err := stateRootID(stateRoot)
	if err != nil {
		return "", err
	}
	return `\\.\pipe\scriptboard-privileged-broker-` + id, nil
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
	if !strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\scriptboard-privileged-broker-`) {
		return nil, errors.New("privileged Broker endpoint must use the dedicated local named-pipe prefix")
	}
	allowedSID, err := allowedWindowsSID(options)
	if err != nil {
		return nil, err
	}
	securityDescriptor := "D:P(A;;GA;;;" + allowedSID + ")(A;;GA;;;SY)"
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: securityDescriptor, MessageMode: false,
		InputBufferSize: MaxRequestBytes, OutputBufferSize: MaxResponseBytes,
	})
	if err != nil {
		return nil, err
	}
	// The protected pipe DACL is the Windows peer-credential check: only the
	// configured Web service SID and LocalSystem can establish a connection.
	return &Transport{Listener: listener, Endpoint: endpoint, VerifyPeer: func(net.Conn) error { return nil }, cleanup: func() {}}, nil
}

func allowedWindowsSID(options TransportOptions) (string, error) {
	if options.DevelopmentCurrentUser {
		token := windows.GetCurrentProcessToken()
		user, err := token.GetTokenUser()
		if err != nil {
			return "", err
		}
		return user.User.Sid.String(), nil
	}
	identity := strings.TrimSpace(options.AllowedIdentity)
	if identity == "" {
		identity = `NT SERVICE\ScriptBoard`
	}
	sid, err := windowsidentity.ResolveSID(identity)
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}

func Dial(endpoint string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		return winio.DialPipeContext(ctx, endpoint)
	}
}
