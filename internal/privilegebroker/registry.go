package privilegebroker

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"scriptboard/internal/registrymonitor"
)

type registryWireRequest struct {
	OperationID string                 `json:"operation_id,omitempty"`
	CardID      string                 `json:"card_id,omitempty"`
	Config      registrymonitor.Config `json:"config,omitempty"`
	Password    string                 `json:"password,omitempty"`
	Preserve    bool                   `json:"preserve,omitempty"`
	Endpoint    string                 `json:"endpoint,omitempty"`
}

type registryWireResponse struct {
	Configured         bool                          `json:"configured,omitempty"`
	Images             []registrymonitor.ImageResult `json:"images,omitempty"`
	InsecureConfigured bool                          `json:"insecure_configured,omitempty"`
	Changed            bool                          `json:"changed,omitempty"`
}

// RegistryConnections is the Web-side adapter for the Broker-owned Registry
// connection module. It never exposes a stored credential.
type RegistryConnections struct{ client *Client }

func NewRegistryConnections(client *Client) *RegistryConnections {
	return &RegistryConnections{client: client}
}

func (connections *RegistryConnections) Prepare(ctx context.Context, operationID, cardID string, config registrymonitor.Config, password string, preserve bool) error {
	_, err := connections.callAuthorized(ctx, operationRegistryPrepare, registryWireRequest{OperationID: operationID, CardID: cardID, Config: config, Password: password, Preserve: preserve})
	return err
}

func (connections *RegistryConnections) PrepareDelete(ctx context.Context, operationID, cardID string) error {
	_, err := connections.callAuthorized(ctx, operationRegistryPrepareDelete, registryWireRequest{OperationID: operationID, CardID: cardID})
	return err
}

func (connections *RegistryConnections) Commit(ctx context.Context, operationID string) error {
	_, err := connections.callPeer(ctx, operationRegistryCommit, registryWireRequest{OperationID: operationID})
	return err
}

func (connections *RegistryConnections) Acknowledge(ctx context.Context, operationID string) error {
	_, err := connections.callPeer(ctx, operationRegistryAcknowledge, registryWireRequest{OperationID: operationID})
	return err
}

func (connections *RegistryConnections) Abort(ctx context.Context, operationID string) error {
	_, err := connections.callPeer(ctx, operationRegistryAbort, registryWireRequest{OperationID: operationID})
	return err
}

func (connections *RegistryConnections) Configured(ctx context.Context, cardID string) (bool, error) {
	response, err := connections.callPeer(ctx, operationRegistryConfigured, registryWireRequest{CardID: cardID})
	return response.Configured, err
}

func (connections *RegistryConnections) Inspect(ctx context.Context, cardID string) ([]registrymonitor.ImageResult, error) {
	response, err := connections.callPeer(ctx, operationRegistryInspect, registryWireRequest{CardID: cardID})
	return response.Images, err
}

func (connections *RegistryConnections) Test(ctx context.Context, cardID string, config registrymonitor.Config, password string, preserve bool) ([]registrymonitor.ImageResult, error) {
	response, err := connections.callAuthorized(ctx, operationRegistryTest, registryWireRequest{CardID: cardID, Config: config, Password: password, Preserve: preserve})
	return response.Images, err
}

func (connections *RegistryConnections) InsecureConfigured(ctx context.Context, endpoint string) (bool, error) {
	response, err := connections.callPeer(ctx, operationRegistryInsecureConfigured, registryWireRequest{Endpoint: endpoint})
	return response.InsecureConfigured, err
}

func (connections *RegistryConnections) RegisterInsecure(ctx context.Context, endpoint string) (bool, error) {
	response, err := connections.callAuthorized(ctx, operationRegistryRegisterInsecure, registryWireRequest{Endpoint: endpoint})
	return response.Changed, err
}

func (connections *RegistryConnections) callAuthorized(ctx context.Context, operation string, payload registryWireRequest) (registryWireResponse, error) {
	if connections == nil || connections.client == nil {
		return registryWireResponse{}, errors.New("privileged Broker Registry service is unavailable")
	}
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return registryWireResponse{}, errors.New("privileged Broker Registry authorization is missing")
	}
	response, err := connections.client.call(ctx, wireRequest{
		Version: ProtocolVersion, Operation: operation, RequestID: authorization.RequestID,
		SessionToken: authorization.SessionToken, Registry: &payload,
	})
	if response.Registry == nil {
		return registryWireResponse{}, err
	}
	return *response.Registry, err
}

func (connections *RegistryConnections) callPeer(ctx context.Context, operation string, payload registryWireRequest) (registryWireResponse, error) {
	if connections == nil || connections.client == nil {
		return registryWireResponse{}, errors.New("privileged Broker Registry service is unavailable")
	}
	requestID, err := registryRequestID()
	if err != nil {
		return registryWireResponse{}, err
	}
	response, err := connections.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operation, RequestID: requestID, Registry: &payload})
	if response.Registry == nil {
		return registryWireResponse{}, err
	}
	return *response.Registry, err
}

func registryRequestID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "registry:" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (server *Server) registryOperation(request wireRequest) wireResponse {
	if server.registry == nil {
		return wireResponse{Status: statusError, ErrorCode: "registry_unavailable", Message: "Registry service is unavailable"}
	}
	payload := request.Registry
	if payload == nil {
		return wireResponse{Status: statusError, ErrorCode: "request_invalid", Message: "Registry request is missing"}
	}
	if request.Operation == operationRegistryPrepare || request.Operation == operationRegistryPrepareDelete || request.Operation == operationRegistryTest || request.Operation == operationRegistryRegisterInsecure {
		action := ActionRegistryStore
		resource := payload.CardID
		if request.Operation == operationRegistryPrepareDelete {
			action = ActionRegistryDelete
		}
		if request.Operation == operationRegistryTest {
			action, resource = ActionRegistryInspect, "registry-connection-test"
		}
		if request.Operation == operationRegistryRegisterInsecure {
			action, resource = ActionRegistryDockerConfigure, payload.Endpoint
		}
		parameters, _ := json.Marshal(payload)
		// Registry connection edits follow normal ManageOperations authorization;
		// only changing Docker's insecure Registry configuration needs recent step-up.
		mode := domainAuthorizationCurrentPrivileged
		if request.Operation == operationRegistryRegisterInsecure {
			mode = domainAuthorizationRecentAdministrator
		}
		mutation, response := server.authorizeDomainOperation(request, action, resource, "registry-connection-v1", parameters, mode)
		if response.Status != "" {
			return response
		}
		result := server.executeRegistryOperation(request.Operation, *payload)
		if result.Status == statusOK && mutation != nil {
			if err := server.recordCredentialMutation(*mutation, "succeeded"); err != nil {
				return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "Registry operation completed but result audit failed"}
			}
		} else if mutation != nil {
			_ = server.recordCredentialMutation(*mutation, "failed")
		}
		return result
	}
	return server.executeRegistryOperation(request.Operation, *payload)
}

func (server *Server) executeRegistryOperation(operation string, payload registryWireRequest) wireResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	response := wireResponse{Status: statusOK, Registry: &registryWireResponse{}}
	var err error
	switch operation {
	case operationRegistryPrepare:
		err = server.registry.Prepare(ctx, payload.OperationID, payload.CardID, payload.Config, payload.Password, payload.Preserve)
	case operationRegistryPrepareDelete:
		err = server.registry.PrepareDelete(ctx, payload.OperationID, payload.CardID)
	case operationRegistryCommit:
		err = server.registry.Commit(ctx, payload.OperationID)
	case operationRegistryAcknowledge:
		err = server.registry.Acknowledge(ctx, payload.OperationID)
	case operationRegistryAbort:
		err = server.registry.Abort(ctx, payload.OperationID)
	case operationRegistryConfigured:
		response.Registry.Configured, err = server.registry.Configured(ctx, payload.CardID)
	case operationRegistryInspect:
		response.Registry.Images, err = server.registry.Inspect(ctx, payload.CardID)
	case operationRegistryTest:
		response.Registry.Images, err = server.registry.Test(ctx, payload.CardID, payload.Config, payload.Password, payload.Preserve)
	case operationRegistryInsecureConfigured:
		response.Registry.InsecureConfigured, err = server.registry.InsecureConfigured(ctx, payload.Endpoint)
	case operationRegistryRegisterInsecure:
		response.Registry.Changed, err = server.registry.RegisterInsecure(ctx, payload.Endpoint)
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "registry_failed", Message: "Registry operation failed"}
	}
	return response
}

func validateRegistryRequest(request wireRequest) error {
	if request.Registry == nil || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" ||
		len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || request.HostFiles != nil || request.StateBackup != nil {
		return errors.New("Registry request is invalid")
	}
	payload := request.Registry
	if len(payload.OperationID) > 160 || len(payload.CardID) > 160 || len(payload.Endpoint) > 512 || strings.ContainsAny(payload.OperationID+payload.CardID+payload.Password+payload.Endpoint, "\r\n\x00") || len(payload.Password) > 8<<10 {
		return errors.New("Registry request contains invalid fields")
	}
	requiresAuthorization := request.Operation == operationRegistryPrepare || request.Operation == operationRegistryPrepareDelete || request.Operation == operationRegistryTest || request.Operation == operationRegistryRegisterInsecure
	if requiresAuthorization && !validCredentialSessionToken(request.SessionToken) || !requiresAuthorization && request.SessionToken != "" {
		return errors.New("Registry request authorization is invalid")
	}
	switch request.Operation {
	case operationRegistryPrepare:
		if payload.OperationID == "" || payload.CardID == "" || payload.Endpoint != "" || registrymonitor.ValidateConfig(payload.Config) != nil {
			return errors.New("Registry prepare request is invalid")
		}
	case operationRegistryPrepareDelete:
		if payload.OperationID == "" || payload.CardID == "" || payload.Password != "" || payload.Preserve || payload.Endpoint != "" || payload.Config.Endpoint != "" || len(payload.Config.Images) != 0 {
			return errors.New("Registry delete request is invalid")
		}
	case operationRegistryCommit, operationRegistryAcknowledge, operationRegistryAbort:
		if payload.OperationID == "" || payload.CardID != "" || payload.Password != "" || payload.Preserve || payload.Endpoint != "" || payload.Config.Endpoint != "" || len(payload.Config.Images) != 0 {
			return errors.New("Registry completion request is invalid")
		}
	case operationRegistryConfigured, operationRegistryInspect:
		if payload.OperationID != "" || payload.CardID == "" || payload.Password != "" || payload.Preserve || payload.Endpoint != "" || payload.Config.Endpoint != "" || len(payload.Config.Images) != 0 {
			return errors.New("Registry query request is invalid")
		}
	case operationRegistryTest:
		if payload.OperationID != "" || payload.Endpoint != "" || payload.Preserve && payload.CardID == "" || !payload.Preserve && payload.CardID != "" || registrymonitor.ValidateConfig(payload.Config) != nil {
			return errors.New("Registry test request is invalid")
		}
	case operationRegistryInsecureConfigured, operationRegistryRegisterInsecure:
		if payload.OperationID != "" || payload.CardID != "" || payload.Password != "" || payload.Preserve || payload.Endpoint == "" || payload.Config.Endpoint != "" || len(payload.Config.Images) != 0 {
			return errors.New("Registry insecure configuration request is invalid")
		}
	}
	return nil
}
