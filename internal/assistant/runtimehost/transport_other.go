//go:build !windows && !linux

package runtimehost

import (
	"context"
	"errors"
	"net"
)

func DefaultEndpoint(string) (string, error) {
	return "", errors.New("isolated Runtime Host is unsupported on this platform")
}
func Listen(TransportOptions) (*Transport, error) {
	return nil, errors.New("isolated Runtime Host is unsupported on this platform")
}
func Dial(string) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		return nil, errors.New("isolated Runtime Host is unsupported on this platform")
	}
}
