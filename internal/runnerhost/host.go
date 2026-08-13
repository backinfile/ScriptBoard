package runnerhost

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/runmanager"
)

const (
	maxHandshakeBytes = 128 << 10
	maxFrameBytes     = 64 << 10
	frameStdout       = byte(1)
	frameStderr       = byte(2)
	frameExit         = byte(3)
	controlGraceful   = byte(1)
	controlForce      = byte(2)
)

type launchEnvelope struct {
	Key     string                   `json:"key"`
	Request runmanager.LaunchRequest `json:"request"`
}

type launchResponse struct {
	OK       bool   `json:"ok"`
	Executor string `json:"executor,omitempty"`
	Error    string `json:"error,omitempty"`
}

type exitFrame struct {
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
}

type ServerOptions struct {
	Listener       net.Listener
	VerifyPeer     func(net.Conn) error
	ExecutorChains map[string][]string
	Maximum        int
}

type Server struct {
	listener    net.Listener
	verifyPeer  func(net.Conn) error
	launcher    runmanager.ProcessLauncher
	maximum     int
	mu          sync.Mutex
	active      map[string]runmanager.ManagedProcess
	done        chan struct{}
	closeOnce   sync.Once
	connections sync.WaitGroup
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Listener == nil {
		return nil, errors.New("Runner Host listener is required")
	}
	verify := options.VerifyPeer
	if verify == nil {
		verify = func(net.Conn) error { return nil }
	}
	maximum := options.Maximum
	if maximum < 1 || maximum > 32 {
		maximum = 16
	}
	return &Server{
		listener: options.Listener, verifyPeer: verify, launcher: runmanager.NewLocalProcessLauncher(options.ExecutorChains),
		maximum: maximum, active: make(map[string]runmanager.ManagedProcess), done: make(chan struct{}),
	}, nil
}

func (server *Server) Start() { go server.accept() }

func (server *Server) accept() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			select {
			case <-server.done:
				return
			default:
				continue
			}
		}
		server.connections.Add(1)
		go func() { defer server.connections.Done(); server.serve(connection) }()
	}
}

func (server *Server) serve(connection net.Conn) {
	defer connection.Close()
	if err := server.verifyPeer(connection); err != nil {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(connection, 32<<10)
	var envelope launchEnvelope
	if err := readJSONLine(reader, &envelope); err != nil || strings.TrimSpace(envelope.Key) == "" ||
		strings.TrimSpace(envelope.Key) != strings.TrimSpace(envelope.Request.RunID) {
		_ = json.NewEncoder(connection).Encode(launchResponse{Error: "invalid Runner Host request"})
		return
	}
	server.mu.Lock()
	if _, exists := server.active[envelope.Key]; exists || len(server.active) >= server.maximum {
		server.mu.Unlock()
		_ = json.NewEncoder(connection).Encode(launchResponse{Error: "Runner Host capacity is full"})
		return
	}
	server.active[envelope.Key] = nil
	server.mu.Unlock()
	process, executor, err := server.launcher.Launch(context.Background(), envelope.Request)
	if err != nil {
		server.remove(envelope.Key, nil)
		_ = json.NewEncoder(connection).Encode(launchResponse{Error: err.Error()})
		return
	}
	server.mu.Lock()
	server.active[envelope.Key] = process
	server.mu.Unlock()
	defer server.remove(envelope.Key, process)
	_ = connection.SetDeadline(time.Time{})
	if err := json.NewEncoder(connection).Encode(launchResponse{OK: true, Executor: executor}); err != nil {
		_ = process.Terminate(true)
		_ = process.Wait()
		_ = process.Close()
		return
	}
	var writeMu sync.Mutex
	write := func(kind byte, payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeFrame(connection, kind, payload)
	}
	controlDone := make(chan struct{})
	go func() {
		defer close(controlDone)
		for {
			control, err := reader.ReadByte()
			if err != nil {
				_ = process.Terminate(true)
				return
			}
			switch control {
			case controlGraceful:
				_ = process.Terminate(false)
			case controlForce:
				_ = process.Terminate(true)
			default:
				_ = process.Terminate(true)
				return
			}
		}
	}()
	var streams sync.WaitGroup
	copyStream := func(kind byte, source io.Reader) {
		defer streams.Done()
		buffer := make([]byte, 32<<10)
		for {
			count, readErr := source.Read(buffer)
			if count > 0 && write(kind, buffer[:count]) != nil {
				_ = process.Terminate(true)
				return
			}
			if readErr != nil {
				return
			}
		}
	}
	streams.Add(2)
	go copyStream(frameStdout, process.Stdout())
	go copyStream(frameStderr, process.Stderr())
	waitErr := process.Wait()
	streams.Wait()
	result := exitFrame{}
	if waitErr != nil {
		result.Error = waitErr.Error()
		result.Code = -1
		var coded interface{ ExitCode() int }
		if errors.As(waitErr, &coded) {
			result.Code = coded.ExitCode()
		}
	}
	payload, _ := json.Marshal(result)
	_ = write(frameExit, payload)
	_ = process.Close()
}

func (server *Server) remove(key string, process runmanager.ManagedProcess) {
	server.mu.Lock()
	if current, exists := server.active[key]; exists && current == process {
		delete(server.active, key)
	}
	server.mu.Unlock()
}

func (server *Server) Close(ctx context.Context) error {
	server.closeOnce.Do(func() { close(server.done); _ = server.listener.Close() })
	server.mu.Lock()
	processes := make([]runmanager.ManagedProcess, 0, len(server.active))
	for _, process := range server.active {
		if process != nil {
			processes = append(processes, process)
		}
	}
	server.mu.Unlock()
	for _, process := range processes {
		_ = process.Terminate(true)
	}
	wait := make(chan struct{})
	go func() { server.connections.Wait(); close(wait) }()
	select {
	case <-wait:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type ClientLauncher struct {
	dial func(context.Context) (net.Conn, error)
}

func NewClientLauncher(dial func(context.Context) (net.Conn, error)) *ClientLauncher {
	return &ClientLauncher{dial: dial}
}

func (launcher *ClientLauncher) RuntimeIdentity() string { return "scriptboard-runner" }

func (launcher *ClientLauncher) Launch(ctx context.Context, request runmanager.LaunchRequest) (runmanager.ManagedProcess, string, error) {
	if launcher == nil || launcher.dial == nil {
		return nil, "", errors.New("Runner Host dialer is unavailable")
	}
	connection, err := launcher.dial(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("connect isolated Runner Host: %w", err)
	}
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(connection).Encode(launchEnvelope{Key: request.RunID, Request: request}); err != nil {
		_ = connection.Close()
		return nil, "", err
	}
	reader := bufio.NewReaderSize(connection, 32<<10)
	var response launchResponse
	if err := readJSONLine(reader, &response); err != nil {
		_ = connection.Close()
		return nil, "", err
	}
	if !response.OK {
		_ = connection.Close()
		return nil, "", errors.New(response.Error)
	}
	_ = connection.SetDeadline(time.Time{})
	return newRemoteProcess(connection, reader), response.Executor, nil
}

func readJSONLine(reader *bufio.Reader, target any) error {
	line := make([]byte, 0, 1024)
	for {
		fragment, more, err := reader.ReadLine()
		if err != nil || len(line)+len(fragment) > maxHandshakeBytes {
			return errors.New("invalid Runner Host handshake")
		}
		line = append(line, fragment...)
		if !more {
			break
		}
	}
	if len(line) == 0 {
		return errors.New("invalid Runner Host handshake")
	}
	return json.Unmarshal(line, target)
}

func writeFrame(writer io.Writer, kind byte, payload []byte) error {
	if len(payload) > maxFrameBytes {
		return errors.New("Runner Host frame exceeds size limit")
	}
	header := [5]byte{kind}
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}
