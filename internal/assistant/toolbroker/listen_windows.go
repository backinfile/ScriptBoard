//go:build windows

package toolbroker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenEndpoint(_ string) (net.Listener, string, string, func(), error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", "", func() {}, err
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, "", "", func() {}, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, "", "", func() {}, err
	}
	sid := user.User.Sid.String()
	endpoint := `\\.\pipe\scriptboard-assistant-` + hex.EncodeToString(random[:])
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")", MessageMode: false, InputBufferSize: MaxRequestBytes, OutputBufferSize: MaxResponseBytes,
	})
	return listener, "pipe", endpoint, func() {}, err
}

func dialEndpoint(_ string, endpoint string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return winio.DialPipeContext(ctx, endpoint)
}
