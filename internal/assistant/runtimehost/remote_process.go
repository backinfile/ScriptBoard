package runtimehost

import (
	"bufio"
	"io"
	"net"
	"strings"
	"sync"
)

type remoteProcess struct {
	connection net.Conn
	reader     *bufio.Reader
	done       chan struct{}
	closeOnce  sync.Once
	waitMu     sync.Mutex
	waitErr    error
}

func newRemoteProcess(connection net.Conn, reader *bufio.Reader) *remoteProcess {
	return &remoteProcess{connection: connection, reader: reader, done: make(chan struct{})}
}

func (process *remoteProcess) Read(data []byte) (int, error) {
	count, err := process.reader.Read(data)
	if err != nil {
		process.finish(err)
	}
	return count, err
}

func (process *remoteProcess) Write(data []byte) (int, error) {
	return process.connection.Write(data)
}

func (process *remoteProcess) Stdin() io.WriteCloser { return process }
func (process *remoteProcess) Stdout() io.ReadCloser { return process }
func (process *remoteProcess) Stderr() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }

func (process *remoteProcess) Wait() error {
	<-process.done
	process.waitMu.Lock()
	defer process.waitMu.Unlock()
	if process.waitErr == io.EOF || process.waitErr == net.ErrClosed {
		return nil
	}
	return process.waitErr
}

func (process *remoteProcess) Terminate(bool) error { return process.Close() }

func (process *remoteProcess) Close() error {
	err := process.connection.Close()
	process.finish(nil)
	return err
}

func (process *remoteProcess) finish(err error) {
	process.closeOnce.Do(func() {
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	})
}
