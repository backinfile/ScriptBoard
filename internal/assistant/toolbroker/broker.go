// Package toolbroker exposes ScriptBoard's fixed assistant tools over a
// process-bound, versioned local IPC capability. It never accepts Go method
// names, HTTP routes, or database queries from the Pi extension.
package toolbroker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion  = 1
	MaxRequestBytes  = 64 << 10
	MaxResponseBytes = 1 << 20
	StatusSuccess    = "success"
	StatusApproval   = "approval_required"
	StatusRejected   = "rejected"
	StatusForbidden  = "forbidden"
	StatusError      = "error"
)

var (
	toolCallIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,160}$`)
	fixedTools        = map[string]struct{}{
		"get_host_status": {}, "list_applications": {}, "get_application": {}, "read_source_log": {},
		"list_website_monitors": {}, "get_website_incident": {}, "list_runs": {}, "get_run": {},
		"read_run_log": {}, "list_quick_runs": {}, "list_schedules": {}, "read_managed_text": {},
		"start_quick_run": {}, "run_schedule_now": {}, "stop_run": {}, "check_website_now": {},
		"list_ui_actions": {}, "perform_ui_action": {},
	}
)

type Request struct {
	Version    int             `json:"version"`
	Capability string          `json:"capability"`
	ToolCallID string          `json:"toolCallId"`
	Tool       string          `json:"tool"`
	Parameters json.RawMessage `json:"parameters"`
	ApprovalID string          `json:"approvalId,omitempty"`
	Decision   string          `json:"decision,omitempty"`
}

type Approval struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

type Response struct {
	Status    string    `json:"status"`
	Content   any       `json:"content,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	ErrorCode string    `json:"errorCode,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	DeepLink  string    `json:"deepLink,omitempty"`
	Approval  *Approval `json:"approval,omitempty"`
}

type SessionBinding struct {
	RuntimeID, UserID, ConversationID string
	ExpiresAt                         time.Time
}

type Invocation struct {
	Binding SessionBinding
	Request Request
}

type Executor interface {
	Invoke(context.Context, Invocation) Response
}

type Broker struct {
	stateRoot string
	executor  Executor
	mu        sync.Mutex
	closed    bool
	sessions  map[*Session]struct{}
}

type Session struct {
	Endpoint, Capability string
	network              string
	listener             net.Listener
	binding              SessionBinding
	executor             Executor
	closeOnce            sync.Once
	done                 chan struct{}
	connections          sync.WaitGroup
	connectionMu         sync.Mutex
	activeConnections    map[net.Conn]struct{}
	closing              bool
	removeEndpoint       func()
	onClose              func(*Session)
}

func New(stateRoot string, executor Executor) (*Broker, error) {
	stateRoot = strings.TrimSpace(stateRoot)
	if stateRoot == "" || executor == nil {
		return nil, errors.New("Tool Broker State Root and executor are required")
	}
	absolute, err := filepath.Abs(stateRoot)
	if err != nil {
		return nil, err
	}
	return &Broker{stateRoot: absolute, executor: executor, sessions: make(map[*Session]struct{})}, nil
}

func (broker *Broker) Start(binding SessionBinding) (*Session, error) {
	if strings.TrimSpace(binding.RuntimeID) == "" || strings.TrimSpace(binding.UserID) == "" || strings.TrimSpace(binding.ConversationID) == "" || !binding.ExpiresAt.After(time.Now()) {
		return nil, errors.New("Tool Broker session binding is invalid")
	}
	capabilityBytes := make([]byte, 32)
	if _, err := rand.Read(capabilityBytes); err != nil {
		return nil, err
	}
	capability := base64.RawURLEncoding.EncodeToString(capabilityBytes)
	listener, network, endpoint, remove, err := listenEndpoint(broker.stateRoot)
	if err != nil {
		return nil, err
	}
	session := &Session{
		Endpoint: endpoint, Capability: capability, network: network, listener: listener,
		binding: binding, executor: broker.executor, done: make(chan struct{}), removeEndpoint: remove,
		activeConnections: make(map[net.Conn]struct{}),
	}
	session.onClose = broker.remove
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		_ = listener.Close()
		remove()
		return nil, errors.New("Tool Broker is closed")
	}
	broker.sessions[session] = struct{}{}
	broker.mu.Unlock()
	go session.serve()
	return session, nil
}

func (broker *Broker) remove(session *Session) {
	broker.mu.Lock()
	delete(broker.sessions, session)
	broker.mu.Unlock()
}

func (broker *Broker) Close() error {
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return nil
	}
	broker.closed = true
	sessions := make([]*Session, 0, len(broker.sessions))
	for session := range broker.sessions {
		sessions = append(sessions, session)
	}
	broker.mu.Unlock()
	var closeErr error
	for _, session := range sessions {
		if err := session.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (session *Session) serve() {
	defer close(session.done)
	for {
		connection, err := session.listener.Accept()
		if err != nil {
			return
		}
		session.connectionMu.Lock()
		if session.closing {
			session.connectionMu.Unlock()
			_ = connection.Close()
			continue
		}
		session.activeConnections[connection] = struct{}{}
		session.connections.Add(1)
		session.connectionMu.Unlock()
		go func() {
			defer func() {
				session.connectionMu.Lock()
				delete(session.activeConnections, connection)
				session.connectionMu.Unlock()
				session.connections.Done()
			}()
			defer connection.Close()
			session.handle(connection)
		}()
	}
}

func (session *Session) handle(connection net.Conn) {
	_ = connection.SetDeadline(time.Now().Add(35 * time.Second))
	request, err := decodeRequest(connection)
	if err != nil {
		writeResponse(connection, Response{Status: StatusError, ErrorCode: "broker_protocol_error", Summary: "Tool Broker request is malformed."})
		return
	}
	if request.Version != ProtocolVersion || !toolCallIDPattern.MatchString(request.ToolCallID) {
		writeResponse(connection, Response{Status: StatusError, ErrorCode: "broker_protocol_error", Summary: "Tool Broker request version or ID is invalid."})
		return
	}
	if _, registered := fixedTools[request.Tool]; !registered {
		writeResponse(connection, Response{Status: StatusForbidden, ErrorCode: "tool_forbidden", Summary: "Tool is not registered by ScriptBoard."})
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Capability), []byte(session.Capability)) != 1 || !time.Now().Before(session.binding.ExpiresAt) {
		writeResponse(connection, Response{Status: StatusForbidden, ErrorCode: "capability_invalid", Summary: "Tool Broker capability is invalid or expired."})
		return
	}
	if len(request.Parameters) == 0 || !json.Valid(request.Parameters) || len(request.Parameters) > MaxRequestBytes/2 {
		writeResponse(connection, Response{Status: StatusError, ErrorCode: "tool_parameters_invalid", Summary: "Tool parameters are invalid."})
		return
	}
	invokeContext, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	response := session.executor.Invoke(invokeContext, Invocation{Binding: session.binding, Request: request})
	cancel()
	if !validStatus(response.Status) {
		response = Response{Status: StatusError, ErrorCode: "tool_failed", Summary: "Tool executor returned an invalid status."}
	}
	writeResponse(connection, response)
}

func (session *Session) Close() error {
	var closeErr error
	session.closeOnce.Do(func() {
		session.connectionMu.Lock()
		session.closing = true
		connections := make([]net.Conn, 0, len(session.activeConnections))
		for connection := range session.activeConnections {
			connections = append(connections, connection)
		}
		session.connectionMu.Unlock()
		closeErr = session.listener.Close()
		for _, connection := range connections {
			_ = connection.Close()
		}
		<-session.done
		session.connections.Wait()
		session.removeEndpoint()
		if session.onClose != nil {
			session.onClose(session)
		}
	})
	return closeErr
}

func decodeRequest(reader io.Reader) (Request, error) {
	buffered := bufio.NewReaderSize(reader, MaxRequestBytes+1)
	record, err := buffered.ReadBytes('\n')
	if err != nil || len(record) <= 1 || len(record) > MaxRequestBytes+1 {
		return Request{}, errors.New("bounded JSONL request is invalid")
	}
	record = bytes.TrimSuffix(record, []byte{'\n'})
	record = bytes.TrimSuffix(record, []byte{'\r'})
	if err := rejectDuplicateJSONKeys(record); err != nil {
		return Request{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(record))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Request{}, errors.New("Tool Broker request has trailing JSON")
	}
	return request, nil
}

func rejectDuplicateJSONKeys(record []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(record))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return errors.New("Tool Broker request has trailing JSON")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("Tool Broker object key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("Tool Broker request contains a duplicate key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("Tool Broker object is incomplete")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("Tool Broker array is incomplete")
		}
	default:
		return errors.New("Tool Broker JSON delimiter is invalid")
	}
	return nil
}

func writeResponse(writer io.Writer, response Response) {
	payload, err := json.Marshal(response)
	if err != nil || len(payload) > MaxResponseBytes {
		payload, _ = json.Marshal(Response{Status: StatusError, ErrorCode: "tool_result_too_large", Summary: "Tool result exceeded its bound."})
	}
	payload = append(payload, '\n')
	_, _ = writer.Write(payload)
}

func validStatus(status string) bool {
	switch status {
	case StatusSuccess, StatusApproval, StatusRejected, StatusForbidden, StatusError:
		return true
	default:
		return false
	}
}

func ToolNames() []string {
	names := make([]string, 0, len(fixedTools))
	for name := range fixedTools {
		names = append(names, name)
	}
	return names
}
