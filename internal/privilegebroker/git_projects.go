package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"scriptboard/internal/gitproject"
)

const operationGitProject = "git_project"
const gitProjectDeadline = 5 * time.Minute

func (server *Server) gitProjectOperation(request wireRequest) wireResponse {
	service, ok := server.hostFiles.(*brokerHostFilesService)
	if !ok || service.db == nil || service.gitProjects == nil {
		return wireResponse{Status: statusError, ErrorCode: "git_unavailable", Message: "Git project service is unavailable"}
	}
	payload := *request.HostFiles.GitProject
	reading := payload.Action == gitproject.Inspect || payload.Action == gitproject.Overview
	authMode := domainAuthorizationCurrentPrivileged
	if reading || payload.QuickID != "" {
		authMode = domainAuthorizationCurrentActor
	} else if payload.Action == gitproject.Force || payload.Action == gitproject.Discard {
		authMode = domainAuthorizationRecentPrivileged
	}

	body, _ := json.Marshal(payload)
	const revision = "git-project-v1"
	resource := "git-project:" + payload.ProjectID
	action := ActionHostFilesWrite
	if reading {
		action = ActionHostFilesRead
	}
	digest := parametersDigest(body)
	ctx, cancel := context.WithTimeout(context.Background(), gitProjectDeadline-10*time.Second)
	defer cancel()
	actor, err := server.authorizeActor(ctx, AuthorizationRequest{
		SessionToken: request.SessionToken, RequestID: request.RequestID, Resource: resource,
		Action: action, Revision: revision, ParametersSHA256: digest,
	}, authMode)
	if err != nil || (actor.Role != "administrator" && actor.Role != "maintainer" &&
		!(actor.Role == "operator" && (reading || payload.QuickID != ""))) {
		return server.operationFailureResponse(request, "authorization", "git_forbidden", "Git operation is not authorized", err)
	}
	if payload.QuickID != "" {
		setup, setupErr := gitproject.QuickSetup(ctx, service.db, payload.QuickID)
		if setupErr != nil || !slices.Contains(setup.Group, payload.ProjectID) && !slices.Contains(setup.Own, payload.ProjectID) {
			return server.operationFailureResponse(request, "authorization", "git_forbidden", "Git project is not configured for this Quick Run", setupErr)
		}
	}
	project, err := gitproject.Get(ctx, service.db, payload.ProjectID)
	if err != nil {
		return server.operationFailureResponse(request, "git_project", "git_unavailable", "Git project is unavailable", err)
	}
	record := AuditRecord{OccurredAt: server.now().UTC(), Actor: actor, RequestID: request.RequestID,
		Action: action, Resource: resource, Revision: revision, ParametersSHA256: digest, Result: "attempted"}
	if !reading && server.auditor != nil {
		if err := server.auditor.Record(ctx, record); err != nil {
			return server.operationFailureResponse(request, "audit", "audit_failed", "Git operation audit is unavailable", err)
		}
	}
	result, operationErr := service.gitProjects.Apply(ctx, project, payload)
	if !reading && server.auditor != nil {
		record.OccurredAt = server.now().UTC()
		record.Result = "succeeded"
		if operationErr != nil {
			record.Result = "failed"
		}
		auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer auditCancel()
		if err := server.auditor.Record(auditCtx, record); err != nil && operationErr == nil {
			operationErr = errors.New("Git operation executed but result audit failed")
		}
	}
	if operationErr != nil {
		return wireResponse{Status: statusError, ErrorCode: "git_failed", Message: operationErr.Error(),
			HostFiles: &hostFilesWireResponse{GitProject: &result}}
	}
	return wireResponse{Status: statusOK, HostFiles: &hostFilesWireResponse{GitProject: &result}}
}

func (backend *HostFilesBackend) GitProject(ctx context.Context, payload gitproject.Request) (gitproject.Result, error) {
	if backend == nil || backend.client == nil {
		return gitproject.Result{}, ErrHostFilesUnavailable
	}
	auth, ok := AuthorizationFromContext(ctx)
	if !ok {
		return gitproject.Result{}, errors.New("Git operation requires a local authenticated session")
	}
	response, err := backend.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operationGitProject, RequestID: auth.RequestID,
		SessionToken: auth.SessionToken, HostFiles: &hostFilesWireRequest{GitProject: &payload}})
	if response.HostFiles != nil && response.HostFiles.GitProject != nil {
		return *response.HostFiles.GitProject, err
	}
	if err == nil {
		err = errors.New("invalid Git project response")
	}
	return gitproject.Result{}, err
}
