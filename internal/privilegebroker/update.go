package privilegebroker

import (
	"context"
	"errors"
	"regexp"
	"time"

	"scriptboard/internal/identity"
	updatepkg "scriptboard/internal/update"
)

const (
	operationUpdateVerify  = "update_verify"
	operationUpdatePrepare = "update_prepare"
	operationUpdateHandoff = "update_handoff"
	updatePrepareDeadline  = 10 * time.Minute
)

var updateOperationID = regexp.MustCompile("^[-_A-Za-z0-9]{20,64}$")

type RemoteUpdates struct {
	client *Client
}

func NewRemoteUpdates(client *Client) *RemoteUpdates {
	return &RemoteUpdates{client: client}
}

func (remote *RemoteUpdates) Verify(ctx context.Context) error {
	_, err := remote.call(ctx, operationUpdateVerify, "", "")
	return err
}

func (remote *RemoteUpdates) Prepare(ctx context.Context, token string) (updatepkg.Operation, error) {
	return remote.mutate(ctx, operationUpdatePrepare, "", token)
}

func (remote *RemoteUpdates) Handoff(ctx context.Context, id, token string) (updatepkg.Operation, error) {
	return remote.mutate(ctx, operationUpdateHandoff, id, token)
}

func (remote *RemoteUpdates) mutate(ctx context.Context, operation, id, token string) (updatepkg.Operation, error) {
	response, err := remote.call(ctx, operation, id, token)
	if err != nil {
		return updatepkg.Operation{}, err
	}
	if response.Update == nil {
		return updatepkg.Operation{}, errors.New("privileged Broker did not return an update operation")
	}
	return *response.Update, nil
}

func (remote *RemoteUpdates) call(ctx context.Context, operation, id, token string) (wireResponse, error) {
	requestID, err := updatepkg.NewOperationID()
	if err != nil {
		return wireResponse{}, err
	}
	return remote.client.call(ctx, wireRequest{
		Version: ProtocolVersion, Operation: operation, RequestID: requestID, Resource: id, SessionToken: token,
	})
}

func validateUpdateRequest(request wireRequest) error {
	// The update interface accepts a session and a generated operation ID. All
	// executable, data and download paths are resolved by the Broker.
	if request.Capability != "" || request.Action != "" || request.Revision != "" ||
		request.ParametersSHA256 != "" || len(request.Parameters) != 0 ||
		hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) ||
		request.MySQL != nil || request.Redis != nil || request.HostFiles != nil ||
		request.StateBackup != nil || request.Registry != nil {
		return errors.New("update request contains unrelated fields")
	}
	switch request.Operation {
	case operationUpdateVerify:
		if request.SessionToken == "" && request.Resource == "" {
			return nil
		}
	case operationUpdatePrepare:
		if validCredentialSessionToken(request.SessionToken) && request.Resource == "" {
			return nil
		}
	case operationUpdateHandoff:
		if validCredentialSessionToken(request.SessionToken) && updateOperationID.MatchString(request.Resource) {
			return nil
		}
	}
	return errors.New("update request is invalid")
}

func (server *Server) updateOperation(ctx context.Context, request wireRequest) wireResponse {
	if server.updates == nil {
		return wireResponse{Status: statusError, ErrorCode: "update_unavailable", Message: "managed update service is unavailable"}
	}
	if request.Operation == operationUpdateVerify {
		if err := server.updates.VerifyManagedService(); err != nil {
			return server.operationFailureResponse(request, "updates", "update_verify_failed", redactedErrorCause(err), err)
		}
		return wireResponse{Status: statusOK}
	}
	mode := domainAuthorizationCurrentPrivileged
	if request.Operation == operationUpdateHandoff {
		mode = domainAuthorizationRecentPrivileged
	}
	actor, err := server.authorizeActor(ctx, AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID}, mode)
	if err == nil && !identity.Allows(identity.Role(actor.Role), identity.PermissionManageSystem) {
		err = errors.New("role cannot manage system updates")
	}
	if err != nil {
		return server.operationFailureResponse(request, "updates", "authorization_failed", "session is not authorized for this update operation", err)
	}
	var operation updatepkg.Operation
	switch request.Operation {
	case operationUpdatePrepare:
		operation, err = server.updates.Prepare(ctx)
	case operationUpdateHandoff:
		operation, err = server.updates.HandoffFromBroker(request.Resource)
	}
	if err != nil {
		return server.operationFailureResponse(request, "updates", "update_failed", redactedErrorCause(err), err)
	}
	return wireResponse{Status: statusOK, Update: &operation}
}
