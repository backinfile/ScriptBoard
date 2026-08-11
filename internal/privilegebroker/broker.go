// Package privilegebroker exposes a small, versioned local RPC boundary for
// fixed host mutations. Authorization and execution are deliberately separate:
// the first request creates a short-lived, single-use capability bound to the
// exact action, resource, revision and canonical parameter digest.
package privilegebroker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion     = 1
	MaxRequestBytes     = 128 << 10
	MaxResponseBytes    = 16 << 10
	capabilityLifetime  = 30 * time.Second
	maxCapabilities     = 1024
	operationAuthorize  = "authorize"
	operationExecute    = "execute"
	statusOK            = "ok"
	statusError         = "error"
	defaultCallDeadline = 35 * time.Second
)

type Action string

const (
	ActionInstallComponent      Action = "install_component"
	ActionFail2BanUnban         Action = "fail2ban_unban"
	ActionUFWEnable             Action = "ufw_enable"
	ActionUFWApply              Action = "ufw_apply"
	ActionWindowsFirewallAdd    Action = "windows_firewall_add"
	ActionWindowsFirewallSet    Action = "windows_firewall_set_enabled"
	ActionWindowsFirewallDelete Action = "windows_firewall_delete"
)

var (
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,160}$`)
	actions          = map[Action]struct{}{
		ActionInstallComponent: {}, ActionFail2BanUnban: {}, ActionUFWEnable: {}, ActionUFWApply: {},
		ActionWindowsFirewallAdd: {}, ActionWindowsFirewallSet: {}, ActionWindowsFirewallDelete: {},
	}
)

type Authorization struct {
	SessionToken string
	RequestID    string
}

type authorizationContextKey struct{}

func WithAuthorization(ctx context.Context, authorization Authorization) context.Context {
	return context.WithValue(ctx, authorizationContextKey{}, authorization)
}

func AuthorizationFromContext(ctx context.Context) (Authorization, bool) {
	authorization, ok := ctx.Value(authorizationContextKey{}).(Authorization)
	return authorization, ok
}

type Actor struct {
	UserID, Username, Role  string
	AuthenticationAssurance int
}

type AuthorizationRequest struct {
	SessionToken, RequestID string
	Action                  Action
	Resource, Revision      string
	ParametersSHA256        string
}

type Authorizer interface {
	Authorize(context.Context, AuthorizationRequest) (Actor, error)
}

type Executor interface {
	Execute(context.Context, ExecutionRequest) error
}

type ExecutionRequest struct {
	Action             Action
	Resource, Revision string
	Parameters         json.RawMessage
	RequestID          string
	Actor              Actor
}

type AuditRecord struct {
	OccurredAt time.Time
	RequestID  string
	Actor      Actor
	Action     Action
	Resource   string
	Revision   string
	Result     string
}

type Auditor interface {
	Record(context.Context, AuditRecord) error
}

type ServerOptions struct {
	Listener   net.Listener
	VerifyPeer func(net.Conn) error
	Authorizer Authorizer
	Executor   Executor
	Auditor    Auditor
	Now        func() time.Time
}

type Server struct {
	listener   net.Listener
	verifyPeer func(net.Conn) error
	authorizer Authorizer
	executor   Executor
	auditor    Auditor
	now        func() time.Time

	mu           sync.Mutex
	capabilities map[string]capabilityBinding
	closed       bool
	closeOnce    sync.Once
	done         chan struct{}
	connections  sync.WaitGroup
}

type capabilityBinding struct {
	Action           Action
	Resource         string
	Revision         string
	ParametersSHA256 string
	RequestID        string
	Actor            Actor
	ExpiresAt        time.Time
}

type wireRequest struct {
	Version          int             `json:"version"`
	Operation        string          `json:"operation"`
	RequestID        string          `json:"request_id"`
	SessionToken     string          `json:"session_token,omitempty"`
	Capability       string          `json:"capability,omitempty"`
	Action           Action          `json:"action"`
	Resource         string          `json:"resource"`
	Revision         string          `json:"revision"`
	ParametersSHA256 string          `json:"parameters_sha256"`
	Parameters       json.RawMessage `json:"parameters,omitempty"`
}

type wireResponse struct {
	Status     string `json:"status"`
	Capability string `json:"capability,omitempty"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	Message    string `json:"message,omitempty"`
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Listener == nil || options.VerifyPeer == nil || options.Authorizer == nil || options.Executor == nil {
		return nil, errors.New("privileged Broker listener, peer verifier, authorizer and executor are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		listener: options.Listener, verifyPeer: options.VerifyPeer, authorizer: options.Authorizer,
		executor: options.Executor, auditor: options.Auditor, now: now,
		capabilities: make(map[string]capabilityBinding), done: make(chan struct{}),
	}, nil
}

func (server *Server) Start() { go server.serve() }

func (server *Server) serve() {
	defer close(server.done)
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		server.connections.Add(1)
		go func() {
			defer server.connections.Done()
			defer connection.Close()
			server.handle(connection)
		}()
	}
}

func (server *Server) handle(connection net.Conn) {
	_ = connection.SetDeadline(server.now().Add(defaultCallDeadline))
	if err := server.verifyPeer(connection); err != nil {
		writeWireResponse(connection, wireResponse{Status: statusError, ErrorCode: "peer_forbidden", Message: "peer identity is not authorized"})
		return
	}
	request, err := readWireRequest(connection)
	if err != nil {
		writeWireResponse(connection, wireResponse{Status: statusError, ErrorCode: "protocol_invalid", Message: "request is malformed"})
		return
	}
	if err := validateWireRequest(request); err != nil {
		writeWireResponse(connection, wireResponse{Status: statusError, ErrorCode: "request_invalid", Message: err.Error()})
		return
	}
	var response wireResponse
	switch request.Operation {
	case operationAuthorize:
		response = server.authorize(request)
	case operationExecute:
		response = server.execute(request)
	default:
		response = wireResponse{Status: statusError, ErrorCode: "operation_forbidden", Message: "operation is not supported"}
	}
	writeWireResponse(connection, response)
}

func (server *Server) authorize(request wireRequest) wireResponse {
	actor, err := server.authorizer.Authorize(context.Background(), AuthorizationRequest{
		SessionToken: request.SessionToken, RequestID: request.RequestID, Action: request.Action,
		Resource: request.Resource, Revision: request.Revision, ParametersSHA256: request.ParametersSHA256,
	})
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "authorization_denied", Message: "authorization denied"}
	}
	capabilityBytes := make([]byte, 32)
	if _, err := rand.Read(capabilityBytes); err != nil {
		return wireResponse{Status: statusError, ErrorCode: "capability_failed", Message: "capability could not be created"}
	}
	capability := base64.RawURLEncoding.EncodeToString(capabilityBytes)
	now := server.now().UTC()
	binding := capabilityBinding{
		Action: request.Action, Resource: request.Resource, Revision: request.Revision,
		ParametersSHA256: request.ParametersSHA256, RequestID: request.RequestID, Actor: actor,
		ExpiresAt: now.Add(capabilityLifetime),
	}
	server.mu.Lock()
	server.pruneCapabilitiesLocked(now)
	if len(server.capabilities) >= maxCapabilities {
		server.mu.Unlock()
		return wireResponse{Status: statusError, ErrorCode: "capability_capacity", Message: "capability capacity reached"}
	}
	server.capabilities[capabilityKey(capability)] = binding
	server.mu.Unlock()
	return wireResponse{Status: statusOK, Capability: capability, ExpiresAt: binding.ExpiresAt.Unix()}
}

func (server *Server) execute(request wireRequest) wireResponse {
	now := server.now().UTC()
	server.mu.Lock()
	server.pruneCapabilitiesLocked(now)
	key := capabilityKey(request.Capability)
	binding, exists := server.capabilities[key]
	delete(server.capabilities, key)
	server.mu.Unlock()
	if !exists || !now.Before(binding.ExpiresAt) {
		return wireResponse{Status: statusError, ErrorCode: "capability_invalid", Message: "capability is invalid, expired or already used"}
	}
	if binding.Action != request.Action || binding.Resource != request.Resource || binding.Revision != request.Revision ||
		binding.ParametersSHA256 != request.ParametersSHA256 || binding.RequestID != request.RequestID ||
		parametersDigest(request.Parameters) != binding.ParametersSHA256 {
		return wireResponse{Status: statusError, ErrorCode: "capability_binding_mismatch", Message: "capability binding does not match the request"}
	}
	if server.auditor != nil {
		auditContext, auditCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := server.auditor.Record(auditContext, AuditRecord{
			OccurredAt: now, RequestID: binding.RequestID, Actor: binding.Actor, Action: binding.Action,
			Resource: binding.Resource, Revision: binding.Revision, Result: "attempted",
		})
		auditCancel()
		if err != nil {
			return wireResponse{Status: statusError, ErrorCode: "audit_failed", Message: "privileged action was not executed because intent audit failed"}
		}
	}
	executionContext, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	err := server.executor.Execute(executionContext, ExecutionRequest{
		Action: binding.Action, Resource: binding.Resource, Revision: binding.Revision,
		Parameters: append(json.RawMessage(nil), request.Parameters...), RequestID: binding.RequestID, Actor: binding.Actor,
	})
	cancel()
	result := "succeeded"
	if err != nil {
		result = "failed"
	}
	if server.auditor != nil {
		auditContext, auditCancel := context.WithTimeout(context.Background(), 10*time.Second)
		auditErr := server.auditor.Record(auditContext, AuditRecord{
			OccurredAt: server.now().UTC(), RequestID: binding.RequestID, Actor: binding.Actor, Action: binding.Action,
			Resource: binding.Resource, Revision: binding.Revision, Result: result,
		})
		auditCancel()
		if auditErr != nil && err == nil {
			return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "action completed but result audit failed"}
		}
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "action_failed", Message: "privileged action failed"}
	}
	return wireResponse{Status: statusOK}
}

func (server *Server) pruneCapabilitiesLocked(now time.Time) {
	for key, binding := range server.capabilities {
		if !now.Before(binding.ExpiresAt) {
			delete(server.capabilities, key)
		}
	}
}

func (server *Server) Close() error {
	var closeErr error
	server.closeOnce.Do(func() {
		server.mu.Lock()
		server.closed = true
		server.capabilities = make(map[string]capabilityBinding)
		server.mu.Unlock()
		closeErr = server.listener.Close()
		<-server.done
		server.connections.Wait()
	})
	return closeErr
}

type ClientOptions struct {
	Dial func(context.Context) (net.Conn, error)
}

type Client struct {
	dial func(context.Context) (net.Conn, error)
}

type clientBinding struct {
	RequestID, Resource, Revision, ParametersSHA256 string
	Action                                          Action
}

func NewClient(options ClientOptions) *Client { return &Client{dial: options.Dial} }

func (client *Client) Invoke(ctx context.Context, action Action, resource, revision string, parameters json.RawMessage) error {
	capability, binding, err := client.authorize(ctx, action, resource, revision, parameters)
	if err != nil {
		return err
	}
	_, err = client.execute(ctx, capability, binding, parameters)
	return err
}

func (client *Client) authorize(ctx context.Context, action Action, resource, revision string, parameters json.RawMessage) (string, clientBinding, error) {
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return "", clientBinding{}, errors.New("privileged Broker request authorization is missing")
	}
	binding := clientBinding{
		RequestID: authorization.RequestID, Action: action, Resource: resource, Revision: revision,
		ParametersSHA256: parametersDigest(parameters),
	}
	response, err := client.call(ctx, wireRequest{
		Version: ProtocolVersion, Operation: operationAuthorize, RequestID: binding.RequestID,
		SessionToken: authorization.SessionToken, Action: action, Resource: resource, Revision: revision,
		ParametersSHA256: binding.ParametersSHA256,
	})
	if err != nil {
		return "", clientBinding{}, err
	}
	if response.Capability == "" {
		return "", clientBinding{}, errors.New("privileged Broker returned an empty capability")
	}
	return response.Capability, binding, nil
}

func (client *Client) execute(ctx context.Context, capability string, binding clientBinding, parameters json.RawMessage) (wireResponse, error) {
	return client.call(ctx, wireRequest{
		Version: ProtocolVersion, Operation: operationExecute, RequestID: binding.RequestID,
		Capability: capability, Action: binding.Action, Resource: binding.Resource, Revision: binding.Revision,
		ParametersSHA256: binding.ParametersSHA256, Parameters: parameters,
	})
}

func (client *Client) call(ctx context.Context, request wireRequest) (wireResponse, error) {
	if client == nil || client.dial == nil {
		return wireResponse{}, errors.New("privileged Broker is unavailable")
	}
	connection, err := client.dial(ctx)
	if err != nil {
		return wireResponse{}, fmt.Errorf("connect privileged Broker: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(defaultCallDeadline)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return wireResponse{}, fmt.Errorf("write privileged Broker request: %w", err)
	}
	response, err := readWireResponse(connection)
	if err != nil {
		return wireResponse{}, fmt.Errorf("read privileged Broker response: %w", err)
	}
	if response.Status != statusOK {
		return response, fmt.Errorf("privileged Broker %s: %s", response.ErrorCode, response.Message)
	}
	return response, nil
}

func validateWireRequest(request wireRequest) error {
	if request.Version != ProtocolVersion || !requestIDPattern.MatchString(request.RequestID) {
		return errors.New("version or request ID is invalid")
	}
	if _, ok := actions[request.Action]; !ok {
		return errors.New("action is not registered")
	}
	if len(request.Resource) == 0 || len(request.Resource) > 512 || len(request.Revision) > 128 ||
		strings.ContainsAny(request.Resource+request.Revision, "\r\n\x00") {
		return errors.New("resource binding is invalid")
	}
	if len(request.ParametersSHA256) != sha256.Size*2 {
		return errors.New("parameter digest is invalid")
	}
	if _, err := hex.DecodeString(request.ParametersSHA256); err != nil || request.ParametersSHA256 != strings.ToLower(request.ParametersSHA256) {
		return errors.New("parameter digest is invalid")
	}
	switch request.Operation {
	case operationAuthorize:
		if len(request.SessionToken) < 16 || len(request.SessionToken) > 256 || strings.ContainsAny(request.SessionToken, "\r\n\x00") || request.Capability != "" || len(request.Parameters) != 0 {
			return errors.New("authorization request is invalid")
		}
	case operationExecute:
		if len(request.Capability) < 32 || len(request.Capability) > 128 || request.SessionToken != "" || len(request.Parameters) == 0 || len(request.Parameters) > MaxRequestBytes/2 || !json.Valid(request.Parameters) {
			return errors.New("execution request is invalid")
		}
	default:
		return errors.New("operation is invalid")
	}
	return nil
}

func parametersDigest(parameters []byte) string {
	digest := sha256.Sum256(parameters)
	return hex.EncodeToString(digest[:])
}

func capabilityKey(capability string) string {
	digest := sha256.Sum256([]byte(capability))
	return hex.EncodeToString(digest[:])
}

func readWireRequest(reader io.Reader) (wireRequest, error) {
	var request wireRequest
	if err := readBoundedJSONLine(reader, MaxRequestBytes, &request); err != nil {
		return wireRequest{}, err
	}
	return request, nil
}

func readWireResponse(reader io.Reader) (wireResponse, error) {
	var response wireResponse
	if err := readBoundedJSONLine(reader, MaxResponseBytes, &response); err != nil {
		return wireResponse{}, err
	}
	return response, nil
}

func readBoundedJSONLine(reader io.Reader, limit int, destination any) error {
	buffered := bufio.NewReaderSize(reader, limit+1)
	record, err := buffered.ReadBytes('\n')
	if err != nil || len(record) <= 1 || len(record) > limit+1 {
		return errors.New("bounded JSONL record is invalid")
	}
	record = bytes.TrimSuffix(record, []byte{'\n'})
	record = bytes.TrimSuffix(record, []byte{'\r'})
	if err := rejectDuplicateJSONKeys(record); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(record))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSONL record has trailing data")
	}
	return nil
}

func rejectDuplicateJSONKeys(record []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(record))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return errors.New("privileged Broker JSON has trailing data")
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
				return errors.New("privileged Broker object key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("privileged Broker request contains a duplicate key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("privileged Broker object is incomplete")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("privileged Broker array is incomplete")
		}
	default:
		return errors.New("privileged Broker JSON delimiter is invalid")
	}
	return nil
}

func writeWireResponse(writer io.Writer, response wireResponse) {
	if response.Status != statusOK && response.Status != statusError {
		response = wireResponse{Status: statusError, ErrorCode: "internal_error", Message: "invalid Broker response"}
	}
	_ = json.NewEncoder(writer).Encode(response)
}
