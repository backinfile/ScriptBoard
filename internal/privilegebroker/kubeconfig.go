package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/kubeconfigmanager"
)

type kubeconfigWireRequest struct {
	Path               string `json:"path"`
	Context            string `json:"context,omitempty"`
	Name               string `json:"name,omitempty"`
	Cluster            string `json:"cluster,omitempty"`
	User               string `json:"user,omitempty"`
	Namespace          string `json:"namespace,omitempty"`
	Raw                []byte `json:"raw,omitempty"`
	UseImportedCurrent bool   `json:"use_imported_current,omitempty"`
}

type kubeconfigWireResponse struct {
	Exists   *bool                            `json:"exists,omitempty"`
	Snapshot *kubeconfigmanager.Snapshot      `json:"snapshot,omitempty"`
	Preview  *kubeconfigmanager.ImportPreview `json:"preview,omitempty"`
	Raw      []byte                           `json:"raw,omitempty"`
}

func isKubeconfigOperation(operation string) bool {
	switch operation {
	case operationKubeconfigExists, operationKubeconfigInspect, operationKubeconfigDownload, operationKubeconfigDownloadContext,
		operationKubeconfigPreviewImport, operationKubeconfigImport, operationKubeconfigUseContext,
		operationKubeconfigUpdateContext, operationKubeconfigRenameContext, operationKubeconfigDeleteContext:
		return true
	default:
		return false
	}
}

func kubeconfigMutation(operation string) bool {
	switch operation {
	case operationKubeconfigImport, operationKubeconfigUseContext, operationKubeconfigUpdateContext,
		operationKubeconfigRenameContext, operationKubeconfigDeleteContext:
		return true
	default:
		return false
	}
}

func validateKubeconfigRequest(request wireRequest) error {
	if request.Kubeconfig == nil || !isKubeconfigOperation(request.Operation) || !validCredentialSessionToken(request.SessionToken) ||
		request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
		request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) ||
		hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || request.HostFiles != nil ||
		request.StateBackup != nil || request.Registry != nil || request.Runtime != nil {
		return errors.New("kubeconfig request contains unrelated fields")
	}
	payload := request.Kubeconfig
	if !filepath.IsAbs(payload.Path) || len(payload.Path) > 4096 || strings.ContainsAny(payload.Path, "\r\n\x00") {
		return errors.New("kubeconfig path is invalid")
	}
	for _, value := range []string{payload.Context, payload.Name, payload.Cluster, payload.User, payload.Namespace} {
		if len(value) > 1024 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("kubeconfig request value is invalid")
		}
	}
	if len(payload.Raw) > kubeconfigmanager.MaxFileSize {
		return errors.New("kubeconfig upload is too large")
	}
	noEditFields := payload.Context == "" && payload.Name == "" && payload.Cluster == "" && payload.User == "" && payload.Namespace == ""
	switch request.Operation {
	case operationKubeconfigExists, operationKubeconfigInspect, operationKubeconfigDownload:
		if !noEditFields || len(payload.Raw) != 0 || payload.UseImportedCurrent {
			return errors.New("kubeconfig read request is invalid")
		}
	case operationKubeconfigDownloadContext, operationKubeconfigUseContext, operationKubeconfigDeleteContext:
		if strings.TrimSpace(payload.Context) == "" || payload.Name != "" || payload.Cluster != "" || payload.User != "" || payload.Namespace != "" || len(payload.Raw) != 0 || payload.UseImportedCurrent {
			return errors.New("kubeconfig context request is invalid")
		}
	case operationKubeconfigPreviewImport:
		if !noEditFields || len(payload.Raw) == 0 || payload.UseImportedCurrent {
			return errors.New("kubeconfig import preview request is invalid")
		}
	case operationKubeconfigImport:
		if !noEditFields || len(payload.Raw) == 0 {
			return errors.New("kubeconfig import request is invalid")
		}
	case operationKubeconfigUpdateContext:
		if strings.TrimSpace(payload.Context) == "" || payload.Name != "" || len(payload.Raw) != 0 || payload.UseImportedCurrent {
			return errors.New("kubeconfig context update request is invalid")
		}
	case operationKubeconfigRenameContext:
		if strings.TrimSpace(payload.Context) == "" || strings.TrimSpace(payload.Name) == "" || payload.Cluster != "" || payload.User != "" || payload.Namespace != "" || len(payload.Raw) != 0 || payload.UseImportedCurrent {
			return errors.New("kubeconfig context rename request is invalid")
		}
	}
	return nil
}

func (server *Server) kubeconfigOperation(request wireRequest) wireResponse {
	if server.kubeconfigs == nil {
		return wireResponse{Status: statusError, ErrorCode: "kubeconfig_unavailable", Message: "kubeconfig management is unavailable"}
	}
	payload := request.Kubeconfig
	body, _ := json.Marshal(payload)
	action := ActionKubeconfigRead
	mode := domainAuthorizationCurrentActor
	if kubeconfigMutation(request.Operation) {
		action = ActionKubeconfigWrite
		mode = domainAuthorizationCurrentPrivileged
	}
	authorization := AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID, Action: action,
		Resource: filepath.Clean(payload.Path), Revision: "kubeconfig-management-v1", ParametersSHA256: parametersDigest(body)}
	authorizeContext, cancelAuthorize := context.WithTimeout(context.Background(), 5*time.Second)
	actor, err := server.authorizeActor(authorizeContext, authorization, mode)
	cancelAuthorize()
	if err != nil || actor.Role != "administrator" && actor.Role != "maintainer" {
		return wireResponse{Status: statusError, ErrorCode: "authorization_denied", Message: "kubeconfig operation authorization denied"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response := &kubeconfigWireResponse{}
	switch request.Operation {
	case operationKubeconfigExists:
		var exists bool
		exists, err = server.kubeconfigs.Exists(ctx, payload.Path)
		response.Exists = &exists
	case operationKubeconfigInspect:
		var snapshot kubeconfigmanager.Snapshot
		snapshot, err = server.kubeconfigs.Inspect(ctx, payload.Path)
		if err == nil && snapshot.Exists {
			snapshot.Exportable, err = server.kubeconfigs.Exportable(ctx, payload.Path)
		}
		response.Snapshot = &snapshot
	case operationKubeconfigDownload:
		response.Raw, err = server.kubeconfigs.Download(ctx, payload.Path)
	case operationKubeconfigDownloadContext:
		response.Raw, err = server.kubeconfigs.DownloadContext(ctx, payload.Path, payload.Context)
	case operationKubeconfigPreviewImport:
		var preview kubeconfigmanager.ImportPreview
		preview, err = server.kubeconfigs.PreviewImport(ctx, payload.Path, payload.Raw)
		response.Preview = &preview
	case operationKubeconfigImport:
		var preview kubeconfigmanager.ImportPreview
		preview, err = server.kubeconfigs.Import(ctx, payload.Path, payload.Raw, payload.UseImportedCurrent)
		response.Preview = &preview
	case operationKubeconfigUseContext:
		err = server.kubeconfigs.UseContext(ctx, payload.Path, payload.Context)
	case operationKubeconfigUpdateContext:
		err = server.kubeconfigs.UpdateContext(ctx, payload.Path, payload.Context, payload.Cluster, payload.User, payload.Namespace)
	case operationKubeconfigRenameContext:
		err = server.kubeconfigs.RenameContext(ctx, payload.Path, payload.Context, payload.Name)
	case operationKubeconfigDeleteContext:
		err = server.kubeconfigs.DeleteContext(ctx, payload.Path, payload.Context)
	}
	if kubeconfigMutation(request.Operation) && server.auditor != nil {
		result := "succeeded"
		if err != nil {
			result = "failed"
		}
		auditErr := server.auditor.Record(context.Background(), AuditRecord{OccurredAt: server.now().UTC(), RequestID: request.RequestID,
			Actor: actor, Action: action, Resource: filepath.Clean(payload.Path), Revision: "kubeconfig-management-v1", ParametersSHA256: parametersDigest(body), Result: result})
		if auditErr != nil && err == nil {
			return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "kubeconfig operation completed but result audit failed"}
		}
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "kubeconfig_failed", Message: "kubeconfig operation failed"}
	}
	return wireResponse{Status: statusOK, Kubeconfig: response}
}

type RemoteKubeconfigManager struct{ client *Client }

func NewRemoteKubeconfigManager(client *Client) *RemoteKubeconfigManager {
	return &RemoteKubeconfigManager{client: client}
}

func (manager *RemoteKubeconfigManager) call(ctx context.Context, operation string, payload kubeconfigWireRequest) (*kubeconfigWireResponse, error) {
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok || authorization.SessionToken == "" || authorization.RequestID == "" {
		return nil, errors.New("kubeconfig operation requires an authenticated Broker context")
	}
	response, err := manager.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operation, RequestID: authorization.RequestID,
		SessionToken: authorization.SessionToken, Kubeconfig: &payload})
	if err != nil {
		return nil, err
	}
	if response.Kubeconfig == nil {
		return nil, errors.New("privileged Broker returned no kubeconfig response")
	}
	return response.Kubeconfig, nil
}

func (manager *RemoteKubeconfigManager) Inspect(ctx context.Context, path string) (kubeconfigmanager.Snapshot, error) {
	response, err := manager.call(ctx, operationKubeconfigInspect, kubeconfigWireRequest{Path: path})
	if err != nil {
		return kubeconfigmanager.Snapshot{}, err
	}
	if response.Snapshot == nil {
		return kubeconfigmanager.Snapshot{}, errors.New("privileged Broker returned no kubeconfig snapshot")
	}
	return *response.Snapshot, nil
}

func (manager *RemoteKubeconfigManager) Exists(ctx context.Context, path string) (bool, error) {
	response, err := manager.call(ctx, operationKubeconfigExists, kubeconfigWireRequest{Path: path})
	if err != nil {
		return false, err
	}
	if response.Exists == nil {
		return false, errors.New("privileged Broker returned no kubeconfig existence result")
	}
	return *response.Exists, nil
}

func (manager *RemoteKubeconfigManager) Download(ctx context.Context, path string) ([]byte, error) {
	response, err := manager.call(ctx, operationKubeconfigDownload, kubeconfigWireRequest{Path: path})
	if err != nil {
		return nil, err
	}
	return response.Raw, nil
}

func (manager *RemoteKubeconfigManager) DownloadContext(ctx context.Context, path, name string) ([]byte, error) {
	response, err := manager.call(ctx, operationKubeconfigDownloadContext, kubeconfigWireRequest{Path: path, Context: name})
	if err != nil {
		return nil, err
	}
	return response.Raw, nil
}

func (manager *RemoteKubeconfigManager) PreviewImport(ctx context.Context, path string, raw []byte) (kubeconfigmanager.ImportPreview, error) {
	response, err := manager.call(ctx, operationKubeconfigPreviewImport, kubeconfigWireRequest{Path: path, Raw: raw})
	if err != nil {
		return kubeconfigmanager.ImportPreview{}, err
	}
	if response.Preview == nil {
		return kubeconfigmanager.ImportPreview{}, errors.New("privileged Broker returned no kubeconfig import preview")
	}
	return *response.Preview, nil
}

func (manager *RemoteKubeconfigManager) Import(ctx context.Context, path string, raw []byte, useImportedCurrent bool) (kubeconfigmanager.ImportPreview, error) {
	response, err := manager.call(ctx, operationKubeconfigImport, kubeconfigWireRequest{Path: path, Raw: raw, UseImportedCurrent: useImportedCurrent})
	if err != nil {
		return kubeconfigmanager.ImportPreview{}, err
	}
	if response.Preview == nil {
		return kubeconfigmanager.ImportPreview{}, errors.New("privileged Broker returned no kubeconfig import result")
	}
	return *response.Preview, nil
}

func (manager *RemoteKubeconfigManager) UseContext(ctx context.Context, path, name string) error {
	_, err := manager.call(ctx, operationKubeconfigUseContext, kubeconfigWireRequest{Path: path, Context: name})
	return err
}

func (manager *RemoteKubeconfigManager) UpdateContext(ctx context.Context, path, name, cluster, user, namespace string) error {
	_, err := manager.call(ctx, operationKubeconfigUpdateContext, kubeconfigWireRequest{Path: path, Context: name, Cluster: cluster, User: user, Namespace: namespace})
	return err
}

func (manager *RemoteKubeconfigManager) RenameContext(ctx context.Context, path, oldName, newName string) error {
	_, err := manager.call(ctx, operationKubeconfigRenameContext, kubeconfigWireRequest{Path: path, Context: oldName, Name: newName})
	return err
}

func (manager *RemoteKubeconfigManager) DeleteContext(ctx context.Context, path, name string) error {
	_, err := manager.call(ctx, operationKubeconfigDeleteContext, kubeconfigWireRequest{Path: path, Context: name})
	return err
}

var _ kubeconfigmanager.Manager = (*RemoteKubeconfigManager)(nil)
