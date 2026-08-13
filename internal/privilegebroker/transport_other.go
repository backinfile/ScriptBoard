//go:build !linux && !windows

package privilegebroker

import (
	"context"
	"errors"
	"net"
)

func DefaultEndpoint(string) (string, error) {
	return "", errors.New("privileged Broker transport is supported only on Linux and Windows")
}

func Listen(TransportOptions) (*Transport, error) {
	return nil, errors.New("privileged Broker transport is supported only on Linux and Windows")
}

func Dial(string) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		return nil, errors.New("privileged Broker transport is supported only on Linux and Windows")
	}
}
