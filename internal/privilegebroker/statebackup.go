package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/statebackup"
)

const maximumStateBackupPassphraseBytes = 4096

type stateBackupWireRequest struct {
	Destination     string `json:"destination,omitempty"`
	ArchivePath     string `json:"archive_path,omitempty"`
	ConfirmBackupID string `json:"confirm_backup_id,omitempty"`
	StageID         string `json:"stage_id,omitempty"`
	Passphrase      []byte `json:"passphrase,omitempty"`
}

type stateBackupWireResponse struct {
	Artifact *statebackup.Artifact `json:"artifact,omitempty"`
	Manifest *statebackup.Manifest `json:"manifest,omitempty"`
	Stage    *statebackup.Stage    `json:"stage,omitempty"`
	Stages   []statebackup.Stage   `json:"stages,omitempty"`
}

type StateBackups struct {
	client *Client
}

func NewStateBackups(client *Client) *StateBackups { return &StateBackups{client: client} }

func (backups *StateBackups) Create(ctx context.Context, destination string, passphrase []byte) (statebackup.Artifact, error) {
	response, err := backups.call(ctx, wireRequest{Operation: operationStateBackupCreate, StateBackup: &stateBackupWireRequest{Destination: destination, Passphrase: passphrase}})
	if err != nil {
		return statebackup.Artifact{}, err
	}
	if response.StateBackup == nil || response.StateBackup.Artifact == nil || response.StateBackup.Artifact.Manifest.ID == "" {
		return statebackup.Artifact{}, errors.New("privileged Broker returned an invalid state backup artifact")
	}
	response.StateBackup.Artifact.Manifest = publicBackupManifest(response.StateBackup.Artifact.Manifest)
	return *response.StateBackup.Artifact, nil
}

func (backups *StateBackups) Inspect(ctx context.Context, archivePath string, passphrase []byte) (statebackup.Manifest, error) {
	response, err := backups.call(ctx, wireRequest{Operation: operationStateBackupInspect, StateBackup: &stateBackupWireRequest{ArchivePath: archivePath, Passphrase: passphrase}})
	if err != nil {
		return statebackup.Manifest{}, err
	}
	if response.StateBackup == nil || response.StateBackup.Manifest == nil || response.StateBackup.Manifest.ID == "" {
		return statebackup.Manifest{}, errors.New("privileged Broker returned an invalid state backup manifest")
	}
	*response.StateBackup.Manifest = publicBackupManifest(*response.StateBackup.Manifest)
	return *response.StateBackup.Manifest, nil
}

func (backups *StateBackups) Stage(ctx context.Context, archivePath string, passphrase []byte, confirmBackupID string) (statebackup.Stage, error) {
	response, err := backups.call(ctx, wireRequest{Operation: operationStateBackupStage, StateBackup: &stateBackupWireRequest{ArchivePath: archivePath, Passphrase: passphrase, ConfirmBackupID: confirmBackupID}})
	if err != nil {
		return statebackup.Stage{}, err
	}
	if response.StateBackup == nil || response.StateBackup.Stage == nil || response.StateBackup.Stage.ID == "" {
		return statebackup.Stage{}, errors.New("privileged Broker returned an invalid state restore stage")
	}
	response.StateBackup.Stage.Manifest = publicBackupManifest(response.StateBackup.Stage.Manifest)
	return *response.StateBackup.Stage, nil
}

func (backups *StateBackups) List(ctx context.Context) ([]statebackup.Stage, error) {
	response, err := backups.call(ctx, wireRequest{Operation: operationStateBackupList, StateBackup: &stateBackupWireRequest{}})
	if err != nil {
		return nil, err
	}
	if response.StateBackup == nil {
		return nil, errors.New("privileged Broker returned an invalid state restore stage list")
	}
	for index := range response.StateBackup.Stages {
		response.StateBackup.Stages[index].Manifest = publicBackupManifest(response.StateBackup.Stages[index].Manifest)
	}
	return append([]statebackup.Stage(nil), response.StateBackup.Stages...), nil
}

func (backups *StateBackups) Discard(ctx context.Context, stageID string) error {
	_, err := backups.call(ctx, wireRequest{Operation: operationStateBackupDiscard, StateBackup: &stateBackupWireRequest{StageID: stageID}})
	return err
}

func (backups *StateBackups) call(ctx context.Context, request wireRequest) (wireResponse, error) {
	if backups == nil || backups.client == nil {
		return wireResponse{}, errors.New("privileged Broker state backup service is unavailable")
	}
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return wireResponse{}, errors.New("privileged Broker state backup authorization is missing")
	}
	request.Version, request.RequestID, request.SessionToken = ProtocolVersion, authorization.RequestID, authorization.SessionToken
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	return backups.client.call(ctx, request)
}

func (server *Server) stateBackupOperation(request wireRequest) wireResponse {
	if server.stateBackups == nil || request.StateBackup == nil {
		return wireResponse{Status: statusError, ErrorCode: "state_backup_unavailable", Message: "state backup service is unavailable"}
	}
	backupRequest := request.StateBackup
	defer clearTransientBytes(backupRequest.Passphrase)
	if request.Operation == operationStateBackupList {
		if _, err := server.authorizeStateBackupRead(request); err != nil {
			server.logOperationFailure(request, "authorization", "authorization_denied", err)
			return wireResponse{Status: statusError, ErrorCode: "authorization_denied", Message: "state backup authorization denied"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		stages, err := server.stateBackups.List(ctx)
		cancel()
		if err != nil {
			return server.operationFailureResponse(request, "state_backup", "state_backup_failed", "state backup stage list failed", err)
		}
		for index := range stages {
			stages[index].Manifest = publicBackupManifest(stages[index].Manifest)
		}
		return wireResponse{Status: statusOK, StateBackup: &stateBackupWireResponse{Stages: stages}}
	}
	action, resource, safeParameters := stateBackupMutationBinding(request.Operation, *backupRequest)
	mutation, denied := server.authorizeDomainOperation(request, action, resource, "state-backup-v1", safeParameters, domainAuthorizationRecentPrivileged)
	if mutation == nil {
		return denied
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	response := wireResponse{Status: statusOK, StateBackup: &stateBackupWireResponse{}}
	var err error
	switch request.Operation {
	case operationStateBackupCreate:
		var artifact statebackup.Artifact
		artifact, err = server.stateBackups.Create(ctx, backupRequest.Destination, backupRequest.Passphrase)
		artifact.Manifest = publicBackupManifest(artifact.Manifest)
		response.StateBackup.Artifact = &artifact
	case operationStateBackupInspect:
		var manifest statebackup.Manifest
		manifest, err = server.stateBackups.Inspect(ctx, backupRequest.ArchivePath, backupRequest.Passphrase)
		manifest = publicBackupManifest(manifest)
		response.StateBackup.Manifest = &manifest
	case operationStateBackupStage:
		var stage statebackup.Stage
		stage, err = server.stateBackups.Stage(ctx, backupRequest.ArchivePath, backupRequest.Passphrase, backupRequest.ConfirmBackupID)
		stage.Manifest = publicBackupManifest(stage.Manifest)
		response.StateBackup.Stage = &stage
	case operationStateBackupDiscard:
		err = server.stateBackups.Discard(ctx, backupRequest.StageID)
	}
	result := "succeeded"
	if err != nil {
		result = "failed"
	}
	if auditErr := server.recordCredentialMutation(*mutation, result); auditErr != nil {
		if err == nil {
			return server.operationFailureResponse(request, "audit", "audit_failed_after_execution", "state backup operation completed but result audit failed", auditErr)
		}
		server.logOperationFailure(request, "audit", "audit_failed_after_execution", auditErr)
	}
	if err != nil {
		return server.operationFailureResponse(request, "state_backup", "state_backup_failed", "state backup operation failed", err)
	}
	return response
}

func (server *Server) authorizeStateBackupRead(request wireRequest) (Actor, error) {
	authorizer, ok := server.authorizer.(SessionAuthorizer)
	if !ok {
		return Actor{}, errors.New("state backup session authorizer is unavailable")
	}
	actor, err := authorizer.AuthorizeSession(context.Background(), AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID, Action: ActionStateBackupInspect, Resource: "stages", Revision: "state-backup-v1"})
	if err != nil || actor.Role != "administrator" && actor.Role != "maintainer" {
		return Actor{}, errors.New("state backup session is not authorized")
	}
	return actor, nil
}

func stateBackupMutationBinding(operation string, request stateBackupWireRequest) (Action, string, []byte) {
	var action Action
	resource := request.ArchivePath
	parameters := struct {
		Destination     string `json:"destination,omitempty"`
		ArchivePath     string `json:"archive_path,omitempty"`
		ConfirmBackupID string `json:"confirm_backup_id,omitempty"`
		StageID         string `json:"stage_id,omitempty"`
	}{request.Destination, request.ArchivePath, request.ConfirmBackupID, request.StageID}
	switch operation {
	case operationStateBackupCreate:
		action, resource = ActionStateBackupCreate, request.Destination
	case operationStateBackupInspect:
		action = ActionStateBackupInspect
	case operationStateBackupStage:
		action = ActionStateBackupStage
	case operationStateBackupDiscard:
		action, resource = ActionStateBackupDiscard, request.StageID
	}
	body, _ := json.Marshal(parameters)
	return action, resource, body
}

func validateStateBackupRequest(request wireRequest) error {
	if request.StateBackup == nil || !validCredentialSessionToken(request.SessionToken) || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" || len(request.Parameters) != 0 ||
		hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || request.MySQL != nil || request.Redis != nil || request.HostFiles != nil {
		return errors.New("state backup request is invalid")
	}
	payload := request.StateBackup
	validPath := func(value string) bool {
		return value != "" && len(value) <= 32<<10 && filepath.IsAbs(value) && !strings.ContainsAny(value, "\r\n\x00")
	}
	validPassphrase := len(payload.Passphrase) >= 16 && len(payload.Passphrase) <= maximumStateBackupPassphraseBytes
	switch request.Operation {
	case operationStateBackupCreate:
		if !validPath(payload.Destination) || payload.ArchivePath != "" || payload.ConfirmBackupID != "" || payload.StageID != "" || !validPassphrase {
			return errors.New("state backup create request is invalid")
		}
	case operationStateBackupInspect:
		if payload.Destination != "" || !validPath(payload.ArchivePath) || payload.ConfirmBackupID != "" || payload.StageID != "" || !validPassphrase {
			return errors.New("state backup inspect request is invalid")
		}
	case operationStateBackupStage:
		if payload.Destination != "" || !validPath(payload.ArchivePath) || payload.StageID != "" || !validPassphrase || !validBackupIdentifier(payload.ConfirmBackupID) {
			return errors.New("state backup stage request is invalid")
		}
	case operationStateBackupList:
		if payload.Destination != "" || payload.ArchivePath != "" || payload.ConfirmBackupID != "" || payload.StageID != "" || len(payload.Passphrase) != 0 {
			return errors.New("state backup list request is invalid")
		}
	case operationStateBackupDiscard:
		if payload.Destination != "" || payload.ArchivePath != "" || payload.ConfirmBackupID != "" || !validBackupIdentifier(payload.StageID) || len(payload.Passphrase) != 0 {
			return errors.New("state backup discard request is invalid")
		}
	}
	return nil
}

func validBackupIdentifier(value string) bool {
	return len(value) == 24 && isBase64URLValue(value)
}

func publicBackupManifest(manifest statebackup.Manifest) statebackup.Manifest {
	manifest.AuditCheckpoint = nil
	return manifest
}

func clearTransientBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
