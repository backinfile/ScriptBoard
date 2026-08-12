package privilegebroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/hostfiles"
)

const hostFilesPageSize = 1000

var ErrHostFilesUnavailable = errors.New("privileged Broker Host Files service is unavailable")

type hostFilesCanonicalKind string

const (
	hostFilesCanonicalExisting    hostFilesCanonicalKind = "existing"
	hostFilesCanonicalDirectory   hostFilesCanonicalKind = "directory"
	hostFilesCanonicalDestination hostFilesCanonicalKind = "destination"
	hostFilesCanonicalChild       hostFilesCanonicalKind = "child"
)

type HostFileInfo struct {
	Name       string      `json:"name"`
	Size       int64       `json:"size"`
	Mode       os.FileMode `json:"mode"`
	ModifiedAt time.Time   `json:"modified_at"`
	Directory  bool        `json:"directory"`
	CanMutate  bool        `json:"can_mutate"`
}

type hostFilesWireRequest struct {
	Path             string                 `json:"path,omitempty"`
	Directory        string                 `json:"directory,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Destination      string                 `json:"destination,omitempty"`
	StoredPath       string                 `json:"stored_path,omitempty"`
	StoredName       string                 `json:"stored_name,omitempty"`
	CanonicalKind    hostFilesCanonicalKind `json:"canonical_kind,omitempty"`
	RestoreAvailable bool                   `json:"restore_available,omitempty"`
	MaxBytes         int64                  `json:"max_bytes,omitempty"`
	Offset           int                    `json:"offset,omitempty"`
	Limit            int                    `json:"limit,omitempty"`
}

type hostFilesWireResponse struct {
	Entries       []hostfiles.Entry       `json:"entries,omitempty"`
	Info          *HostFileInfo           `json:"info,omitempty"`
	Document      *hostfiles.TextDocument `json:"document,omitempty"`
	CanonicalPath string                  `json:"canonical_path,omitempty"`
	AvailableName string                  `json:"available_name,omitempty"`
	Execute       *bool                   `json:"execute,omitempty"`
	Trashed       *hostfiles.Trashed      `json:"trashed,omitempty"`
	NextOffset    int                     `json:"next_offset,omitempty"`
}

type brokerHostFilesService struct{ files *hostfiles.Manager }

func NewBrokerHostFilesService(files *hostfiles.Manager) (HostFilesService, error) {
	if files == nil {
		return nil, errors.New("Broker Host Files manager is required")
	}
	return &brokerHostFilesService{files: files}, nil
}

func (service *brokerHostFilesService) Roots(context.Context) ([]hostfiles.Entry, error) {
	return service.files.Roots()
}

func (service *brokerHostFilesService) List(_ context.Context, path string) ([]hostfiles.Entry, error) {
	return service.files.List(path)
}

func (service *brokerHostFilesService) Info(_ context.Context, path string) (HostFileInfo, error) {
	info, err := service.files.Info(path)
	if err != nil {
		return HostFileInfo{}, err
	}
	return HostFileInfo{Name: info.Name(), Size: info.Size(), Mode: info.Mode(), ModifiedAt: info.ModTime(), Directory: info.IsDir(), CanMutate: service.files.CanMutate(path)}, nil
}

func (service *brokerHostFilesService) ReadText(_ context.Context, path string, maxBytes int64) (hostfiles.TextDocument, error) {
	return service.files.ReadText(path, maxBytes)
}

func (service *brokerHostFilesService) Canonical(_ context.Context, kind hostFilesCanonicalKind, path, name string) (string, error) {
	switch kind {
	case hostFilesCanonicalExisting:
		return service.files.CanonicalExisting(path)
	case hostFilesCanonicalDirectory:
		return service.files.CanonicalDirectory(path)
	case hostFilesCanonicalDestination:
		return service.files.CanonicalDestination(path)
	case hostFilesCanonicalChild:
		return service.files.Destination(path, name)
	default:
		return "", errors.New("unknown Host Files canonicalization operation")
	}
}

func (service *brokerHostFilesService) AvailableName(_ context.Context, directory, name string) (string, error) {
	return service.files.AvailableName(directory, name)
}

func (service *brokerHostFilesService) CreateDirectory(_ context.Context, directory, name string) error {
	return service.files.CreateDirectory(directory, name)
}

func (service *brokerHostFilesService) ToggleOwnerExecute(_ context.Context, path string) (bool, error) {
	return service.files.ToggleOwnerExecute(path)
}

func (service *brokerHostFilesService) MoveToTrash(_ context.Context, path, storedName string) (hostfiles.Trashed, error) {
	return service.files.MoveToTrash(path, storedName)
}

func (service *brokerHostFilesService) RestoreFromTrash(_ context.Context, storedPath, original string, available bool) (string, error) {
	if available {
		return service.files.RestoreFromTrashToAvailablePath(storedPath, original)
	}
	return original, service.files.RestoreFromTrash(storedPath, original)
}

func (service *brokerHostFilesService) PurgeTrash(_ context.Context, storedPath string) error {
	return service.files.PurgeTrash(storedPath)
}

func (service *brokerHostFilesService) Move(_ context.Context, source, destination string) error {
	return service.files.Move(source, destination)
}

func (server *Server) hostFilesOperation(request wireRequest) wireResponse {
	if server.hostFiles == nil {
		return wireResponse{Status: statusError, ErrorCode: "host_files_unavailable", Message: "Host Files service is unavailable"}
	}
	actor, action, err := server.authorizeHostFilesOperation(request)
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "host_files_forbidden", Message: "Host Files operation is not authorized"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	payload := request.HostFiles
	result := &hostFilesWireResponse{}
	response := wireResponse{Status: statusOK, HostFiles: result}
	switch request.Operation {
	case operationHostFilesRoots:
		result.Entries, err = server.hostFiles.Roots(ctx)
	case operationHostFilesList:
		var entries []hostfiles.Entry
		entries, err = server.hostFiles.List(ctx, payload.Path)
		if err == nil {
			start := min(payload.Offset, len(entries))
			end := min(start+payload.Limit, len(entries))
			result.Entries = entries[start:end]
			if end < len(entries) {
				result.NextOffset = end
			}
		}
	case operationHostFilesInfo:
		var value HostFileInfo
		value, err = server.hostFiles.Info(ctx, payload.Path)
		result.Info = &value
	case operationHostFilesReadText:
		var value hostfiles.TextDocument
		value, err = server.hostFiles.ReadText(ctx, payload.Path, payload.MaxBytes)
		result.Document = &value
	case operationHostFilesCanonical:
		result.CanonicalPath, err = server.hostFiles.Canonical(ctx, payload.CanonicalKind, payload.Path, payload.Name)
	case operationHostFilesAvailable:
		result.AvailableName, err = server.hostFiles.AvailableName(ctx, payload.Directory, payload.Name)
	case operationHostFilesMkdir:
		err = server.hostFiles.CreateDirectory(ctx, payload.Directory, payload.Name)
	case operationHostFilesToggleExec:
		var enabled bool
		enabled, err = server.hostFiles.ToggleOwnerExecute(ctx, payload.Path)
		result.Execute = &enabled
	case operationHostFilesTrash:
		var value hostfiles.Trashed
		value, err = server.hostFiles.MoveToTrash(ctx, payload.Path, payload.StoredName)
		result.Trashed = &value
	case operationHostFilesRestore:
		result.CanonicalPath, err = server.hostFiles.RestoreFromTrash(ctx, payload.StoredPath, payload.Destination, payload.RestoreAvailable)
	case operationHostFilesPurge:
		err = server.hostFiles.PurgeTrash(ctx, payload.StoredPath)
	case operationHostFilesMove:
		err = server.hostFiles.Move(ctx, payload.Path, payload.Destination)
	}
	mutation := action != ActionHostFilesRead
	if mutation && server.auditor != nil {
		body, _ := json.Marshal(payload)
		resultText := "succeeded"
		if err != nil {
			resultText = "failed"
		}
		auditErr := server.auditor.Record(context.Background(), AuditRecord{OccurredAt: server.now().UTC(), RequestID: request.RequestID,
			Actor: actor, Action: action, Resource: hostFilesResource(payload), Revision: "host-files-v1", ParametersSHA256: parametersDigest(body), Result: resultText})
		if auditErr != nil && err == nil {
			return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "Host Files operation completed but result audit failed"}
		}
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "host_files_failed", Message: "Host Files operation failed"}
	}
	return response
}

func (server *Server) authorizeHostFilesOperation(request wireRequest) (Actor, Action, error) {
	payload := request.HostFiles
	action, recent := hostFilesAction(request.Operation)
	body, _ := json.Marshal(payload)
	authorization := AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID, Action: action,
		Resource: hostFilesResource(payload), Revision: "host-files-v1", ParametersSHA256: parametersDigest(body)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var actor Actor
	var err error
	if recent {
		actor, err = server.authorizer.Authorize(ctx, authorization)
	} else if sessions, ok := server.authorizer.(SessionAuthorizer); ok {
		actor, err = sessions.AuthorizeSession(ctx, authorization)
	} else {
		err = errors.New("session authorization is unavailable")
	}
	if err != nil {
		return Actor{}, action, err
	}
	if action == ActionHostFilesRead {
		if actor.Role != "administrator" && actor.Role != "maintainer" && actor.Role != "operator" {
			return Actor{}, action, errors.New("role cannot read Host Files")
		}
	} else if actor.Role != "administrator" && actor.Role != "maintainer" {
		return Actor{}, action, errors.New("role cannot mutate Host Files")
	}
	return actor, action, nil
}

func hostFilesAction(operation string) (Action, bool) {
	switch operation {
	case operationHostFilesTrash, operationHostFilesRestore, operationHostFilesPurge:
		return ActionHostFilesDelete, true
	case operationHostFilesMove, operationHostFilesToggleExec:
		return ActionHostFilesMove, true
	case operationHostFilesMkdir:
		return ActionHostFilesWrite, false
	default:
		return ActionHostFilesRead, false
	}
}

func hostFilesResource(payload *hostFilesWireRequest) string {
	for _, value := range []string{payload.Path, payload.StoredPath, payload.Directory, payload.Destination} {
		if value != "" {
			return value
		}
	}
	return "host-files-roots"
}

func validateHostFilesRequest(request wireRequest) error {
	if request.HostFiles == nil || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
		request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || !validCredentialSessionToken(request.SessionToken) {
		return errors.New("Host Files request contains unrelated fields")
	}
	payload := request.HostFiles
	for _, value := range []string{payload.Path, payload.Directory, payload.Destination, payload.StoredPath} {
		if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("Host Files path is invalid")
		}
	}
	for _, value := range []string{payload.Name, payload.StoredName} {
		if len(value) > 255 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("Host Files name is invalid")
		}
	}
	emptyPaths := func() bool {
		return payload.Path == "" && payload.Directory == "" && payload.Name == "" && payload.Destination == "" && payload.StoredPath == "" && payload.StoredName == "" && payload.CanonicalKind == "" && !payload.RestoreAvailable && payload.MaxBytes == 0
	}
	switch request.Operation {
	case operationHostFilesRoots:
		if !emptyPaths() || payload.Offset != 0 || payload.Limit != 0 {
			return errors.New("Host Files roots request is invalid")
		}
	case operationHostFilesList:
		if payload.Path != "" && !filepath.IsAbs(payload.Path) || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.RestoreAvailable || payload.MaxBytes != 0 || payload.Offset < 0 || payload.Limit != hostFilesPageSize {
			return errors.New("Host Files list request is invalid")
		}
	case operationHostFilesInfo:
		if !onlyHostFilePath(payload) {
			return errors.New("Host Files info request is invalid")
		}
	case operationHostFilesReadText:
		if payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.MaxBytes <= 0 || payload.MaxBytes > 1<<20 || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.RestoreAvailable || payload.Offset != 0 || payload.Limit != 0 {
			return errors.New("Host Files text request is invalid")
		}
	case operationHostFilesCanonical:
		validKind := payload.CanonicalKind == hostFilesCanonicalExisting || payload.CanonicalKind == hostFilesCanonicalDirectory || payload.CanonicalKind == hostFilesCanonicalDestination || payload.CanonicalKind == hostFilesCanonicalChild
		if !validKind || payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.Directory != "" || payload.Destination != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.RestoreAvailable || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 || (payload.CanonicalKind == hostFilesCanonicalChild) != (payload.Name != "") {
			return errors.New("Host Files canonical request is invalid")
		}
	case operationHostFilesAvailable, operationHostFilesMkdir:
		if payload.Directory == "" || !filepath.IsAbs(payload.Directory) || payload.Name == "" || payload.Path != "" || payload.Destination != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.RestoreAvailable || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 {
			return errors.New("Host Files directory request is invalid")
		}
	case operationHostFilesToggleExec:
		if !onlyHostFilePath(payload) {
			return errors.New("Host Files execute-bit request is invalid")
		}
	case operationHostFilesTrash:
		if payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.StoredName == "" || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredPath != "" || payload.CanonicalKind != "" || payload.RestoreAvailable || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 {
			return errors.New("Host Files trash request is invalid")
		}
	case operationHostFilesRestore:
		if payload.StoredPath == "" || !filepath.IsAbs(payload.StoredPath) || payload.Destination == "" || !filepath.IsAbs(payload.Destination) || payload.Path != "" || payload.Directory != "" || payload.Name != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 {
			return errors.New("Host Files restore request is invalid")
		}
	case operationHostFilesPurge:
		if payload.StoredPath == "" || !filepath.IsAbs(payload.StoredPath) || payload.Path != "" || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.RestoreAvailable || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 {
			return errors.New("Host Files purge request is invalid")
		}
	case operationHostFilesMove:
		if payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.Destination == "" || !filepath.IsAbs(payload.Destination) || payload.Directory != "" || payload.Name != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.RestoreAvailable || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 {
			return errors.New("Host Files move request is invalid")
		}
	}
	return nil
}

func onlyHostFilePath(payload *hostFilesWireRequest) bool {
	return payload.Path != "" && filepath.IsAbs(payload.Path) && payload.Directory == "" && payload.Name == "" && payload.Destination == "" && payload.StoredPath == "" && payload.StoredName == "" && payload.CanonicalKind == "" && !payload.RestoreAvailable && payload.MaxBytes == 0 && payload.Offset == 0 && payload.Limit == 0
}

func isHostFilesOperation(operation string) bool {
	switch operation {
	case operationHostFilesRoots, operationHostFilesList, operationHostFilesInfo, operationHostFilesReadText,
		operationHostFilesCanonical, operationHostFilesAvailable, operationHostFilesMkdir, operationHostFilesToggleExec,
		operationHostFilesTrash, operationHostFilesRestore, operationHostFilesPurge, operationHostFilesMove:
		return true
	default:
		return false
	}
}

type HostFilesBackend struct{ client *Client }

func NewHostFilesBackend(client *Client) *HostFilesBackend { return &HostFilesBackend{client: client} }

func (backend *HostFilesBackend) call(ctx context.Context, operation string, payload hostFilesWireRequest) (hostFilesWireResponse, error) {
	if backend == nil || backend.client == nil {
		return hostFilesWireResponse{}, ErrHostFilesUnavailable
	}
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return hostFilesWireResponse{}, errors.New("privileged Broker Host Files authorization is missing")
	}
	response, err := backend.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operation, RequestID: authorization.RequestID, SessionToken: authorization.SessionToken, HostFiles: &payload})
	if err != nil {
		return hostFilesWireResponse{}, fmt.Errorf("%w: %v", ErrHostFilesUnavailable, err)
	}
	if response.HostFiles == nil {
		return hostFilesWireResponse{}, errors.New("privileged Broker returned an invalid Host Files response")
	}
	return *response.HostFiles, nil
}

func (backend *HostFilesBackend) Roots(ctx context.Context) ([]hostfiles.Entry, error) {
	value, err := backend.call(ctx, operationHostFilesRoots, hostFilesWireRequest{})
	return value.Entries, err
}

func (backend *HostFilesBackend) List(ctx context.Context, path string) ([]hostfiles.Entry, error) {
	var entries []hostfiles.Entry
	for offset := 0; ; {
		value, err := backend.call(ctx, operationHostFilesList, hostFilesWireRequest{Path: path, Offset: offset, Limit: hostFilesPageSize})
		if err != nil {
			return nil, err
		}
		entries = append(entries, value.Entries...)
		if value.NextOffset == 0 {
			return entries, nil
		}
		if value.NextOffset <= offset {
			return nil, errors.New("privileged Broker returned an invalid Host Files page")
		}
		offset = value.NextOffset
	}
}

func (backend *HostFilesBackend) Info(ctx context.Context, path string) (HostFileInfo, error) {
	value, err := backend.call(ctx, operationHostFilesInfo, hostFilesWireRequest{Path: path})
	if value.Info == nil {
		return HostFileInfo{}, errors.Join(err, errors.New("privileged Broker returned no Host Files metadata"))
	}
	return *value.Info, err
}

func (backend *HostFilesBackend) ReadText(ctx context.Context, path string, maxBytes int64) (hostfiles.TextDocument, error) {
	value, err := backend.call(ctx, operationHostFilesReadText, hostFilesWireRequest{Path: path, MaxBytes: maxBytes})
	if value.Document == nil {
		return hostfiles.TextDocument{}, errors.Join(err, errors.New("privileged Broker returned no Host Files text"))
	}
	return *value.Document, err
}

func (backend *HostFilesBackend) canonical(ctx context.Context, kind hostFilesCanonicalKind, path, name string) (string, error) {
	value, err := backend.call(ctx, operationHostFilesCanonical, hostFilesWireRequest{Path: path, Name: name, CanonicalKind: kind})
	return value.CanonicalPath, err
}

func (backend *HostFilesBackend) CanonicalExisting(ctx context.Context, path string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalExisting, path, "")
}
func (backend *HostFilesBackend) CanonicalDirectory(ctx context.Context, path string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalDirectory, path, "")
}
func (backend *HostFilesBackend) CanonicalDestination(ctx context.Context, path string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalDestination, path, "")
}
func (backend *HostFilesBackend) Destination(ctx context.Context, directory, name string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalChild, directory, name)
}

func (backend *HostFilesBackend) AvailableName(ctx context.Context, directory, name string) (string, error) {
	value, err := backend.call(ctx, operationHostFilesAvailable, hostFilesWireRequest{Directory: directory, Name: name})
	return value.AvailableName, err
}

func (backend *HostFilesBackend) CreateDirectory(ctx context.Context, directory, name string) error {
	_, err := backend.call(ctx, operationHostFilesMkdir, hostFilesWireRequest{Directory: directory, Name: name})
	return err
}

func (backend *HostFilesBackend) ToggleOwnerExecute(ctx context.Context, path string) (bool, error) {
	value, err := backend.call(ctx, operationHostFilesToggleExec, hostFilesWireRequest{Path: path})
	if value.Execute == nil {
		return false, errors.Join(err, errors.New("privileged Broker returned no execute-bit state"))
	}
	return *value.Execute, err
}

func (backend *HostFilesBackend) MoveToTrash(ctx context.Context, path, storedName string) (hostfiles.Trashed, error) {
	value, err := backend.call(ctx, operationHostFilesTrash, hostFilesWireRequest{Path: path, StoredName: storedName})
	if value.Trashed == nil {
		return hostfiles.Trashed{}, errors.Join(err, errors.New("privileged Broker returned no trash record"))
	}
	return *value.Trashed, err
}

func (backend *HostFilesBackend) RestoreFromTrash(ctx context.Context, storedPath, original string) error {
	_, err := backend.call(ctx, operationHostFilesRestore, hostFilesWireRequest{StoredPath: storedPath, Destination: original})
	return err
}

func (backend *HostFilesBackend) RestoreFromTrashToAvailablePath(ctx context.Context, storedPath, original string) (string, error) {
	value, err := backend.call(ctx, operationHostFilesRestore, hostFilesWireRequest{StoredPath: storedPath, Destination: original, RestoreAvailable: true})
	return value.CanonicalPath, err
}

func (backend *HostFilesBackend) PurgeTrash(ctx context.Context, storedPath string) error {
	_, err := backend.call(ctx, operationHostFilesPurge, hostFilesWireRequest{StoredPath: storedPath})
	return err
}

func (backend *HostFilesBackend) Move(ctx context.Context, source, destination string) error {
	_, err := backend.call(ctx, operationHostFilesMove, hostFilesWireRequest{Path: source, Destination: destination})
	return err
}

func (info HostFileInfo) String() string {
	return fmt.Sprintf("%s (%d bytes)", info.Name, info.Size)
}
