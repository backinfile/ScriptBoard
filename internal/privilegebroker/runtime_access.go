package privilegebroker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/logstream"
)

type ApplicationService interface {
	appstatus.Probe
	appstatus.RuntimeDetailProbe
	appstatus.ContainerOperator
	appstatus.LogProbe
	Close() error
}

type KubernetesService interface {
	clusterstatus.Factory
	ResolveConnection(context.Context, string) (clusterstatus.Connection, bool, error)
	OpenCandidate(context.Context, clusterstatus.Connection) (clusterstatus.Client, error)
}

type runtimeWireRequest struct {
	Detail              *appstatus.DetailRequest  `json:"detail,omitempty"`
	ContainerName       string                    `json:"container_name,omitempty"`
	ContainerAction     appstatus.ContainerAction `json:"container_action,omitempty"`
	Log                 *appstatus.LogRequest     `json:"log,omitempty"`
	Cursor              string                    `json:"cursor,omitempty"`
	Kubernetes          *clusterstatus.Connection `json:"kubernetes,omitempty"`
	WorkloadKey         string                    `json:"workload_key,omitempty"`
	LogLimit            int                       `json:"log_limit,omitempty"`
	KubernetesOperation *clusterstatus.Operation  `json:"kubernetes_operation,omitempty"`
}

type runtimeWireResponse struct {
	Snapshot           *appstatus.RawSnapshot      `json:"snapshot,omitempty"`
	Detail             *appstatus.RuntimeDetail    `json:"detail,omitempty"`
	Metadata           *logstream.Metadata         `json:"metadata,omitempty"`
	Page               *logstream.Page             `json:"page,omitempty"`
	Events             []logstream.Event           `json:"events,omitempty"`
	Capabilities       *clusterstatus.Capabilities `json:"capabilities,omitempty"`
	Fingerprint        string                      `json:"fingerprint,omitempty"`
	KubernetesSnapshot *clusterstatus.Snapshot     `json:"kubernetes_snapshot,omitempty"`
	KubernetesDetail   *clusterstatus.Detail       `json:"kubernetes_detail,omitempty"`
	KubernetesLogs     []clusterstatus.LogLine     `json:"kubernetes_logs,omitempty"`
}

func isRuntimeOperation(operation string) bool {
	switch operation {
	case operationApplicationSnapshot, operationApplicationDetail,
		operationApplicationLogOpen, operationApplicationLogHistory, operationApplicationLogFollow,
		operationKubernetesOpen, operationKubernetesSnapshot, operationKubernetesDetail, operationKubernetesLogs:
		return true
	default:
		return false
	}
}

func validateRuntimeRequest(request wireRequest) error {
	if !isRuntimeOperation(request.Operation) || request.Runtime == nil || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" || len(request.Parameters) != 0 ||
		hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || request.MySQL != nil || request.Redis != nil || request.HostFiles != nil || request.StateBackup != nil || request.Registry != nil || request.Kubeconfig != nil {
		return errors.New("runtime request contains unrelated fields")
	}
	if strings.HasPrefix(request.Operation, "application_") && (request.Runtime.Kubernetes != nil || request.Runtime.KubernetesOperation != nil || request.Runtime.WorkloadKey != "" || request.Runtime.LogLimit != 0) {
		return errors.New("application runtime request contains Kubernetes fields")
	}
	switch request.Operation {
	case operationApplicationSnapshot:
		if request.SessionToken != "" || request.Runtime.Detail != nil || request.Runtime.ContainerName != "" || request.Runtime.ContainerAction != "" || request.Runtime.Log != nil || request.Runtime.Cursor != "" {
			return errors.New("application snapshot request is invalid")
		}
	case operationApplicationDetail:
		if request.SessionToken != "" || request.Runtime.Detail == nil || request.Runtime.ContainerName != "" || request.Runtime.ContainerAction != "" || request.Runtime.Log != nil || request.Runtime.Cursor != "" {
			return errors.New("application detail request is invalid")
		}
	case operationApplicationLogOpen:
		if request.SessionToken != "" || request.Runtime.Detail != nil || request.Runtime.ContainerName != "" || request.Runtime.ContainerAction != "" || request.Runtime.Log == nil || request.Runtime.Cursor != "" {
			return errors.New("application log-open request is invalid")
		}
	case operationApplicationLogHistory, operationApplicationLogFollow:
		if request.SessionToken != "" || request.Runtime.Detail != nil || request.Runtime.ContainerName != "" || request.Runtime.ContainerAction != "" || request.Runtime.Log == nil || len(request.Runtime.Cursor) > 2048 || strings.ContainsAny(request.Runtime.Cursor, "\r\n\x00") {
			return errors.New("application log-read request is invalid")
		}
	case operationKubernetesOpen, operationKubernetesSnapshot:
		if request.Operation != operationKubernetesOpen && request.SessionToken != "" || request.Operation == operationKubernetesOpen && request.SessionToken != "" && !validCredentialSessionToken(request.SessionToken) || request.Runtime.Detail != nil || request.Runtime.ContainerName != "" || request.Runtime.ContainerAction != "" || request.Runtime.Log != nil || request.Runtime.Cursor != "" || !validKubernetesConnection(request.Runtime.Kubernetes) {
			return errors.New("Kubernetes runtime request is invalid")
		}
		if request.Runtime.WorkloadKey != "" || request.Runtime.LogLimit != 0 || request.Runtime.KubernetesOperation != nil {
			return errors.New("Kubernetes runtime request contains workload fields")
		}
	case operationKubernetesDetail:
		if !validKubernetesReadRequest(request, false) {
			return errors.New("Kubernetes detail request is invalid")
		}
	case operationKubernetesLogs:
		if !validKubernetesReadRequest(request, true) {
			return errors.New("Kubernetes logs request is invalid")
		}
	}
	return nil
}

func (server *Server) runtimeOperation(request wireRequest) wireResponse {
	if strings.HasPrefix(request.Operation, "application_") && server.applications == nil {
		return wireResponse{Status: statusError, ErrorCode: "application_probe_unavailable", Message: "application probe is unavailable"}
	}
	timeout := 10 * time.Second
	if strings.HasPrefix(request.Operation, "kubernetes_") {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	switch request.Operation {
	case operationApplicationSnapshot:
		snapshot := server.applications.Snapshot(ctx)
		return wireResponse{Status: statusOK, Runtime: &runtimeWireResponse{Snapshot: &snapshot}}
	case operationApplicationDetail:
		detail := server.applications.RuntimeDetail(ctx, *request.Runtime.Detail)
		return wireResponse{Status: statusOK, Runtime: &runtimeWireResponse{Detail: &detail}}
	case operationApplicationLogOpen, operationApplicationLogHistory, operationApplicationLogFollow:
		return server.applicationLogOperation(ctx, request)
	case operationKubernetesOpen, operationKubernetesSnapshot, operationKubernetesDetail, operationKubernetesLogs:
		return server.kubernetesOperation(ctx, request)
	default:
		return wireResponse{Status: statusError, ErrorCode: "operation_forbidden", Message: "runtime operation is not supported"}
	}
}

func validKubernetesReadRequest(request wireRequest, logs bool) bool {
	if request.SessionToken != "" || request.Runtime.Detail != nil || request.Runtime.ContainerName != "" || request.Runtime.ContainerAction != "" || request.Runtime.Log != nil || request.Runtime.Cursor != "" || request.Runtime.KubernetesOperation != nil || !validKubernetesConnection(request.Runtime.Kubernetes) {
		return false
	}
	if request.Runtime.WorkloadKey == "" || len(request.Runtime.WorkloadKey) > 1024 || strings.ContainsAny(request.Runtime.WorkloadKey, "\r\n\x00") {
		return false
	}
	if logs {
		return request.Runtime.LogLimit > 0 && request.Runtime.LogLimit <= 1000
	}
	return request.Runtime.LogLimit == 0
}

func validKubernetesOperation(connection *clusterstatus.Connection, operation *clusterstatus.Operation) bool {
	if !validKubernetesConnection(connection) || connection.Mode != clusterstatus.ModeLimited || operation == nil {
		return false
	}
	if operation.WorkloadKey == "" || len(operation.WorkloadKey) > 1024 || strings.ContainsAny(operation.WorkloadKey, "\r\n\x00") {
		return false
	}
	switch operation.Kind {
	case clusterstatus.OperationScale:
		return operation.Replicas >= 0 && operation.Replicas <= 1000
	case clusterstatus.OperationRedeploy, clusterstatus.OperationRunCron:
		return operation.Replicas == 0
	default:
		return false
	}
}

func validKubernetesConnection(connection *clusterstatus.Connection) bool {
	if connection == nil || len(connection.ID) > 160 || strings.TrimSpace(connection.Name) == "" || len(connection.Name) > 160 || strings.ContainsAny(connection.ID+connection.Name+connection.Context+connection.KubeconfigPath, "\r\n\x00") {
		return false
	}
	if connection.KubeconfigPath == "" || len(connection.KubeconfigPath) > 4096 || !filepath.IsAbs(connection.KubeconfigPath) || len(connection.Context) > 253 {
		return false
	}
	return connection.Mode == "" || connection.Mode == clusterstatus.ModeObserve || connection.Mode == clusterstatus.ModeLimited
}

func sameKubernetesConnection(left, right clusterstatus.Connection) bool {
	return left.ID == right.ID && left.Name == right.Name && left.KubeconfigPath == right.KubeconfigPath && left.Context == right.Context && left.Mode == right.Mode
}

func (server *Server) resolveKubernetesConnection(ctx context.Context, request wireRequest) (clusterstatus.Connection, bool, wireResponse) {
	requested := *request.Runtime.Kubernetes
	if requested.ID != "" {
		configured, found, err := server.kubernetes.ResolveConnection(ctx, requested.ID)
		if err != nil {
			return clusterstatus.Connection{}, false, server.operationFailureResponse(request, "kubernetes", "kubernetes_connection_unavailable", "Kubernetes connection could not be resolved", err)
		}
		if found && sameKubernetesConnection(configured, requested) {
			return configured, false, wireResponse{}
		}
	}
	if request.Operation != operationKubernetesOpen || request.SessionToken == "" {
		return clusterstatus.Connection{}, false, wireResponse{Status: statusError, ErrorCode: "kubernetes_connection_forbidden", Message: "Kubernetes connection is not configured"}
	}
	sessions, ok := server.authorizer.(SessionAuthorizer)
	if !ok {
		return clusterstatus.Connection{}, false, wireResponse{Status: statusError, ErrorCode: "authorization_unavailable", Message: "session authorization is unavailable"}
	}
	body, _ := json.Marshal(requested)
	actor, err := sessions.AuthorizeSession(ctx, AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID,
		Action: Action("kubernetes_connect"), Resource: requested.ID, Revision: "kubernetes-connection-v1", ParametersSHA256: parametersDigest(body)})
	if err != nil || actor.Role != "administrator" && actor.Role != "maintainer" {
		if err != nil {
			server.logOperationFailure(request, "authorization", "operation_forbidden", err)
		}
		return clusterstatus.Connection{}, false, wireResponse{Status: statusError, ErrorCode: "operation_forbidden", Message: "Kubernetes connection is not authorized"}
	}
	return requested, true, wireResponse{}
}

func decodeRuntimeMutation(parameters json.RawMessage) (runtimeWireRequest, error) {
	if err := rejectDuplicateJSONKeys(parameters); err != nil {
		return runtimeWireRequest{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(parameters)))
	decoder.DisallowUnknownFields()
	var payload runtimeWireRequest
	if err := decoder.Decode(&payload); err != nil {
		return runtimeWireRequest{}, err
	}
	return payload, nil
}

func (server *Server) executeRuntimeMutation(ctx context.Context, request ExecutionRequest) error {
	payload, err := decodeRuntimeMutation(request.Parameters)
	if err != nil {
		return errors.New("runtime mutation parameters are invalid")
	}
	switch request.Action {
	case ActionApplicationOperate:
		validAction := payload.ContainerAction == appstatus.ContainerStart || payload.ContainerAction == appstatus.ContainerStop || payload.ContainerAction == appstatus.ContainerRestart
		if server.applications == nil || request.Revision != "application-runtime-v1" || payload.ContainerName != request.Resource || strings.TrimSpace(payload.ContainerName) == "" || len(payload.ContainerName) > 255 || strings.ContainsAny(payload.ContainerName, "\r\n\x00") || !validAction || payload.Detail != nil || payload.Log != nil || payload.Cursor != "" || payload.Kubernetes != nil || payload.KubernetesOperation != nil || payload.WorkloadKey != "" || payload.LogLimit != 0 {
			return errors.New("container operation parameters are invalid")
		}
		return server.applications.OperateContainer(ctx, payload.ContainerName, payload.ContainerAction)
	case ActionKubernetesOperate:
		if server.kubernetes == nil || request.Revision != "kubernetes-runtime-v1" || request.Resource == "" || payload.KubernetesOperation == nil || payload.KubernetesOperation.WorkloadKey != request.Resource || payload.Detail != nil || payload.ContainerName != "" || payload.ContainerAction != "" || payload.Log != nil || payload.Cursor != "" || payload.WorkloadKey != "" || payload.LogLimit != 0 || !validKubernetesOperation(payload.Kubernetes, payload.KubernetesOperation) {
			return errors.New("Kubernetes operation parameters are invalid")
		}
		configured, found, err := server.kubernetes.ResolveConnection(ctx, payload.Kubernetes.ID)
		if err != nil || !found || !sameKubernetesConnection(configured, *payload.Kubernetes) {
			return errors.New("Kubernetes connection is not configured")
		}
		client, err := server.kubernetes.Open(ctx, configured)
		if err != nil {
			return errors.New("Kubernetes connection could not be opened")
		}
		defer client.Close()
		return validateAndOperateKubernetes(ctx, client, *payload.KubernetesOperation)
	default:
		return errors.New("runtime mutation is unsupported")
	}
}

func validateAndOperateKubernetes(ctx context.Context, client clusterstatus.Client, operation clusterstatus.Operation) error {
	capabilities, err := client.Capabilities(ctx)
	if err != nil {
		return err
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return err
	}
	var selected clusterstatus.Workload
	for _, workload := range snapshot.Workloads {
		if workload.Key == operation.WorkloadKey {
			selected = workload
			break
		}
	}
	if selected.Key == "" {
		return errors.New("Kubernetes workload was not found")
	}
	switch operation.Kind {
	case clusterstatus.OperationRedeploy:
		if !capabilities.Redeploy || selected.Kind == "CronJob" {
			return errors.New("Kubernetes workload cannot be redeployed")
		}
	case clusterstatus.OperationScale:
		difference := operation.Replicas - selected.Desired
		if !capabilities.Scale || selected.Kind != "Deployment" && selected.Kind != "StatefulSet" || difference != 1 && difference != -1 {
			return errors.New("Kubernetes workload cannot be scaled")
		}
	case clusterstatus.OperationRunCron:
		if !capabilities.RunCron || selected.Kind != "CronJob" {
			return errors.New("Kubernetes workload is not a CronJob")
		}
	default:
		return errors.New("Kubernetes operation is unsupported")
	}
	return client.Operate(ctx, operation)
}

func (server *Server) kubernetesOperation(ctx context.Context, request wireRequest) wireResponse {
	if server.kubernetes == nil {
		return wireResponse{Status: statusError, ErrorCode: "kubernetes_unavailable", Message: "Kubernetes runtime is unavailable"}
	}
	connection, candidate, resolutionResponse := server.resolveKubernetesConnection(ctx, request)
	if resolutionResponse.Status != "" {
		return resolutionResponse
	}
	var client clusterstatus.Client
	var err error
	if candidate {
		client, err = server.kubernetes.OpenCandidate(ctx, connection)
	} else {
		client, err = server.kubernetes.Open(ctx, connection)
	}
	if err != nil {
		server.logKubernetesFailure(request, connection, candidate, "open", err)
		return wireResponse{Status: statusError, ErrorCode: "kubernetes_open_failed", Message: kubernetesOpenFailureMessage(err)}
	}
	defer client.Close()
	response := &runtimeWireResponse{}
	switch request.Operation {
	case operationKubernetesOpen:
		capabilities, capabilityErr := client.Capabilities(ctx)
		err = capabilityErr
		response.Capabilities = &capabilities
		response.Fingerprint = client.Fingerprint()
	case operationKubernetesSnapshot:
		snapshot, snapshotErr := client.Snapshot(ctx)
		err = snapshotErr
		response.KubernetesSnapshot = &snapshot
	case operationKubernetesDetail:
		detail, detailErr := client.Detail(ctx, request.Runtime.WorkloadKey)
		err = detailErr
		response.KubernetesDetail = &detail
	case operationKubernetesLogs:
		response.KubernetesLogs, err = client.Logs(ctx, request.Runtime.WorkloadKey, request.Runtime.LogLimit)
	}
	if err != nil {
		// Preserve the redacted API cause so an unchanged connection that fails
		// capability revalidation can be diagnosed from both the UI and Broker log.
		cause := server.logKubernetesFailure(request, connection, candidate, "operation", err)
		return wireResponse{Status: statusError, ErrorCode: "kubernetes_operation_failed", Message: "Kubernetes operation failed: " + cause}
	}
	return wireResponse{Status: statusOK, Runtime: response}
}

func (server *Server) logKubernetesFailure(request wireRequest, connection clusterstatus.Connection, candidate bool, stage string, err error) string {
	cause := redactedErrorCause(err)
	server.errorLogger.Printf("privileged Broker Kubernetes request failed request_id=%q operation=%q stage=%q connection_id=%q connection_name=%q context=%q candidate=%t error=%q",
		request.RequestID, request.Operation, stage, connection.ID, connection.Name, connection.Context, candidate, cause)
	return cause
}

func kubernetesOpenFailureMessage(err error) string {
	var openError *clusterstatus.KubeconfigOpenError
	if !errors.As(err, &openError) {
		return "Kubernetes connection could not be opened: verify that the kubeconfig uses embedded, supported credentials"
	}
	switch openError.Kind {
	case clusterstatus.KubeconfigRequiresEmbeddedCA:
		return "Kubernetes connection could not be opened: new connections must embed certificate authority data"
	case clusterstatus.KubeconfigRequiresEmbeddedAuth:
		return "Kubernetes connection could not be opened: new connections must embed token, client certificate, and key data"
	case clusterstatus.KubeconfigUnsupportedAuth:
		return "Kubernetes connection could not be opened: exec and auth-provider kubeconfig credentials are not supported"
	case clusterstatus.KubeconfigNoSelectedContext:
		return "Kubernetes connection could not be opened: the kubeconfig has no selected context"
	case clusterstatus.KubeconfigContextNotFound:
		return "Kubernetes connection could not be opened: the selected context was not found"
	case clusterstatus.KubeconfigInvalidServer:
		return "Kubernetes connection could not be opened: the server must be an absolute HTTP or HTTPS URL"
	case clusterstatus.KubeconfigUnsupportedCredentials:
		return "Kubernetes connection could not be opened: the selected context has no supported credentials"
	case clusterstatus.KubeconfigInvalid:
		return "Kubernetes connection could not be opened: the kubeconfig is invalid"
	case clusterstatus.KubeconfigInvalidTLSMaterial:
		return "Kubernetes connection could not be opened: certificate authority, client certificate, or client key data is invalid"
	case clusterstatus.KubeconfigUnreadable:
		return "Kubernetes connection could not be opened: the privileged Broker cannot read the kubeconfig path"
	default:
		return "Kubernetes connection could not be opened: verify that the kubeconfig uses embedded, supported credentials"
	}
}

var errRuntimeLogBatchFull = errors.New("runtime log batch is full")

func (server *Server) applicationLogOperation(ctx context.Context, request wireRequest) wireResponse {
	source, err := server.applications.LogSource(ctx, *request.Runtime.Log)
	if err != nil {
		if errors.Is(err, appstatus.ErrDockerLogContainerNotFound) {
			return wireResponse{Status: statusError, ErrorCode: "application_log_not_found", Message: "application log source is no longer available"}
		}
		if errors.Is(err, appstatus.ErrApplicationLogsUnsupported) {
			return wireResponse{Status: statusError, ErrorCode: "application_log_unsupported", Message: "application log source is unsupported"}
		}
		return server.operationFailureResponse(request, "application_log", "application_log_unavailable", "application log source is unavailable", err)
	}
	response := &runtimeWireResponse{}
	switch request.Operation {
	case operationApplicationLogOpen:
		metadata := source.Metadata()
		response.Metadata = &metadata
	case operationApplicationLogHistory:
		page, historyErr := source.History(ctx, request.Runtime.Cursor)
		err = historyErr
		response.Page = &page
	case operationApplicationLogFollow:
		followContext, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		encodedBytes := 0
		err = source.Follow(followContext, request.Runtime.Cursor, func(event logstream.Event) error {
			encoded, encodeErr := json.Marshal(event)
			if encodeErr != nil {
				return encodeErr
			}
			// Reserve space for the response envelope and JSON separators.
			if len(response.Events) >= 512 || encodedBytes+len(encoded)+len(response.Events) > logstream.DefaultPageBytes-4096 {
				return errRuntimeLogBatchFull
			}
			encodedBytes += len(encoded)
			response.Events = append(response.Events, event)
			return nil
		})
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, errRuntimeLogBatchFull) {
			err = nil
		}
	}
	if err != nil {
		if errors.Is(err, logstream.ErrInvalidCursor) {
			return wireResponse{Status: statusError, ErrorCode: "application_log_invalid_cursor", Message: "application log cursor is invalid"}
		}
		if errors.Is(err, appstatus.ErrDockerLogContainerNotFound) {
			return wireResponse{Status: statusError, ErrorCode: "application_log_not_found", Message: "application log source is no longer available"}
		}
		return server.operationFailureResponse(request, "application_log", "application_log_failed", "application log operation failed", err)
	}
	return wireResponse{Status: statusOK, Runtime: response}
}

type ApplicationProbe struct {
	client *Client
}

func NewApplicationProbe(client *Client) *ApplicationProbe {
	return &ApplicationProbe{client: client}
}

func (probe *ApplicationProbe) Snapshot(ctx context.Context) appstatus.RawSnapshot {
	response, err := probe.call(ctx, operationApplicationSnapshot, &runtimeWireRequest{})
	if err != nil || response.Snapshot == nil {
		if err == nil {
			err = errors.New("privileged Broker returned no application snapshot")
		}
		return appstatus.RawSnapshot{CollectedAt: time.Now().UTC(), Errors: map[string]string{"host": err.Error(), "docker": err.Error()}}
	}
	return *response.Snapshot
}

func (probe *ApplicationProbe) RuntimeDetail(ctx context.Context, request appstatus.DetailRequest) appstatus.RuntimeDetail {
	response, err := probe.call(ctx, operationApplicationDetail, &runtimeWireRequest{Detail: &request})
	if err != nil || response.Detail == nil {
		if err == nil {
			err = errors.New("privileged Broker returned no application detail")
		}
		return appstatus.RuntimeDetail{State: appstatus.RuntimeUnavailable, Code: "broker_unavailable", Message: err.Error(), Kind: request.Application.Kind}
	}
	return *response.Detail
}

func (probe *ApplicationProbe) OperateContainer(ctx context.Context, name string, action appstatus.ContainerAction) error {
	parameters, _ := json.Marshal(runtimeWireRequest{ContainerName: name, ContainerAction: action})
	return probe.client.Invoke(ctx, ActionApplicationOperate, name, "application-runtime-v1", parameters)
}

func (probe *ApplicationProbe) LogSource(ctx context.Context, request appstatus.LogRequest) (logstream.Source, error) {
	response, err := probe.call(ctx, operationApplicationLogOpen, &runtimeWireRequest{Log: &request})
	if err != nil {
		return nil, err
	}
	if response.Metadata == nil {
		return nil, errors.New("privileged Broker returned no application log metadata")
	}
	return &remoteApplicationLogSource{probe: probe, request: request, metadata: *response.Metadata}, nil
}

type remoteApplicationLogSource struct {
	probe    *ApplicationProbe
	request  appstatus.LogRequest
	metadata logstream.Metadata
}

type KubernetesFactory struct {
	client *Client
}

func NewKubernetesFactory(client *Client) KubernetesFactory {
	return KubernetesFactory{client: client}
}

func (factory KubernetesFactory) Open(ctx context.Context, connection clusterstatus.Connection) (clusterstatus.Client, error) {
	client := &remoteKubernetesClient{broker: factory.client, connection: connection}
	response, err := client.call(ctx, operationKubernetesOpen)
	if err != nil {
		return nil, err
	}
	if response.Capabilities == nil || response.Fingerprint == "" {
		return nil, errors.New("privileged Broker returned incomplete Kubernetes connection metadata")
	}
	client.capabilities = *response.Capabilities
	client.fingerprint = response.Fingerprint
	return client, nil
}

type remoteKubernetesClient struct {
	broker       *Client
	connection   clusterstatus.Connection
	capabilities clusterstatus.Capabilities
	fingerprint  string
}

func (client *remoteKubernetesClient) Close() error { return nil }

func (client *remoteKubernetesClient) Capabilities(context.Context) (clusterstatus.Capabilities, error) {
	return client.capabilities, nil
}

func (client *remoteKubernetesClient) Fingerprint() string { return client.fingerprint }

func (client *remoteKubernetesClient) Snapshot(ctx context.Context) (clusterstatus.Snapshot, error) {
	response, err := client.call(ctx, operationKubernetesSnapshot)
	if err != nil {
		return clusterstatus.Snapshot{}, err
	}
	if response.KubernetesSnapshot == nil {
		return clusterstatus.Snapshot{}, errors.New("privileged Broker returned no Kubernetes snapshot")
	}
	return *response.KubernetesSnapshot, nil
}

func (client *remoteKubernetesClient) Detail(ctx context.Context, key string) (clusterstatus.Detail, error) {
	response, err := client.callWith(ctx, operationKubernetesDetail, &runtimeWireRequest{Kubernetes: &client.connection, WorkloadKey: key})
	if err != nil {
		return clusterstatus.Detail{}, err
	}
	if response.KubernetesDetail == nil {
		return clusterstatus.Detail{}, errors.New("privileged Broker returned no Kubernetes detail")
	}
	return *response.KubernetesDetail, nil
}

func (client *remoteKubernetesClient) Logs(ctx context.Context, key string, limit int) ([]clusterstatus.LogLine, error) {
	response, err := client.callWith(ctx, operationKubernetesLogs, &runtimeWireRequest{Kubernetes: &client.connection, WorkloadKey: key, LogLimit: limit})
	if err != nil {
		return nil, err
	}
	return response.KubernetesLogs, nil
}

func (client *remoteKubernetesClient) Operate(ctx context.Context, operation clusterstatus.Operation) error {
	parameters, _ := json.Marshal(runtimeWireRequest{Kubernetes: &client.connection, KubernetesOperation: &operation})
	return client.broker.Invoke(ctx, ActionKubernetesOperate, operation.WorkloadKey, "kubernetes-runtime-v1", parameters)
}

func (client *remoteKubernetesClient) call(ctx context.Context, operation string) (*runtimeWireResponse, error) {
	return client.callWith(ctx, operation, &runtimeWireRequest{Kubernetes: &client.connection})
}

func (client *remoteKubernetesClient) callWith(ctx context.Context, operation string, payload *runtimeWireRequest) (*runtimeWireResponse, error) {
	requestID, err := runtimeRequestID("kubernetes")
	if err != nil {
		return nil, err
	}
	request := wireRequest{Version: ProtocolVersion, Operation: operation, RequestID: requestID, Runtime: payload}
	if operation == operationKubernetesOpen {
		if authorization, ok := AuthorizationFromContext(ctx); ok {
			request.RequestID = authorization.RequestID
			request.SessionToken = authorization.SessionToken
		}
	}
	response, err := client.broker.call(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.Runtime == nil {
		return nil, errors.New("privileged Broker returned no Kubernetes response")
	}
	return response.Runtime, nil
}

func (source *remoteApplicationLogSource) Metadata() logstream.Metadata { return source.metadata }

func (source *remoteApplicationLogSource) History(ctx context.Context, before string) (logstream.Page, error) {
	response, err := source.probe.call(ctx, operationApplicationLogHistory, &runtimeWireRequest{Log: &source.request, Cursor: before})
	if err != nil {
		return logstream.Page{}, err
	}
	if response.Page == nil {
		return logstream.Page{}, errors.New("privileged Broker returned no application log page")
	}
	return *response.Page, nil
}

func (source *remoteApplicationLogSource) Follow(ctx context.Context, after string, emit func(logstream.Event) error) error {
	liveEmitted := false
	for ctx.Err() == nil {
		response, err := source.probe.call(ctx, operationApplicationLogFollow, &runtimeWireRequest{Log: &source.request, Cursor: after})
		if err != nil {
			return err
		}
		for _, event := range response.Events {
			if event.Kind == logstream.EventState && event.State == "live" {
				if liveEmitted {
					continue
				}
				liveEmitted = true
			}
			if event.Entry != nil && event.Entry.Cursor != "" {
				after = event.Entry.Cursor
			}
			if err := emit(event); err != nil {
				return err
			}
			if event.Kind == logstream.EventComplete {
				return nil
			}
		}
	}
	return ctx.Err()
}

func (probe *ApplicationProbe) call(ctx context.Context, operation string, payload *runtimeWireRequest) (*runtimeWireResponse, error) {
	requestID, err := runtimeRequestID("applications")
	if err != nil {
		return nil, err
	}
	response, err := probe.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operation, RequestID: requestID, Runtime: payload})
	if err != nil {
		switch response.ErrorCode {
		case "application_log_invalid_cursor":
			return nil, logstream.ErrInvalidCursor
		case "application_log_not_found":
			return nil, appstatus.ErrDockerLogContainerNotFound
		case "application_log_unsupported":
			return nil, appstatus.ErrApplicationLogsUnsupported
		default:
			return nil, err
		}
	}
	if response.Runtime == nil {
		return nil, errors.New("privileged Broker returned no runtime response")
	}
	return response.Runtime, nil
}

func runtimeRequestID(domain string) (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("runtime:%s:%x", domain, raw), nil
}

var _ appstatus.Probe = (*ApplicationProbe)(nil)
var _ appstatus.RuntimeDetailProbe = (*ApplicationProbe)(nil)
var _ appstatus.ContainerOperator = (*ApplicationProbe)(nil)
var _ appstatus.LogProbe = (*ApplicationProbe)(nil)
var _ logstream.Source = (*remoteApplicationLogSource)(nil)
var _ clusterstatus.Factory = KubernetesFactory{}
var _ clusterstatus.Client = (*remoteKubernetesClient)(nil)
