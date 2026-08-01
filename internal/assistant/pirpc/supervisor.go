package pirpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrAlreadyRunning = errors.New("a Pi process is already running for this conversation")
	ErrCapacity       = errors.New("the Pi process capacity is full")
)

type Supervisor struct {
	mu        sync.Mutex
	maximum   int
	accepting bool
	active    map[string]*Session
}

type Session struct {
	key        string
	command    *exec.Cmd
	client     *Client
	controller assistantProcessController
	stderr     *boundedTail
	done       chan struct{}
	stopOnce   sync.Once
	waitMu     sync.Mutex
	waitErr    error
}

func NewSupervisor(maximum int) *Supervisor {
	if maximum < 1 {
		maximum = 1
	}
	return &Supervisor{maximum: maximum, accepting: true, active: make(map[string]*Session)}
}

func (supervisor *Supervisor) Start(key string, spec LaunchSpec) (*Session, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("assistant process key is required")
	}
	if !filepath.IsAbs(spec.Executable) || strings.TrimSpace(spec.Workspace) == "" {
		return nil, fmt.Errorf("assistant launch spec is incomplete")
	}
	if err := ensurePrivateDirectory(spec.Workspace); err != nil {
		return nil, err
	}

	supervisor.mu.Lock()
	if !supervisor.accepting {
		supervisor.mu.Unlock()
		return nil, ErrClientClosed
	}
	if _, exists := supervisor.active[key]; exists {
		supervisor.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	if len(supervisor.active) >= supervisor.maximum {
		supervisor.mu.Unlock()
		return nil, ErrCapacity
	}
	// Reserve the key while pipes and the child process are created so two
	// concurrent requests cannot both pass the capacity check.
	supervisor.active[key] = nil
	supervisor.mu.Unlock()
	reserved := true
	defer func() {
		if !reserved {
			return
		}
		supervisor.mu.Lock()
		if supervisor.active[key] == nil {
			delete(supervisor.active, key)
		}
		supervisor.mu.Unlock()
	}()

	command := exec.Command(spec.Executable, spec.Args...)
	command.Dir = spec.Workspace
	command.Env = append([]string(nil), spec.Env...)
	configureAssistantProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Pi stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open Pi stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("open Pi stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start private Pi runtime: %w", err)
	}
	controller, err := attachAssistantProcess(command.Process)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	session := &Session{
		key: key, command: command, client: NewClient(stdout, stdin, ClientOptions{}), controller: controller,
		stderr: newBoundedTail(64 << 10), done: make(chan struct{}),
	}
	supervisor.mu.Lock()
	if !supervisor.accepting {
		supervisor.mu.Unlock()
		_ = controller.Terminate(true)
		_ = command.Wait()
		_ = controller.Close()
		return nil, ErrClientClosed
	}
	supervisor.active[key] = session
	supervisor.mu.Unlock()
	reserved = false
	go supervisor.supervise(session, stderr)
	return session, nil
}

func ensurePrivateDirectory(path string) error {
	if err := mkdirPrivate(path); err != nil {
		return fmt.Errorf("prepare Pi workspace: %w", err)
	}
	return nil
}

func (supervisor *Supervisor) supervise(session *Session, stderr io.ReadCloser) {
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(session.stderr, stderr)
		_ = stderr.Close()
		close(stderrDone)
	}()
	waitErr := session.command.Wait()
	_ = session.client.Close()
	_ = session.controller.Close()
	<-stderrDone
	session.waitMu.Lock()
	session.waitErr = waitErr
	session.waitMu.Unlock()
	supervisor.mu.Lock()
	if supervisor.active[session.key] == session {
		delete(supervisor.active, session.key)
	}
	supervisor.mu.Unlock()
	close(session.done)
}

func (supervisor *Supervisor) Active() int {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	active := 0
	for _, session := range supervisor.active {
		if session != nil {
			active++
		}
	}
	return active
}

func (supervisor *Supervisor) SetMaximum(maximum int) error {
	if maximum < 1 || maximum > 8 {
		return fmt.Errorf("Pi process maximum must be between 1 and 8")
	}
	supervisor.mu.Lock()
	supervisor.maximum = maximum
	supervisor.mu.Unlock()
	return nil
}

func (supervisor *Supervisor) Session(key string) (*Session, bool) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	session, exists := supervisor.active[key]
	return session, exists && session != nil
}

func (supervisor *Supervisor) Stop(ctx context.Context, key string) error {
	session, exists := supervisor.Session(key)
	if !exists {
		return nil
	}
	session.stopOnce.Do(func() { go stopAssistantSession(session) })
	select {
	case <-session.done:
		return nil
	case <-ctx.Done():
		_ = session.controller.Terminate(true)
		select {
		case <-session.done:
			return nil
		case <-time.After(time.Second):
			return ctx.Err()
		}
	}
}

func stopAssistantSession(session *Session) {
	abortContext, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	_, _ = session.client.Abort(abortContext, "scriptboard-abort")
	cancel()
	select {
	case <-session.done:
		return
	case <-time.After(750 * time.Millisecond):
	}
	_ = session.controller.Terminate(false)
	select {
	case <-session.done:
		return
	case <-time.After(1250 * time.Millisecond):
	}
	_ = session.controller.Terminate(true)
}

func (supervisor *Supervisor) Close(ctx context.Context) error {
	supervisor.mu.Lock()
	supervisor.accepting = false
	keys := make([]string, 0, len(supervisor.active))
	for key, session := range supervisor.active {
		if session != nil {
			keys = append(keys, key)
		}
	}
	supervisor.mu.Unlock()
	var closeErr error
	for _, key := range keys {
		if err := supervisor.Stop(ctx, key); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (session *Session) Client() *Client       { return session.client }
func (session *Session) Done() <-chan struct{} { return session.done }
func (session *Session) StderrTail() string    { return session.stderr.String() }

func (session *Session) Wait(ctx context.Context) error {
	select {
	case <-session.done:
		session.waitMu.Lock()
		defer session.waitMu.Unlock()
		return session.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

type boundedTail struct {
	mu      sync.Mutex
	maximum int
	data    []byte
}

func newBoundedTail(maximum int) *boundedTail {
	return &boundedTail{maximum: maximum, data: make([]byte, 0, maximum)}
}

func (tail *boundedTail) Write(data []byte) (int, error) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	written := len(data)
	if len(data) >= tail.maximum {
		tail.data = append(tail.data[:0], data[len(data)-tail.maximum:]...)
		return written, nil
	}
	if overflow := len(tail.data) + len(data) - tail.maximum; overflow > 0 {
		copy(tail.data, tail.data[overflow:])
		tail.data = tail.data[:len(tail.data)-overflow]
	}
	tail.data = append(tail.data, data...)
	return written, nil
}

func (tail *boundedTail) String() string {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	return string(append([]byte(nil), tail.data...))
}
