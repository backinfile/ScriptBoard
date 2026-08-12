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

	"github.com/go-webauthn/webauthn/webauthn"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/mfa"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/passkey"
	"scriptboard/internal/providercredential"
)

const (
	ProtocolVersion              = 1
	MaxRequestBytes              = 128 << 10
	MaxResponseBytes             = 5 << 20
	capabilityLifetime           = 30 * time.Second
	maxCapabilities              = 1024
	maxMFAVerifyFailureUsers     = 4096
	maxMFAVerifyFailures         = 5
	mfaVerifyFailureWindow       = 5 * time.Minute
	operationAuthorize           = "authorize"
	operationExecute             = "execute"
	operationCheckpointVerify    = "checkpoint_verify"
	operationCheckpointWrite     = "checkpoint_write"
	operationMFAStatus           = "mfa_status"
	operationMFABegin            = "mfa_begin"
	operationMFAConfirm          = "mfa_confirm"
	operationMFAVerify           = "mfa_verify"
	operationMFAReset            = "mfa_reset"
	operationPasskeyUser         = "passkey_user"
	operationPasskeyList         = "passkey_list"
	operationPasskeyAdd          = "passkey_add"
	operationPasskeyUpdate       = "passkey_update"
	operationPasskeyDelete       = "passkey_delete"
	operationPasskeyReset        = "passkey_reset"
	operationRemoteWebsiteStore  = "remote_website_store"
	operationRemoteWebsiteFetch  = "remote_website_fetch"
	operationRemoteWebsiteDelete = "remote_website_delete"
	operationProviderStore       = "provider_store"
	operationProviderDelete      = "provider_delete"
	operationProviderStart       = "provider_start"
	operationProviderStop        = "provider_stop"
	operationMySQLStore          = "mysql_store"
	operationMySQLDelete         = "mysql_delete"
	operationMySQLTest           = "mysql_test"
	operationMySQLDatabases      = "mysql_databases"
	operationMySQLStatus         = "mysql_status"
	operationMySQLExists         = "mysql_exists"
	operationMySQLCreate         = "mysql_create"
	operationMySQLReplace        = "mysql_replace"
	operationMySQLDrop           = "mysql_drop"
	operationMySQLDump           = "mysql_dump"
	operationMySQLImport         = "mysql_import"
	operationMySQLSetTools       = "mysql_set_tools"
	operationMySQLTestTools      = "mysql_test_tools"
	operationMySQLCancel         = "mysql_cancel"
	operationHostFilesRoots      = "host_files_roots"
	operationHostFilesList       = "host_files_list"
	operationHostFilesInfo       = "host_files_info"
	operationHostFilesReadText   = "host_files_read_text"
	operationHostFilesCanonical  = "host_files_canonical"
	operationHostFilesAvailable  = "host_files_available_name"
	operationHostFilesMkdir      = "host_files_mkdir"
	operationHostFilesToggleExec = "host_files_toggle_execute"
	operationHostFilesTrash      = "host_files_trash"
	operationHostFilesRestore    = "host_files_restore"
	operationHostFilesPurge      = "host_files_purge"
	operationHostFilesMove       = "host_files_move"
	operationHostFilesReadChunk  = "host_files_read_chunk"
	operationHostFilesOpenRead   = "host_files_open_read"
	operationHostFilesCloseRead  = "host_files_close_read"
	operationHostFilesUpload     = "host_files_upload"
	operationHostFilesSaveText   = "host_files_save_text"
	operationHostFilesRollback   = "host_files_rollback"
	operationHostFilesRemove     = "host_files_remove"
	operationHostFilesPrepare    = "host_files_prepare"
	operationHostFilesSameFS     = "host_files_same_filesystem"
	operationHostFilesAppend     = "host_files_append"
	statusOK                     = "ok"
	statusError                  = "error"
	defaultCallDeadline          = 35 * time.Second
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
	ActionMFABegin              Action = "mfa_begin"
	ActionMFAConfirm            Action = "mfa_confirm"
	ActionMFAReset              Action = "mfa_reset"
	ActionPasskeyAdd            Action = "passkey_add"
	ActionPasskeyDelete         Action = "passkey_delete"
	ActionPasskeyReset          Action = "passkey_reset"
	ActionRemoteWebsiteStore    Action = "remote_website_store"
	ActionRemoteWebsiteFetch    Action = "remote_website_fetch"
	ActionRemoteWebsiteDelete   Action = "remote_website_delete"
	ActionProviderStore         Action = "provider_store"
	ActionProviderDelete        Action = "provider_delete"
	ActionProviderStart         Action = "provider_start"
	ActionMySQLRead             Action = "mysql_read"
	ActionMySQLStore            Action = "mysql_store"
	ActionMySQLDelete           Action = "mysql_delete"
	ActionMySQLCreate           Action = "mysql_create"
	ActionMySQLReplace          Action = "mysql_replace"
	ActionMySQLDrop             Action = "mysql_drop"
	ActionMySQLDump             Action = "mysql_dump"
	ActionMySQLImport           Action = "mysql_import"
	ActionMySQLSetTools         Action = "mysql_set_tools"
	ActionMySQLCancel           Action = "mysql_cancel"
	ActionHostFilesRead         Action = "host_files_read"
	ActionHostFilesWrite        Action = "host_files_write"
	ActionHostFilesDelete       Action = "host_files_delete"
	ActionHostFilesMove         Action = "host_files_move"
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

type SessionAuthorizer interface {
	AuthorizeSession(context.Context, AuthorizationRequest) (Actor, error)
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
	OccurredAt       time.Time
	RequestID        string
	Actor            Actor
	Action           Action
	Resource         string
	Revision         string
	ParametersSHA256 string
	Result           string
}

type Auditor interface {
	Record(context.Context, AuditRecord) error
}

type CheckpointService interface {
	Verify(context.Context) (int64, error)
	Write(context.Context) (int64, error)
}

type MFAService interface {
	Status(string) (mfa.Status, error)
	Begin(string, string) (mfa.Enrollment, error)
	Confirm(string, string) ([]string, error)
	Verify(string, string) (bool, error)
	Reset(string) error
}

type PasskeyService interface {
	User(string, string) (passkey.User, error)
	List(string) ([]passkey.CredentialView, error)
	Add(string, string, webauthn.Credential) error
	Update(string, webauthn.Credential) error
	Delete(string, string) error
	Reset(string) error
}

type RemoteWebsiteService interface {
	Store(context.Context, string, string, string) error
	Fetch(context.Context, string, string) (json.RawMessage, error)
	Delete(context.Context, string) error
}

type ProviderCredentialService interface {
	Store(context.Context, string, providercredential.Record, string) error
	Delete(context.Context, string, string) error
	Start(context.Context, string, string) (providercredential.Session, error)
	Stop(context.Context, string) error
	Close(context.Context) error
}

type MySQLService interface {
	mysqlmanager.Backend
	ValidateInstance(context.Context, mysqlmanager.Instance) error
	ValidateInstanceID(context.Context, string) error
	CancelOperation(context.Context, string) error
	ArtifactRoot(context.Context) (string, error)
}

type HostFilesService interface {
	Roots(context.Context) ([]hostfiles.Entry, error)
	List(context.Context, string) ([]hostfiles.Entry, error)
	Info(context.Context, string) (HostFileInfo, error)
	ReadText(context.Context, string, int64) (hostfiles.TextDocument, error)
	Canonical(context.Context, hostFilesCanonicalKind, string, string) (string, error)
	AvailableName(context.Context, string, string) (string, error)
	CreateDirectory(context.Context, string, string) error
	ToggleOwnerExecute(context.Context, string) (bool, error)
	MoveToTrash(context.Context, string, string) (hostfiles.Trashed, error)
	RestoreFromTrash(context.Context, string, string, bool) (string, error)
	PurgeTrash(context.Context, string) error
	Move(context.Context, string, string) error
	OpenRead(context.Context, string) (string, HostFileInfo, error)
	ReadChunk(context.Context, string, int64, int) ([]byte, error)
	CloseRead(context.Context, string) error
	Upload(context.Context, string, string, string, int64, bool, string) (*hostfiles.Trashed, error)
	SaveText(context.Context, string, string, string, string, int64) (hostfiles.Trashed, error)
	RollbackTextSave(context.Context, string, string) error
	RemoveRegular(context.Context, string) error
	Prepare(context.Context, string, bool) (hostFilesPrepared, error)
	SameFilesystem(context.Context, string, string) (bool, error)
	AppendText(context.Context, string, string) error
}

type ServerOptions struct {
	Listener       net.Listener
	VerifyPeer     func(net.Conn) error
	Authorizer     Authorizer
	Executor       Executor
	Auditor        Auditor
	Checkpoint     CheckpointService
	MFA            MFAService
	Passkeys       PasskeyService
	RemoteWebsites RemoteWebsiteService
	Providers      ProviderCredentialService
	MySQL          MySQLService
	HostFiles      HostFilesService
	Now            func() time.Time
}

type Server struct {
	listener       net.Listener
	verifyPeer     func(net.Conn) error
	authorizer     Authorizer
	executor       Executor
	auditor        Auditor
	checkpoint     CheckpointService
	mfa            MFAService
	passkeys       PasskeyService
	remoteWebsites RemoteWebsiteService
	providers      ProviderCredentialService
	mysql          MySQLService
	hostFiles      HostFilesService
	now            func() time.Time

	mu                sync.Mutex
	capabilities      map[string]capabilityBinding
	closed            bool
	closeOnce         sync.Once
	done              chan struct{}
	connections       sync.WaitGroup
	mfaVerifyFailures map[string]mfaVerifyFailure
}

type mfaVerifyFailure struct {
	count       int
	windowStart time.Time
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
	Version               int                   `json:"version"`
	Operation             string                `json:"operation"`
	RequestID             string                `json:"request_id"`
	SessionToken          string                `json:"session_token,omitempty"`
	Capability            string                `json:"capability,omitempty"`
	Action                Action                `json:"action"`
	Resource              string                `json:"resource"`
	Revision              string                `json:"revision"`
	ParametersSHA256      string                `json:"parameters_sha256"`
	Parameters            json.RawMessage       `json:"parameters,omitempty"`
	MFAUserID             string                `json:"mfa_user_id,omitempty"`
	MFAAccount            string                `json:"mfa_account,omitempty"`
	MFACode               string                `json:"mfa_code,omitempty"`
	PasskeyUserID         string                `json:"passkey_user_id,omitempty"`
	PasskeyUsername       string                `json:"passkey_username,omitempty"`
	PasskeyName           string                `json:"passkey_name,omitempty"`
	PasskeyCredentialID   string                `json:"passkey_credential_id,omitempty"`
	PasskeyCredential     *webauthn.Credential  `json:"passkey_credential,omitempty"`
	RemoteWebsiteID       string                `json:"remote_website_id,omitempty"`
	RemoteWebsiteEndpoint string                `json:"remote_website_endpoint,omitempty"`
	RemoteWebsiteKey      string                `json:"remote_website_key,omitempty"`
	RemoteWebsiteLocale   string                `json:"remote_website_locale,omitempty"`
	ProviderID            string                `json:"provider_id,omitempty"`
	ProviderName          string                `json:"provider_name,omitempty"`
	ProviderModel         string                `json:"provider_model,omitempty"`
	ProviderEndpoint      string                `json:"provider_endpoint,omitempty"`
	ProviderCredential    string                `json:"provider_credential,omitempty"`
	ProviderShared        bool                  `json:"provider_shared,omitempty"`
	ProviderSessionHandle string                `json:"provider_session_handle,omitempty"`
	MySQL                 *mysqlWireRequest     `json:"mysql,omitempty"`
	HostFiles             *hostFilesWireRequest `json:"host_files,omitempty"`
}

type wireResponse struct {
	Status                string                   `json:"status"`
	Capability            string                   `json:"capability,omitempty"`
	ExpiresAt             int64                    `json:"expires_at,omitempty"`
	ErrorCode             string                   `json:"error_code,omitempty"`
	Message               string                   `json:"message,omitempty"`
	EventID               int64                    `json:"event_id,omitempty"`
	MFAEnabled            bool                     `json:"mfa_enabled,omitempty"`
	MFARecoveryCodes      int                      `json:"mfa_recovery_codes,omitempty"`
	MFAEnrollment         *mfa.Enrollment          `json:"mfa_enrollment,omitempty"`
	MFARecoveryValues     []string                 `json:"mfa_recovery_values,omitempty"`
	MFAVerified           bool                     `json:"mfa_verified,omitempty"`
	PasskeyUser           *passkey.User            `json:"passkey_user,omitempty"`
	PasskeyCredentials    []passkey.CredentialView `json:"passkey_credentials,omitempty"`
	RemoteWebsitePayload  json.RawMessage          `json:"remote_website_payload,omitempty"`
	ProviderProxyEndpoint string                   `json:"provider_proxy_endpoint,omitempty"`
	ProviderCapability    string                   `json:"provider_capability,omitempty"`
	ProviderSessionHandle string                   `json:"provider_session_handle,omitempty"`
	MySQL                 *mysqlWireResponse       `json:"mysql,omitempty"`
	HostFiles             *hostFilesWireResponse   `json:"host_files,omitempty"`
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
		executor: options.Executor, auditor: options.Auditor, checkpoint: options.Checkpoint, mfa: options.MFA, passkeys: options.Passkeys, remoteWebsites: options.RemoteWebsites, providers: options.Providers, mysql: options.MySQL, hostFiles: options.HostFiles, now: now,
		capabilities: make(map[string]capabilityBinding), done: make(chan struct{}),
		mfaVerifyFailures: make(map[string]mfaVerifyFailure),
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
	case operationCheckpointVerify, operationCheckpointWrite:
		response = server.checkpointOperation(request.Operation)
	case operationMFAStatus, operationMFABegin, operationMFAConfirm, operationMFAVerify, operationMFAReset:
		response = server.mfaOperation(request)
	case operationPasskeyUser, operationPasskeyList, operationPasskeyAdd, operationPasskeyUpdate, operationPasskeyDelete, operationPasskeyReset:
		response = server.passkeyOperation(request)
	case operationRemoteWebsiteStore, operationRemoteWebsiteFetch, operationRemoteWebsiteDelete:
		response = server.remoteWebsiteOperation(request)
	case operationProviderStore, operationProviderDelete, operationProviderStart, operationProviderStop:
		response = server.providerOperation(request)
	case operationMySQLStore, operationMySQLDelete, operationMySQLTest, operationMySQLDatabases, operationMySQLStatus,
		operationMySQLExists, operationMySQLCreate, operationMySQLReplace, operationMySQLDrop, operationMySQLDump,
		operationMySQLImport, operationMySQLSetTools, operationMySQLTestTools, operationMySQLCancel:
		_ = connection.SetDeadline(server.now().Add(2 * time.Hour))
		operationContext, cancelOperation := context.WithCancel(context.Background())
		peerClosed := make(chan struct{})
		go func() {
			defer close(peerClosed)
			var probe [1]byte
			_, _ = connection.Read(probe[:])
			cancelOperation()
		}()
		response = server.mysqlOperation(operationContext, request)
		cancelOperation()
	case operationHostFilesRoots, operationHostFilesList, operationHostFilesInfo, operationHostFilesReadText,
		operationHostFilesCanonical, operationHostFilesAvailable, operationHostFilesMkdir, operationHostFilesToggleExec,
		operationHostFilesTrash, operationHostFilesRestore, operationHostFilesPurge, operationHostFilesMove,
		operationHostFilesOpenRead, operationHostFilesReadChunk, operationHostFilesCloseRead, operationHostFilesUpload, operationHostFilesSaveText, operationHostFilesRollback,
		operationHostFilesRemove, operationHostFilesPrepare, operationHostFilesSameFS, operationHostFilesAppend:
		response = server.hostFilesOperation(request)
	default:
		response = wireResponse{Status: statusError, ErrorCode: "operation_forbidden", Message: "operation is not supported"}
	}
	writeWireResponse(connection, response)
}

func (server *Server) remoteWebsiteOperation(request wireRequest) wireResponse {
	if server.remoteWebsites == nil {
		return wireResponse{Status: statusError, ErrorCode: "remote_website_unavailable", Message: "remote website service is unavailable"}
	}
	mutation, response := server.authorizeRemoteWebsiteOperation(request)
	if response.Status != "" {
		return response
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response = wireResponse{Status: statusOK}
	var err error
	switch request.Operation {
	case operationRemoteWebsiteStore:
		err = server.remoteWebsites.Store(ctx, request.RemoteWebsiteID, request.RemoteWebsiteEndpoint, request.RemoteWebsiteKey)
	case operationRemoteWebsiteFetch:
		response.RemoteWebsitePayload, err = server.remoteWebsites.Fetch(ctx, request.RemoteWebsiteID, request.RemoteWebsiteLocale)
	case operationRemoteWebsiteDelete:
		err = server.remoteWebsites.Delete(ctx, request.RemoteWebsiteID)
	}
	if err == nil {
		if mutation != nil {
			if auditErr := server.recordCredentialMutation(*mutation, "succeeded"); auditErr != nil {
				return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "remote website operation completed but result audit failed"}
			}
		}
		return response
	}
	if mutation != nil {
		_ = server.recordCredentialMutation(*mutation, "failed")
	}
	return wireResponse{Status: statusError, ErrorCode: "remote_website_failed", Message: "remote website operation failed"}
}

func (server *Server) providerOperation(request wireRequest) wireResponse {
	if server.providers == nil {
		return wireResponse{Status: statusError, ErrorCode: "provider_unavailable", Message: "provider credential service is unavailable"}
	}
	if request.Operation == operationProviderStop {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.providers.Stop(ctx, request.ProviderSessionHandle); err != nil {
			return wireResponse{Status: statusError, ErrorCode: "provider_stop_failed", Message: "provider proxy session could not be stopped"}
		}
		return wireResponse{Status: statusOK}
	}

	action := ActionProviderStart
	if request.Operation == operationProviderStore {
		action = ActionProviderStore
	} else if request.Operation == operationProviderDelete {
		action = ActionProviderDelete
	}
	parameters, _ := json.Marshal(struct {
		Provider   string `json:"provider,omitempty"`
		Model      string `json:"model,omitempty"`
		Endpoint   string `json:"endpoint,omitempty"`
		Credential string `json:"credential,omitempty"`
		Shared     bool   `json:"shared,omitempty"`
	}{request.ProviderName, request.ProviderModel, request.ProviderEndpoint, request.ProviderCredential, request.ProviderShared})
	authorization := AuthorizationRequest{
		SessionToken: request.SessionToken, RequestID: request.RequestID, Action: action,
		Resource: request.ProviderID, Revision: "assistant-provider-record-v1", ParametersSHA256: parametersDigest(parameters),
	}
	var (
		actor Actor
		err   error
	)
	if request.Operation == operationProviderStart {
		if authorizer, ok := server.authorizer.(SessionAuthorizer); ok {
			actor, err = authorizer.AuthorizeSession(context.Background(), authorization)
		} else {
			actor, err = server.authorizer.Authorize(context.Background(), authorization)
		}
	} else {
		actor, err = server.authorizer.Authorize(context.Background(), authorization)
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "authorization_denied", Message: "provider operation authorization denied"}
	}

	var mutation *credentialMutation
	if request.Operation != operationProviderStart {
		value := credentialMutation{
			action: action, resource: request.ProviderID, revision: "assistant-provider-record-v1",
			requestID: request.RequestID, parametersSHA256: authorization.ParametersSHA256, actor: actor,
		}
		if err := server.recordCredentialMutation(value, "attempted"); err != nil {
			return wireResponse{Status: statusError, ErrorCode: "audit_failed", Message: "provider operation was not executed because intent audit failed"}
		}
		mutation = &value
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	response := wireResponse{Status: statusOK}
	switch request.Operation {
	case operationProviderStore:
		err = server.providers.Store(ctx, actor.UserID, providercredential.Record{
			ID: request.ProviderID, OwnerUserID: actor.UserID, Provider: request.ProviderName,
			Model: request.ProviderModel, Endpoint: request.ProviderEndpoint, Shared: request.ProviderShared,
		}, request.ProviderCredential)
	case operationProviderDelete:
		err = server.providers.Delete(ctx, actor.UserID, request.ProviderID)
	case operationProviderStart:
		var session providercredential.Session
		session, err = server.providers.Start(ctx, actor.UserID, request.ProviderID)
		response.ProviderProxyEndpoint = session.Endpoint
		response.ProviderCapability = session.Capability
		response.ProviderSessionHandle = session.Handle
	}
	if err == nil {
		if mutation != nil {
			if auditErr := server.recordCredentialMutation(*mutation, "succeeded"); auditErr != nil {
				return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "provider operation completed but result audit failed"}
			}
		}
		return response
	}
	if mutation != nil {
		_ = server.recordCredentialMutation(*mutation, "failed")
	}
	return wireResponse{Status: statusError, ErrorCode: "provider_failed", Message: "provider operation failed"}
}

func (server *Server) authorizeRemoteWebsiteOperation(request wireRequest) (*credentialMutation, wireResponse) {
	if request.Operation == operationRemoteWebsiteFetch {
		authorization := AuthorizationRequest{
			SessionToken: request.SessionToken, RequestID: request.RequestID, Action: ActionRemoteWebsiteFetch,
			Resource: request.RemoteWebsiteID, Revision: "remote-website-connection-v1",
			ParametersSHA256: parametersDigest([]byte(request.RemoteWebsiteLocale)),
		}
		var err error
		if authorizer, ok := server.authorizer.(SessionAuthorizer); ok {
			_, err = authorizer.AuthorizeSession(context.Background(), authorization)
		} else {
			_, err = server.authorizer.Authorize(context.Background(), authorization)
		}
		if err != nil {
			return nil, wireResponse{Status: statusError, ErrorCode: "authorization_denied", Message: "remote website session authorization denied"}
		}
		return nil, wireResponse{}
	}
	action := ActionRemoteWebsiteFetch
	switch request.Operation {
	case operationRemoteWebsiteStore:
		action = ActionRemoteWebsiteStore
	case operationRemoteWebsiteDelete:
		action = ActionRemoteWebsiteDelete
	}
	parameters, _ := json.Marshal(struct {
		Endpoint string `json:"endpoint,omitempty"`
		Key      string `json:"key,omitempty"`
		Locale   string `json:"locale,omitempty"`
	}{request.RemoteWebsiteEndpoint, request.RemoteWebsiteKey, request.RemoteWebsiteLocale})
	return server.authorizeDomainOperation(request, action, request.RemoteWebsiteID, "remote-website-connection-v1", parameters, false)
}

func (server *Server) passkeyOperation(request wireRequest) wireResponse {
	if server.passkeys == nil {
		return wireResponse{Status: statusError, ErrorCode: "passkey_unavailable", Message: "passkey service is unavailable"}
	}
	mutation, response := server.authorizePasskeyMutation(request)
	if response.Status != "" {
		return response
	}
	var err error
	response = wireResponse{Status: statusOK}
	switch request.Operation {
	case operationPasskeyUser:
		var user passkey.User
		user, err = server.passkeys.User(request.PasskeyUserID, request.PasskeyUsername)
		response.PasskeyUser = &user
	case operationPasskeyList:
		response.PasskeyCredentials, err = server.passkeys.List(request.PasskeyUserID)
	case operationPasskeyAdd:
		err = server.passkeys.Add(request.PasskeyUserID, request.PasskeyName, *request.PasskeyCredential)
	case operationPasskeyUpdate:
		err = server.passkeys.Update(request.PasskeyUserID, *request.PasskeyCredential)
	case operationPasskeyDelete:
		err = server.passkeys.Delete(request.PasskeyUserID, request.PasskeyCredentialID)
	case operationPasskeyReset:
		err = server.passkeys.Reset(request.PasskeyUserID)
	}
	if err == nil {
		if mutation != nil {
			if auditErr := server.recordCredentialMutation(*mutation, "succeeded"); auditErr != nil {
				return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "passkey operation completed but result audit failed"}
			}
		}
		return response
	}
	if mutation != nil {
		_ = server.recordCredentialMutation(*mutation, "failed")
	}
	code := "passkey_failed"
	switch {
	case errors.Is(err, passkey.ErrDuplicateCredential):
		code = "passkey_duplicate"
	case errors.Is(err, passkey.ErrCredentialLimit):
		code = "passkey_limit"
	case errors.Is(err, passkey.ErrCredentialNotFound):
		code = "passkey_not_found"
	case errors.Is(err, passkey.ErrCredentialIdentityMismatch):
		code = "passkey_identity_mismatch"
	case errors.Is(err, passkey.ErrCredentialTooLarge):
		code = "passkey_too_large"
	}
	return wireResponse{Status: statusError, ErrorCode: code, Message: "passkey operation failed"}
}

func (server *Server) mfaOperation(request wireRequest) wireResponse {
	if server.mfa == nil {
		return wireResponse{Status: statusError, ErrorCode: "mfa_unavailable", Message: "MFA service is unavailable"}
	}
	mutation, response := server.authorizeMFAMutation(request)
	if response.Status != "" {
		return response
	}
	var err error
	response = wireResponse{Status: statusOK}
	switch request.Operation {
	case operationMFAStatus:
		var status mfa.Status
		status, err = server.mfa.Status(request.MFAUserID)
		response.MFAEnabled, response.MFARecoveryCodes = status.Enabled, status.RecoveryCodes
	case operationMFABegin:
		var enrollment mfa.Enrollment
		enrollment, err = server.mfa.Begin(request.MFAUserID, request.MFAAccount)
		response.MFAEnrollment = &enrollment
	case operationMFAConfirm:
		response.MFARecoveryValues, err = server.mfa.Confirm(request.MFAUserID, request.MFACode)
	case operationMFAVerify:
		if !server.allowMFAVerify(request.MFAUserID) {
			return response
		}
		response.MFAVerified, err = server.mfa.Verify(request.MFAUserID, request.MFACode)
		if err == nil {
			server.recordMFAVerifyResult(request.MFAUserID, response.MFAVerified)
		}
	case operationMFAReset:
		err = server.mfa.Reset(request.MFAUserID)
	}
	if err == nil {
		if mutation != nil {
			if auditErr := server.recordCredentialMutation(*mutation, "succeeded"); auditErr != nil {
				return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "MFA operation completed but result audit failed"}
			}
		}
		return response
	}
	if mutation != nil {
		_ = server.recordCredentialMutation(*mutation, "failed")
	}
	code := "mfa_failed"
	switch {
	case errors.Is(err, mfa.ErrAlreadyEnabled):
		code = "mfa_already_enabled"
	case errors.Is(err, mfa.ErrEnrollmentAbsent):
		code = "mfa_enrollment_absent"
	case errors.Is(err, mfa.ErrInvalidCode):
		code = "mfa_invalid_code"
	}
	return wireResponse{Status: statusError, ErrorCode: code, Message: "MFA operation failed"}
}

func (server *Server) allowMFAVerify(userID string) bool {
	now := server.now().UTC()
	server.mu.Lock()
	defer server.mu.Unlock()
	server.pruneMFAVerifyFailuresLocked(now)
	failure, exists := server.mfaVerifyFailures[userID]
	if exists && failure.count >= maxMFAVerifyFailures {
		return false
	}
	if !exists {
		if len(server.mfaVerifyFailures) >= maxMFAVerifyFailureUsers {
			return false
		}
		failure.windowStart = now
	}
	failure.count++
	server.mfaVerifyFailures[userID] = failure
	return true
}

func (server *Server) recordMFAVerifyResult(userID string, verified bool) {
	now := server.now().UTC()
	server.mu.Lock()
	defer server.mu.Unlock()
	server.pruneMFAVerifyFailuresLocked(now)
	if verified {
		delete(server.mfaVerifyFailures, userID)
	}
}

func (server *Server) pruneMFAVerifyFailuresLocked(now time.Time) {
	for userID, failure := range server.mfaVerifyFailures {
		if now.Sub(failure.windowStart) >= mfaVerifyFailureWindow || failure.windowStart.After(now.Add(time.Minute)) {
			delete(server.mfaVerifyFailures, userID)
		}
	}
}

type credentialMutation struct {
	action                      Action
	resource, revision          string
	requestID, parametersSHA256 string
	actor                       Actor
}

func (server *Server) authorizeMFAMutation(request wireRequest) (*credentialMutation, wireResponse) {
	var action Action
	switch request.Operation {
	case operationMFABegin:
		action = ActionMFABegin
	case operationMFAConfirm:
		action = ActionMFAConfirm
	case operationMFAReset:
		action = ActionMFAReset
	default:
		return nil, wireResponse{}
	}
	parameters, _ := json.Marshal(struct {
		Account string `json:"account,omitempty"`
		Code    string `json:"code,omitempty"`
	}{request.MFAAccount, request.MFACode})
	return server.authorizeCredentialMutation(request, action, request.MFAUserID, "mfa-state-v1", parameters)
}

func (server *Server) authorizePasskeyMutation(request wireRequest) (*credentialMutation, wireResponse) {
	var action Action
	switch request.Operation {
	case operationPasskeyAdd:
		action = ActionPasskeyAdd
	case operationPasskeyDelete:
		action = ActionPasskeyDelete
	case operationPasskeyReset:
		action = ActionPasskeyReset
	default:
		return nil, wireResponse{}
	}
	parameters, _ := json.Marshal(struct {
		Name         string               `json:"name,omitempty"`
		CredentialID string               `json:"credential_id,omitempty"`
		Credential   *webauthn.Credential `json:"credential,omitempty"`
	}{request.PasskeyName, request.PasskeyCredentialID, request.PasskeyCredential})
	return server.authorizeCredentialMutation(request, action, request.PasskeyUserID, "passkey-state-v1", parameters)
}

func (server *Server) authorizeCredentialMutation(request wireRequest, action Action, resource, revision string, parameters []byte) (*credentialMutation, wireResponse) {
	return server.authorizeDomainOperation(request, action, resource, revision, parameters, true)
}

func (server *Server) authorizeDomainOperation(request wireRequest, action Action, resource, revision string, parameters []byte, bindActorToResource bool) (*credentialMutation, wireResponse) {
	digest := parametersDigest(parameters)
	actor, err := server.authorizer.Authorize(context.Background(), AuthorizationRequest{
		SessionToken: request.SessionToken, RequestID: request.RequestID, Action: action,
		Resource: resource, Revision: revision, ParametersSHA256: digest,
	})
	if err != nil {
		return nil, wireResponse{Status: statusError, ErrorCode: "authorization_denied", Message: "credential operation authorization denied"}
	}
	if bindActorToResource && actor.UserID != resource {
		return nil, wireResponse{Status: statusError, ErrorCode: "authorization_denied", Message: "credential operation user binding denied"}
	}
	mutation := credentialMutation{action: action, resource: resource, revision: revision, requestID: request.RequestID, parametersSHA256: digest, actor: actor}
	if err := server.recordCredentialMutation(mutation, "attempted"); err != nil {
		return nil, wireResponse{Status: statusError, ErrorCode: "audit_failed", Message: "credential operation was not executed because intent audit failed"}
	}
	return &mutation, wireResponse{}
}

func (server *Server) recordCredentialMutation(mutation credentialMutation, result string) error {
	if server.auditor == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.auditor.Record(ctx, AuditRecord{
		OccurredAt: server.now().UTC(), RequestID: mutation.requestID, Actor: mutation.actor,
		Action: mutation.action, Resource: mutation.resource, Revision: mutation.revision,
		ParametersSHA256: mutation.parametersSHA256, Result: result,
	})
}

func (server *Server) checkpointOperation(operation string) wireResponse {
	if server.checkpoint == nil {
		return wireResponse{Status: statusError, ErrorCode: "checkpoint_unavailable", Message: "audit checkpoint service is unavailable"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var (
		eventID int64
		err     error
	)
	if operation == operationCheckpointVerify {
		eventID, err = server.checkpoint.Verify(ctx)
	} else {
		eventID, err = server.checkpoint.Write(ctx)
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "checkpoint_failed", Message: "audit checkpoint operation failed"}
	}
	return wireResponse{Status: statusOK, EventID: eventID}
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
			Resource: binding.Resource, Revision: binding.Revision, ParametersSHA256: binding.ParametersSHA256, Result: "attempted",
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
			Resource: binding.Resource, Revision: binding.Revision, ParametersSHA256: binding.ParametersSHA256, Result: result,
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
		if server.providers != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			providerErr := server.providers.Close(ctx)
			cancel()
			if closeErr == nil {
				closeErr = providerErr
			}
		}
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
	stopCancellationWatch := make(chan struct{})
	defer close(stopCancellationWatch)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopCancellationWatch:
		}
	}()
	deadline := time.Now().Add(defaultCallDeadline)
	if isMySQLOperation(request.Operation) {
		deadline = time.Now().Add(2 * time.Hour)
	}
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
	if request.Operation == operationCheckpointVerify || request.Operation == operationCheckpointWrite {
		if request.SessionToken != "" || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
			request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || request.HostFiles != nil {
			return errors.New("checkpoint request is invalid")
		}
		return nil
	}
	if isMFAOperation(request.Operation) {
		if request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
			request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || request.HostFiles != nil || len(request.MFAUserID) == 0 || len(request.MFAUserID) > 160 || strings.ContainsAny(request.MFAUserID, "\r\n\x00") {
			return errors.New("MFA request is invalid")
		}
		switch request.Operation {
		case operationMFAStatus:
			if request.SessionToken != "" || request.MFAAccount != "" || request.MFACode != "" {
				return errors.New("MFA request contains unrelated fields")
			}
		case operationMFAReset:
			if !validCredentialSessionToken(request.SessionToken) || request.MFAAccount != "" || request.MFACode != "" {
				return errors.New("MFA reset request is invalid")
			}
		case operationMFABegin:
			if !validCredentialSessionToken(request.SessionToken) || len(request.MFAAccount) == 0 || len(request.MFAAccount) > 320 || strings.ContainsAny(request.MFAAccount, "\r\n\x00") || request.MFACode != "" {
				return errors.New("MFA enrollment request is invalid")
			}
		case operationMFAConfirm:
			if !validCredentialSessionToken(request.SessionToken) || request.MFAAccount != "" || len(request.MFACode) == 0 || len(request.MFACode) > 128 || strings.ContainsAny(request.MFACode, "\r\n\x00") {
				return errors.New("MFA confirmation request is invalid")
			}
		case operationMFAVerify:
			if request.SessionToken != "" || request.MFAAccount != "" || len(request.MFACode) == 0 || len(request.MFACode) > 128 || strings.ContainsAny(request.MFACode, "\r\n\x00") {
				return errors.New("MFA verification request is invalid")
			}
		}
		return nil
	}
	if isPasskeyOperation(request.Operation) {
		return validatePasskeyRequest(request)
	}
	if isRemoteWebsiteOperation(request.Operation) {
		return validateRemoteWebsiteRequest(request)
	}
	if isProviderOperation(request.Operation) {
		return validateProviderRequest(request)
	}
	if isMySQLOperation(request.Operation) {
		return validateMySQLRequest(request)
	}
	if isHostFilesOperation(request.Operation) {
		return validateHostFilesRequest(request)
	}
	if hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || request.HostFiles != nil {
		return errors.New("privileged action contains credential-domain fields")
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

func validatePasskeyRequest(request wireRequest) error {
	if request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
		request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || request.HostFiles != nil || len(request.PasskeyUserID) == 0 ||
		len(request.PasskeyUserID) > 160 || strings.ContainsAny(request.PasskeyUserID, "\r\n\x00") {
		return errors.New("passkey request is invalid")
	}
	switch request.Operation {
	case operationPasskeyUser:
		if request.SessionToken != "" || len(request.PasskeyUsername) == 0 || len(request.PasskeyUsername) > 320 || strings.ContainsAny(request.PasskeyUsername, "\r\n\x00") ||
			request.PasskeyName != "" || request.PasskeyCredentialID != "" || request.PasskeyCredential != nil {
			return errors.New("passkey user request is invalid")
		}
	case operationPasskeyList:
		if request.SessionToken != "" || request.PasskeyUsername != "" || request.PasskeyName != "" || request.PasskeyCredentialID != "" || request.PasskeyCredential != nil {
			return errors.New("passkey request contains unrelated fields")
		}
	case operationPasskeyReset:
		if !validCredentialSessionToken(request.SessionToken) || request.PasskeyUsername != "" || request.PasskeyName != "" || request.PasskeyCredentialID != "" || request.PasskeyCredential != nil {
			return errors.New("passkey reset request is invalid")
		}
	case operationPasskeyAdd:
		if !validCredentialSessionToken(request.SessionToken) || request.PasskeyUsername != "" || len(request.PasskeyName) > 256 || strings.ContainsAny(request.PasskeyName, "\r\n\x00") ||
			request.PasskeyCredentialID != "" || !validPasskeyCredential(request.PasskeyCredential) {
			return errors.New("passkey add request is invalid")
		}
	case operationPasskeyUpdate:
		if request.SessionToken != "" || request.PasskeyUsername != "" || request.PasskeyName != "" || request.PasskeyCredentialID != "" || !validPasskeyCredential(request.PasskeyCredential) {
			return errors.New("passkey update request is invalid")
		}
	case operationPasskeyDelete:
		if !validCredentialSessionToken(request.SessionToken) || request.PasskeyUsername != "" || request.PasskeyName != "" || request.PasskeyCredential != nil || len(request.PasskeyCredentialID) == 0 ||
			len(request.PasskeyCredentialID) > 1024 || len(request.PasskeyCredentialID)%2 != 0 {
			return errors.New("passkey delete request is invalid")
		}
		if _, err := hex.DecodeString(request.PasskeyCredentialID); err != nil || request.PasskeyCredentialID != strings.ToLower(request.PasskeyCredentialID) {
			return errors.New("passkey credential ID is invalid")
		}
	}
	return nil
}

func validateRemoteWebsiteRequest(request wireRequest) error {
	if request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" ||
		len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasProviderFields(request) || request.MySQL != nil || request.HostFiles != nil || !validRemoteWebsiteID(request.RemoteWebsiteID) ||
		!validCredentialSessionToken(request.SessionToken) {
		return errors.New("remote website request is invalid")
	}
	switch request.Operation {
	case operationRemoteWebsiteStore:
		if len(request.RemoteWebsiteEndpoint) == 0 || len(request.RemoteWebsiteEndpoint) > 2048 || strings.ContainsAny(request.RemoteWebsiteEndpoint, "\r\n\x00") ||
			!validRemoteWebsiteKey(request.RemoteWebsiteKey) || request.RemoteWebsiteLocale != "" {
			return errors.New("remote website store request is invalid")
		}
	case operationRemoteWebsiteFetch:
		if request.RemoteWebsiteEndpoint != "" || request.RemoteWebsiteKey != "" || len(request.RemoteWebsiteLocale) > 64 || strings.ContainsAny(request.RemoteWebsiteLocale, "\r\n\x00") {
			return errors.New("remote website fetch request is invalid")
		}
	case operationRemoteWebsiteDelete:
		if request.RemoteWebsiteEndpoint != "" || request.RemoteWebsiteKey != "" || request.RemoteWebsiteLocale != "" {
			return errors.New("remote website delete request is invalid")
		}
	}
	return nil
}

func validateProviderRequest(request wireRequest) error {
	if request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" ||
		len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || request.MySQL != nil || request.HostFiles != nil {
		return errors.New("provider request is invalid")
	}
	if request.Operation == operationProviderStop {
		if request.SessionToken != "" || request.ProviderID != "" || request.ProviderName != "" || request.ProviderModel != "" || request.ProviderEndpoint != "" ||
			request.ProviderCredential != "" || request.ProviderShared || !validProviderSessionHandle(request.ProviderSessionHandle) {
			return errors.New("provider stop request is invalid")
		}
		return nil
	}
	if !validCredentialSessionToken(request.SessionToken) || !validRemoteWebsiteID(request.ProviderID) || request.ProviderSessionHandle != "" {
		return errors.New("provider request is invalid")
	}
	switch request.Operation {
	case operationProviderStore:
		if len(request.ProviderName) == 0 || len(request.ProviderName) > 32 || strings.ContainsAny(request.ProviderName, "\r\n\x00") ||
			len(request.ProviderModel) == 0 || len(request.ProviderModel) > 160 || strings.ContainsAny(request.ProviderModel, "\r\n\x00") ||
			len(request.ProviderEndpoint) == 0 || len(request.ProviderEndpoint) > 2048 || strings.ContainsAny(request.ProviderEndpoint, "\r\n\x00") ||
			len(request.ProviderCredential) > 8<<10 || strings.ContainsAny(request.ProviderCredential, "\r\n\x00") {
			return errors.New("provider store request is invalid")
		}
	case operationProviderDelete, operationProviderStart:
		if request.ProviderName != "" || request.ProviderModel != "" || request.ProviderEndpoint != "" || request.ProviderCredential != "" || request.ProviderShared {
			return errors.New("provider request contains unrelated fields")
		}
	}
	return nil
}

func validProviderSessionHandle(value string) bool {
	if len(value) != 43 || !isBase64URLValue(value) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validRemoteWebsiteID(value string) bool {
	return len(value) > 0 && len(value) <= 160 && !strings.ContainsAny(value, "\r\n\x00")
}

func validRemoteWebsiteKey(value string) bool {
	if !strings.HasPrefix(value, "sbk_") || len(value) > 128 {
		return false
	}
	identity, secret, ok := strings.Cut(strings.TrimPrefix(value, "sbk_"), ".")
	return ok && len(identity) == 16 && len(secret) == 43 && isBase64URLValue(identity) && isBase64URLValue(secret)
}

func isBase64URLValue(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validPasskeyCredential(credential *webauthn.Credential) bool {
	if credential == nil || len(credential.ID) == 0 || len(credential.ID) > 1024 {
		return false
	}
	body, err := json.Marshal(credential)
	return err == nil && len(body) <= 64<<10
}

func validCredentialSessionToken(token string) bool {
	return len(token) >= 16 && len(token) <= 256 && !strings.ContainsAny(token, "\r\n\x00")
}

func hasMFAFields(request wireRequest) bool {
	return request.MFAUserID != "" || request.MFAAccount != "" || request.MFACode != ""
}

func hasPasskeyFields(request wireRequest) bool {
	return request.PasskeyUserID != "" || request.PasskeyUsername != "" || request.PasskeyName != "" || request.PasskeyCredentialID != "" || request.PasskeyCredential != nil
}

func hasRemoteWebsiteFields(request wireRequest) bool {
	return request.RemoteWebsiteID != "" || request.RemoteWebsiteEndpoint != "" || request.RemoteWebsiteKey != "" || request.RemoteWebsiteLocale != ""
}

func hasProviderFields(request wireRequest) bool {
	return request.ProviderID != "" || request.ProviderName != "" || request.ProviderModel != "" || request.ProviderEndpoint != "" ||
		request.ProviderCredential != "" || request.ProviderShared || request.ProviderSessionHandle != ""
}

func isMFAOperation(operation string) bool {
	switch operation {
	case operationMFAStatus, operationMFABegin, operationMFAConfirm, operationMFAVerify, operationMFAReset:
		return true
	default:
		return false
	}
}

func isPasskeyOperation(operation string) bool {
	switch operation {
	case operationPasskeyUser, operationPasskeyList, operationPasskeyAdd, operationPasskeyUpdate, operationPasskeyDelete, operationPasskeyReset:
		return true
	default:
		return false
	}
}

func isRemoteWebsiteOperation(operation string) bool {
	switch operation {
	case operationRemoteWebsiteStore, operationRemoteWebsiteFetch, operationRemoteWebsiteDelete:
		return true
	default:
		return false
	}
}

func isProviderOperation(operation string) bool {
	switch operation {
	case operationProviderStore, operationProviderDelete, operationProviderStart, operationProviderStop:
		return true
	default:
		return false
	}
}

func isMySQLOperation(operation string) bool {
	switch operation {
	case operationMySQLStore, operationMySQLDelete, operationMySQLTest, operationMySQLDatabases, operationMySQLStatus,
		operationMySQLExists, operationMySQLCreate, operationMySQLReplace, operationMySQLDrop, operationMySQLDump,
		operationMySQLImport, operationMySQLSetTools, operationMySQLTestTools, operationMySQLCancel:
		return true
	default:
		return false
	}
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
