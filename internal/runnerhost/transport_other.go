//go:build !windows && !linux

package runnerhost

import (
	"context"
	"errors"
	"net"
)

func DefaultEndpoint(string) (string, error) {
	return "", errors.New("isolated Runner Host is unsupported on this platform")
}
func Listen(TransportOptions) (*Transport, error) {
	return nil, errors.New("isolated Runner Host is unsupported on this platform")
}
func Dial(string) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		return nil, errors.New("isolated Runner Host is unsupported on this platform")
	}
}
