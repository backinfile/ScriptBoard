//go:build windows

package runtimehost

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
	return `\\.\pipe\scriptboard-ai-runtime-` + id, nil
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
	if !strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\scriptboard-ai-runtime-`) {
		return nil, errors.New("Runtime Host endpoint must use the dedicated local named-pipe prefix")
	}
	allowedSID, err := allowedWindowsSID(options)
	if err != nil {
		return nil, err
	}
	serverSID, err := runtimeWindowsSID(options)
	if err != nil {
		return nil, err
	}
	// Accept creates a subsequent pipe instance, which must remain writable by
	// the restricted AI service after the first instance applies this DACL.
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + allowedSID + ")(A;;GA;;;" + serverSID + ")(A;;GA;;;SY)",
		MessageMode:        false, InputBufferSize: maxHandshakeBytes, OutputBufferSize: maxHandshakeBytes,
	})
	if err != nil {
		return nil, err
	}
	return &Transport{Listener: listener, Endpoint: endpoint, VerifyPeer: func(net.Conn) error { return nil }, cleanup: func() {}}, nil
}

func runtimeWindowsSID(options TransportOptions) (string, error) {
	if options.DevelopmentCurrentUser {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return "", err
		}
		return user.User.Sid.String(), nil
	}
	sid, err := windowsidentity.ResolveSID(`NT SERVICE\ScriptBoardAI`)
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}

func allowedWindowsSID(options TransportOptions) (string, error) {
	if options.DevelopmentCurrentUser {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
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
	return func(ctx context.Context) (net.Conn, error) { return winio.DialPipeContext(ctx, endpoint) }
}
