package runtimehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/assistant/pirpc"
)

const maxHandshakeBytes = 128 << 10

type launchEnvelope struct {
	Key     string                     `json:"key"`
	Request pirpc.RuntimeLaunchRequest `json:"request"`
}

type launchResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type ServerOptions struct {
	Listener   net.Listener
	VerifyPeer func(net.Conn) error
	StateRoot  string
	Maximum    int
}

type Server struct {
	listener    net.Listener
	verifyPeer  func(net.Conn) error
	launcher    pirpc.ProcessLauncher
	maximum     int
	mu          sync.Mutex
	active      map[string]pirpc.ManagedProcess
	done        chan struct{}
	closeOnce   sync.Once
	connections sync.WaitGroup
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Listener == nil || strings.TrimSpace(options.StateRoot) == "" {
		return nil, errors.New("Runtime Host listener and State Root are required")
	}
	verify := options.VerifyPeer
	if verify == nil {
		verify = func(net.Conn) error { return nil }
	}
	maximum := options.Maximum
	if maximum < 1 || maximum > 8 {
		maximum = 8
	}
	return &Server{
		listener: options.Listener, verifyPeer: verify,
		launcher: pirpc.NewLocalRuntimeLauncher(options.StateRoot), maximum: maximum,
		active: make(map[string]pirpc.ManagedProcess), done: make(chan struct{}),
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
		go func() {
			defer server.connections.Done()
			server.serveConnection(connection)
		}()
	}
}

func (server *Server) serveConnection(connection net.Conn) {
	defer connection.Close()
	if err := server.verifyPeer(connection); err != nil {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(connection, 32<<10)
	var envelope launchEnvelope
	if err := readJSONLine(reader, &envelope); err != nil {
		_ = json.NewEncoder(connection).Encode(launchResponse{Error: "invalid Runtime Host request"})
		return
	}
	if strings.TrimSpace(envelope.Key) == "" || len(envelope.Key) > 128 || strings.TrimSpace(envelope.Key) != strings.TrimSpace(envelope.Request.ConversationID) {
		_ = json.NewEncoder(connection).Encode(launchResponse{Error: "invalid Runtime Host process key"})
		return
	}
	server.mu.Lock()
	if _, exists := server.active[envelope.Key]; exists {
		server.mu.Unlock()
		_ = json.NewEncoder(connection).Encode(launchResponse{Error: pirpc.ErrAlreadyRunning.Error()})
		return
	}
	if len(server.active) >= server.maximum {
		server.mu.Unlock()
		_ = json.NewEncoder(connection).Encode(launchResponse{Error: pirpc.ErrCapacity.Error()})
		return
	}
	server.active[envelope.Key] = nil
	server.mu.Unlock()
	process, err := server.launcher.LaunchRuntime(context.Background(), envelope.Request)
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
	if err := json.NewEncoder(connection).Encode(launchResponse{OK: true}); err != nil {
		_ = process.Terminate(true)
		_ = process.Wait()
		_ = process.Close()
		return
	}
	clientClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(process.Stdin(), reader)
		close(clientClosed)
	}()
	go func() {
		_, _ = io.Copy(connection, process.Stdout())
	}()
	go func() { _, _ = io.Copy(io.Discard, process.Stderr()) }()
	processDone := make(chan struct{})
	go func() { _ = process.Wait(); close(processDone) }()
	select {
	case <-clientClosed:
	case <-processDone:
	case <-server.done:
	}
	select {
	case <-processDone:
	default:
		_ = process.Terminate(true)
		select {
		case <-processDone:
		case <-time.After(2 * time.Second):
		}
	}
	_ = process.Close()
}

func (server *Server) remove(key string, process pirpc.ManagedProcess) {
	server.mu.Lock()
	if current, exists := server.active[key]; exists && current == process {
		delete(server.active, key)
	}
	server.mu.Unlock()
}

func (server *Server) Close(ctx context.Context) error {
	server.closeOnce.Do(func() {
		close(server.done)
		_ = server.listener.Close()
	})
	server.mu.Lock()
	processes := make([]pirpc.ManagedProcess, 0, len(server.active))
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

// ClientLauncher is the production adapter. It accepts only domain Runtime
// requests, so the IPC boundary cannot be used as a generic command launcher.
type ClientLauncher struct {
	dial func(context.Context) (net.Conn, error)
}

func NewClientLauncher(dial func(context.Context) (net.Conn, error)) *ClientLauncher {
	return &ClientLauncher{dial: dial}
}

func (launcher *ClientLauncher) LaunchSpec(context.Context, pirpc.LaunchSpec) (pirpc.ManagedProcess, error) {
	return nil, errors.New("isolated Runtime Host rejects executable launch specifications")
}

func (launcher *ClientLauncher) LaunchRuntime(ctx context.Context, request pirpc.RuntimeLaunchRequest) (pirpc.ManagedProcess, error) {
	if launcher == nil || launcher.dial == nil {
		return nil, errors.New("Runtime Host dialer is unavailable")
	}
	connection, err := launcher.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect isolated Runtime Host: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	}
	if err := json.NewEncoder(connection).Encode(launchEnvelope{Key: request.ConversationID, Request: request}); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("send Runtime Host request: %w", err)
	}
	reader := bufio.NewReaderSize(connection, 32<<10)
	var response launchResponse
	if err := readJSONLine(reader, &response); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("read Runtime Host response: %w", err)
	}
	if !response.OK {
		_ = connection.Close()
		switch response.Error {
		case pirpc.ErrAlreadyRunning.Error():
			return nil, pirpc.ErrAlreadyRunning
		case pirpc.ErrCapacity.Error():
			return nil, pirpc.ErrCapacity
		default:
			return nil, errors.New(response.Error)
		}
	}
	_ = connection.SetDeadline(time.Time{})
	return newRemoteProcess(connection, reader), nil
}

func readJSONLine(reader *bufio.Reader, target any) error {
	line := make([]byte, 0, 1024)
	for {
		fragment, more, err := reader.ReadLine()
		if err != nil {
			return err
		}
		if len(line)+len(fragment) > maxHandshakeBytes {
			return errors.New("Runtime Host handshake exceeds size limit")
		}
		line = append(line, fragment...)
		if !more {
			break
		}
	}
	if len(line) == 0 {
		return errors.New("empty Runtime Host handshake")
	}
	return json.Unmarshal(line, target)
}
