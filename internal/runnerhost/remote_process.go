package runnerhost

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

type remoteProcess struct {
	connection net.Conn
	stdoutR    *io.PipeReader
	stdoutW    *io.PipeWriter
	stderrR    *io.PipeReader
	stderrW    *io.PipeWriter
	done       chan struct{}
	writeMu    sync.Mutex
	finishOnce sync.Once
	waitErr    error
}

func newRemoteProcess(connection net.Conn, reader *bufio.Reader) *remoteProcess {
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	process := &remoteProcess{connection: connection, stdoutR: stdoutR, stdoutW: stdoutW, stderrR: stderrR, stderrW: stderrW, done: make(chan struct{})}
	go process.readFrames(reader)
	return process
}

func (process *remoteProcess) readFrames(reader io.Reader) {
	for {
		header := [5]byte{}
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			process.finish(err)
			return
		}
		length := binary.BigEndian.Uint32(header[1:])
		if length > maxFrameBytes {
			process.finish(errors.New("Runner Host frame exceeds size limit"))
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			process.finish(err)
			return
		}
		switch header[0] {
		case frameStdout:
			if _, err := process.stdoutW.Write(payload); err != nil {
				process.finish(err)
				return
			}
		case frameStderr:
			if _, err := process.stderrW.Write(payload); err != nil {
				process.finish(err)
				return
			}
		case frameExit:
			var result exitFrame
			if json.Unmarshal(payload, &result) != nil {
				process.finish(errors.New("invalid Runner Host exit frame"))
			} else if result.Code != 0 || result.Error != "" {
				process.finish(runExitError{code: result.Code, message: result.Error})
			} else {
				process.finish(nil)
			}
			return
		default:
			process.finish(errors.New("invalid Runner Host frame type"))
			return
		}
	}
}

func (process *remoteProcess) Stdout() io.ReadCloser { return process.stdoutR }
func (process *remoteProcess) Stderr() io.ReadCloser { return process.stderrR }
func (process *remoteProcess) Wait() error           { <-process.done; return process.waitErr }

func (process *remoteProcess) Terminate(force bool) error {
	control := controlGraceful
	if force {
		control = controlForce
	}
	process.writeMu.Lock()
	defer process.writeMu.Unlock()
	_, err := process.connection.Write([]byte{control})
	return err
}

func (process *remoteProcess) Close() error {
	err := process.connection.Close()
	process.finish(nil)
	return err
}

func (process *remoteProcess) finish(err error) {
	process.finishOnce.Do(func() {
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			err = errors.New("Runner Host connection closed before exit status")
		}
		process.waitErr = err
		_ = process.stdoutW.Close()
		_ = process.stderrW.Close()
		close(process.done)
	})
}

type runExitError struct {
	code    int
	message string
}

func (err runExitError) Error() string {
	if err.message != "" {
		return err.message
	}
	return fmt.Sprintf("Runner process exited with code %d", err.code)
}
func (err runExitError) ExitCode() int { return err.code }
