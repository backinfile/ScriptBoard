package privilegebroker

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	ByteOffset       int64                  `json:"byte_offset,omitempty"`
	ByteLimit        int                    `json:"byte_limit,omitempty"`
	Handle           string                 `json:"handle,omitempty"`
	StagingPath      string                 `json:"staging_path,omitempty"`
	ExpectedDigest   string                 `json:"expected_digest,omitempty"`
	Replace          bool                   `json:"replace,omitempty"`
	DirectoryPrepare bool                   `json:"directory_prepare,omitempty"`
	Record           string                 `json:"record,omitempty"`
}

type hostFilesWireResponse struct {
	Entries        []hostfiles.Entry       `json:"entries,omitempty"`
	Info           *HostFileInfo           `json:"info,omitempty"`
	Document       *hostfiles.TextDocument `json:"document,omitempty"`
	CanonicalPath  string                  `json:"canonical_path,omitempty"`
	AvailableName  string                  `json:"available_name,omitempty"`
	Execute        *bool                   `json:"execute,omitempty"`
	Trashed        *hostfiles.Trashed      `json:"trashed,omitempty"`
	NextOffset     int                     `json:"next_offset,omitempty"`
	Content        []byte                  `json:"content,omitempty"`
	Handle         string                  `json:"handle,omitempty"`
	Prepared       *hostFilesPrepared      `json:"prepared,omitempty"`
	SameFilesystem *bool                   `json:"same_filesystem,omitempty"`
}

type hostFilesPrepared struct {
	Path      string `json:"path"`
	Directory string `json:"directory,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type brokerHostFilesService struct {
	files       *hostfiles.Manager
	stagingRoot string
	mu          sync.Mutex
	reads       map[string]*os.File
}

func NewBrokerHostFilesService(files *hostfiles.Manager, stagingRoots ...string) (HostFilesService, error) {
	if files == nil {
		return nil, errors.New("Broker Host Files manager is required")
	}
	stagingRoot := ""
	if len(stagingRoots) != 0 {
		stagingRoot = filepath.Clean(strings.TrimSpace(stagingRoots[0]))
		if !filepath.IsAbs(stagingRoot) {
			return nil, errors.New("Broker Host Files staging root must be absolute")
		}
	}
	return &brokerHostFilesService{files: files, stagingRoot: stagingRoot, reads: make(map[string]*os.File)}, nil
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

func (service *brokerHostFilesService) OpenRead(_ context.Context, path string) (string, HostFileInfo, error) {
	file, info, err := service.files.OpenRegular(path)
	if err != nil {
		return "", HostFileInfo{}, err
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		_ = file.Close()
		return "", HostFileInfo{}, err
	}
	handle := base64.RawURLEncoding.EncodeToString(token[:])
	service.mu.Lock()
	if len(service.reads) >= 1024 {
		service.mu.Unlock()
		_ = file.Close()
		return "", HostFileInfo{}, errors.New("too many open Host Files downloads")
	}
	service.reads[handle] = file
	service.mu.Unlock()
	return handle, HostFileInfo{Name: info.Name(), Size: info.Size(), Mode: info.Mode(), ModifiedAt: info.ModTime(), Directory: false}, nil
}

func (service *brokerHostFilesService) ReadChunk(_ context.Context, handle string, offset int64, limit int) ([]byte, error) {
	service.mu.Lock()
	file := service.reads[handle]
	service.mu.Unlock()
	if file == nil {
		return nil, errors.New("Host Files download handle is unavailable")
	}
	buffer := make([]byte, limit)
	read, err := file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buffer[:read], nil
}

func (service *brokerHostFilesService) CloseRead(_ context.Context, handle string) error {
	service.mu.Lock()
	file := service.reads[handle]
	delete(service.reads, handle)
	service.mu.Unlock()
	if file == nil {
		return errors.New("Host Files download handle is unavailable")
	}
	return file.Close()
}

func (service *brokerHostFilesService) consumeStaging(path string) (*os.File, error) {
	if service.stagingRoot == "" || !filepath.IsAbs(path) || filepath.Dir(filepath.Clean(path)) != service.stagingRoot {
		return nil, errors.New("Host Files staging path is outside the exchange root")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Host Files staging entry is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, errors.New("Host Files staging entry changed while opening")
	}
	return file, nil
}

func (service *brokerHostFilesService) Upload(_ context.Context, stagingPath, directory, name string, maxBytes int64, replace bool, storedName string) (*hostfiles.Trashed, error) {
	file, err := service.consumeStaging(stagingPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(stagingPath) }()
	return service.files.Upload(directory, name, file, maxBytes, replace, storedName)
}

func (service *brokerHostFilesService) SaveText(_ context.Context, stagingPath, path, expectedDigest, storedName string, maxBytes int64) (hostfiles.Trashed, error) {
	file, err := service.consumeStaging(stagingPath)
	if err != nil {
		return hostfiles.Trashed{}, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(stagingPath) }()
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return hostfiles.Trashed{}, errors.New("Host Files staged text exceeds the size limit")
	}
	return service.files.SaveText(path, expectedDigest, string(body), storedName, maxBytes)
}

func (service *brokerHostFilesService) RollbackTextSave(_ context.Context, path, storedPath string) error {
	return service.files.RollbackTextSave(path, storedPath)
}

func (service *brokerHostFilesService) RemoveRegular(_ context.Context, path string) error {
	return service.files.RemoveRegular(path)
}

func (service *brokerHostFilesService) Prepare(_ context.Context, path string, directory bool) (hostFilesPrepared, error) {
	if directory {
		prepared, err := service.files.PrepareDirectory(path)
		return hostFilesPrepared{Path: prepared.Path}, err
	}
	prepared, err := service.files.PrepareScript(path)
	return hostFilesPrepared{Path: prepared.Path, Directory: prepared.Directory, Digest: prepared.Digest}, err
}

func (service *brokerHostFilesService) SameFilesystem(_ context.Context, source, destination string) (bool, error) {
	return service.files.SameFilesystem(source, destination)
}

func (service *brokerHostFilesService) AppendText(_ context.Context, path, record string) error {
	return service.files.AppendText(path, record)
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
	case operationHostFilesOpenRead:
		var info HostFileInfo
		result.Handle, info, err = server.hostFiles.OpenRead(ctx, payload.Path)
		result.Info = &info
	case operationHostFilesReadChunk:
		result.Content, err = server.hostFiles.ReadChunk(ctx, payload.Handle, payload.ByteOffset, payload.ByteLimit)
	case operationHostFilesCloseRead:
		err = server.hostFiles.CloseRead(ctx, payload.Handle)
	case operationHostFilesUpload:
		result.Trashed, err = server.hostFiles.Upload(ctx, payload.StagingPath, payload.Directory, payload.Name, payload.MaxBytes, payload.Replace, payload.StoredName)
	case operationHostFilesSaveText:
		var value hostfiles.Trashed
		value, err = server.hostFiles.SaveText(ctx, payload.StagingPath, payload.Path, payload.ExpectedDigest, payload.StoredName, payload.MaxBytes)
		result.Trashed = &value
	case operationHostFilesRollback:
		err = server.hostFiles.RollbackTextSave(ctx, payload.Path, payload.StoredPath)
	case operationHostFilesRemove:
		err = server.hostFiles.RemoveRegular(ctx, payload.Path)
	case operationHostFilesPrepare:
		var value hostFilesPrepared
		value, err = server.hostFiles.Prepare(ctx, payload.Path, payload.DirectoryPrepare)
		result.Prepared = &value
	case operationHostFilesSameFS:
		var same bool
		same, err = server.hostFiles.SameFilesystem(ctx, payload.Path, payload.Destination)
		result.SameFilesystem = &same
	case operationHostFilesAppend:
		err = server.hostFiles.AppendText(ctx, payload.Path, payload.Record)
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
	case operationHostFilesMkdir, operationHostFilesUpload, operationHostFilesSaveText, operationHostFilesRollback,
		operationHostFilesRemove, operationHostFilesAppend:
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
	if len(payload.Handle) > 128 || len(payload.StagingPath) > 4096 || len(payload.ExpectedDigest) > 128 || len(payload.Record) > 64<<10 || strings.ContainsAny(payload.Handle+payload.StagingPath+payload.ExpectedDigest+payload.Record, "\x00") {
		return errors.New("Host Files request value is invalid")
	}
	for _, value := range []string{payload.Name, payload.StoredName} {
		if len(value) > 255 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("Host Files name is invalid")
		}
	}
	emptyPaths := func() bool {
		return payload.Path == "" && payload.Directory == "" && payload.Name == "" && payload.Destination == "" && payload.StoredPath == "" && payload.StoredName == "" && payload.CanonicalKind == "" && !payload.RestoreAvailable && payload.MaxBytes == 0 && payload.ByteOffset == 0 && payload.ByteLimit == 0 && payload.Handle == "" && payload.StagingPath == "" && payload.ExpectedDigest == "" && !payload.Replace && !payload.DirectoryPrepare && payload.Record == ""
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
	case operationHostFilesOpenRead:
		if !onlyHostFilePath(payload) {
			return errors.New("Host Files open-read request is invalid")
		}
	case operationHostFilesReadChunk:
		if !onlyHostFileHandle(payload) || payload.ByteOffset < 0 || payload.ByteLimit <= 0 || payload.ByteLimit > 3<<20 {
			return errors.New("Host Files read-chunk request is invalid")
		}
	case operationHostFilesCloseRead:
		if !onlyHostFileHandle(payload) || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files close-read request is invalid")
		}
	case operationHostFilesUpload:
		if payload.StagingPath == "" || !filepath.IsAbs(payload.StagingPath) || payload.Directory == "" || !filepath.IsAbs(payload.Directory) || payload.Name == "" || payload.MaxBytes <= 0 || payload.MaxBytes > 1<<30 || payload.Path != "" || payload.Destination != "" || payload.StoredPath != "" || payload.CanonicalKind != "" || payload.ExpectedDigest != "" || payload.Handle != "" || payload.Record != "" || payload.Offset != 0 || payload.Limit != 0 || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files upload request is invalid")
		}
	case operationHostFilesSaveText:
		if payload.StagingPath == "" || !filepath.IsAbs(payload.StagingPath) || payload.Path == "" || !filepath.IsAbs(payload.Path) || len(payload.ExpectedDigest) != 64 || payload.StoredName == "" || payload.MaxBytes <= 0 || payload.MaxBytes > 1<<20 || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredPath != "" || payload.CanonicalKind != "" || payload.Handle != "" || payload.Record != "" || payload.Replace || payload.Offset != 0 || payload.Limit != 0 || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files save-text request is invalid")
		}
	case operationHostFilesRollback:
		if payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.StoredPath == "" || !filepath.IsAbs(payload.StoredPath) || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.Handle != "" || payload.StagingPath != "" || payload.ExpectedDigest != "" || payload.Record != "" || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files rollback request is invalid")
		}
	case operationHostFilesRemove:
		if !onlyHostFilePath(payload) {
			return errors.New("Host Files remove request is invalid")
		}
	case operationHostFilesPrepare:
		if payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.Handle != "" || payload.StagingPath != "" || payload.ExpectedDigest != "" || payload.Record != "" || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files prepare request is invalid")
		}
	case operationHostFilesSameFS:
		if payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.Destination == "" || !filepath.IsAbs(payload.Destination) || payload.Directory != "" || payload.Name != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.Handle != "" || payload.StagingPath != "" || payload.ExpectedDigest != "" || payload.Record != "" || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files filesystem request is invalid")
		}
	case operationHostFilesAppend:
		if payload.Path == "" || !filepath.IsAbs(payload.Path) || len(payload.Record) > 64<<10 || payload.Record == "" || payload.Directory != "" || payload.Name != "" || payload.Destination != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.Handle != "" || payload.StagingPath != "" || payload.ExpectedDigest != "" || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files append request is invalid")
		}
	}
	if hostFilesExpectedPayload(request.Operation, *payload) != *payload {
		return errors.New("Host Files request contains operation-forbidden fields")
	}
	return nil
}

func hostFilesExpectedPayload(operation string, payload hostFilesWireRequest) hostFilesWireRequest {
	switch operation {
	case operationHostFilesRoots:
		return hostFilesWireRequest{}
	case operationHostFilesList:
		return hostFilesWireRequest{Path: payload.Path, Offset: payload.Offset, Limit: payload.Limit}
	case operationHostFilesInfo, operationHostFilesToggleExec, operationHostFilesOpenRead, operationHostFilesRemove:
		return hostFilesWireRequest{Path: payload.Path}
	case operationHostFilesReadText:
		return hostFilesWireRequest{Path: payload.Path, MaxBytes: payload.MaxBytes}
	case operationHostFilesCanonical:
		return hostFilesWireRequest{Path: payload.Path, Name: payload.Name, CanonicalKind: payload.CanonicalKind}
	case operationHostFilesAvailable, operationHostFilesMkdir:
		return hostFilesWireRequest{Directory: payload.Directory, Name: payload.Name}
	case operationHostFilesTrash:
		return hostFilesWireRequest{Path: payload.Path, StoredName: payload.StoredName}
	case operationHostFilesRestore:
		return hostFilesWireRequest{StoredPath: payload.StoredPath, Destination: payload.Destination, RestoreAvailable: payload.RestoreAvailable}
	case operationHostFilesPurge:
		return hostFilesWireRequest{StoredPath: payload.StoredPath}
	case operationHostFilesMove, operationHostFilesSameFS:
		return hostFilesWireRequest{Path: payload.Path, Destination: payload.Destination}
	case operationHostFilesReadChunk:
		return hostFilesWireRequest{Handle: payload.Handle, ByteOffset: payload.ByteOffset, ByteLimit: payload.ByteLimit}
	case operationHostFilesCloseRead:
		return hostFilesWireRequest{Handle: payload.Handle}
	case operationHostFilesUpload:
		return hostFilesWireRequest{StagingPath: payload.StagingPath, Directory: payload.Directory, Name: payload.Name, MaxBytes: payload.MaxBytes, Replace: payload.Replace, StoredName: payload.StoredName}
	case operationHostFilesSaveText:
		return hostFilesWireRequest{StagingPath: payload.StagingPath, Path: payload.Path, ExpectedDigest: payload.ExpectedDigest, StoredName: payload.StoredName, MaxBytes: payload.MaxBytes}
	case operationHostFilesRollback:
		return hostFilesWireRequest{Path: payload.Path, StoredPath: payload.StoredPath}
	case operationHostFilesPrepare:
		return hostFilesWireRequest{Path: payload.Path, DirectoryPrepare: payload.DirectoryPrepare}
	case operationHostFilesAppend:
		return hostFilesWireRequest{Path: payload.Path, Record: payload.Record}
	default:
		return hostFilesWireRequest{}
	}
}

func onlyHostFilePath(payload *hostFilesWireRequest) bool {
	return payload.Path != "" && filepath.IsAbs(payload.Path) && payload.Directory == "" && payload.Name == "" && payload.Destination == "" && payload.StoredPath == "" && payload.StoredName == "" && payload.CanonicalKind == "" && !payload.RestoreAvailable && payload.MaxBytes == 0 && payload.Offset == 0 && payload.Limit == 0 && payload.ByteOffset == 0 && payload.ByteLimit == 0 && payload.Handle == "" && payload.StagingPath == "" && payload.ExpectedDigest == "" && !payload.Replace && !payload.DirectoryPrepare && payload.Record == ""
}

func onlyHostFileHandle(payload *hostFilesWireRequest) bool {
	return len(payload.Handle) == 32 && payload.Path == "" && payload.Directory == "" && payload.Name == "" && payload.Destination == "" && payload.StoredPath == "" && payload.StoredName == "" && payload.CanonicalKind == "" && !payload.RestoreAvailable && payload.MaxBytes == 0 && payload.Offset == 0 && payload.Limit == 0 && payload.StagingPath == "" && payload.ExpectedDigest == "" && !payload.Replace && !payload.DirectoryPrepare && payload.Record == ""
}

func isHostFilesOperation(operation string) bool {
	switch operation {
	case operationHostFilesRoots, operationHostFilesList, operationHostFilesInfo, operationHostFilesReadText,
		operationHostFilesCanonical, operationHostFilesAvailable, operationHostFilesMkdir, operationHostFilesToggleExec,
		operationHostFilesTrash, operationHostFilesRestore, operationHostFilesPurge, operationHostFilesMove,
		operationHostFilesOpenRead, operationHostFilesReadChunk, operationHostFilesCloseRead, operationHostFilesUpload,
		operationHostFilesSaveText, operationHostFilesRollback, operationHostFilesRemove, operationHostFilesPrepare,
		operationHostFilesSameFS, operationHostFilesAppend:
		return true
	default:
		return false
	}
}

type HostFilesBackend struct {
	client      *Client
	stagingRoot string
}

func NewHostFilesBackend(client *Client, stagingRoots ...string) *HostFilesBackend {
	root := ""
	if len(stagingRoots) != 0 {
		root = filepath.Clean(stagingRoots[0])
	}
	return &HostFilesBackend{client: client, stagingRoot: root}
}

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

type RemoteHostFile struct {
	ctx     context.Context
	backend *HostFilesBackend
	handle  string
	offset  int64
	info    HostFileInfo
	closed  bool
}

func (backend *HostFilesBackend) OpenRegular(ctx context.Context, path string) (*RemoteHostFile, HostFileInfo, error) {
	value, err := backend.call(ctx, operationHostFilesOpenRead, hostFilesWireRequest{Path: path})
	if err != nil {
		return nil, HostFileInfo{}, err
	}
	if value.Info == nil || len(value.Handle) != 32 {
		return nil, HostFileInfo{}, errors.New("privileged Broker returned an invalid Host Files download")
	}
	return &RemoteHostFile{ctx: ctx, backend: backend, handle: value.Handle, info: *value.Info}, *value.Info, nil
}

func (file *RemoteHostFile) Read(destination []byte) (int, error) {
	if file.closed {
		return 0, os.ErrClosed
	}
	if file.offset >= file.info.Size {
		return 0, io.EOF
	}
	wanted := min(len(destination), 3<<20)
	value, err := file.backend.call(file.ctx, operationHostFilesReadChunk, hostFilesWireRequest{Handle: file.handle, ByteOffset: file.offset, ByteLimit: wanted})
	if err != nil {
		return 0, err
	}
	read := copy(destination, value.Content)
	file.offset += int64(read)
	if read == 0 || file.offset >= file.info.Size {
		return read, io.EOF
	}
	return read, nil
}

func (file *RemoteHostFile) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = file.offset + offset
	case io.SeekEnd:
		next = file.info.Size + offset
	default:
		return 0, errors.New("invalid Host Files seek origin")
	}
	if next < 0 {
		return 0, errors.New("negative Host Files seek offset")
	}
	file.offset = next
	return next, nil
}

func (file *RemoteHostFile) Close() error {
	if file.closed {
		return nil
	}
	file.closed = true
	_, err := file.backend.call(file.ctx, operationHostFilesCloseRead, hostFilesWireRequest{Handle: file.handle})
	return err
}

func (backend *HostFilesBackend) stage(source io.Reader, maxBytes int64) (string, error) {
	if backend.stagingRoot == "" || !filepath.IsAbs(backend.stagingRoot) {
		return "", ErrHostFilesUnavailable
	}
	if err := os.MkdirAll(backend.stagingRoot, 0o750); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(backend.stagingRoot, "host-files-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if err := file.Chmod(0o640); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	written, err := io.Copy(file, io.LimitReader(source, maxBytes+1))
	if err == nil && written > maxBytes {
		err = fmt.Errorf("Host Files staging input exceeds %d bytes", maxBytes)
	}
	if syncErr := file.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	file = nil
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (backend *HostFilesBackend) Upload(ctx context.Context, directory, name string, source io.Reader, maxBytes int64, replace bool, storedName string) (*hostfiles.Trashed, error) {
	staged, err := backend.stage(source, maxBytes)
	if err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	value, err := backend.call(ctx, operationHostFilesUpload, hostFilesWireRequest{StagingPath: staged, Directory: directory, Name: name, MaxBytes: maxBytes, Replace: replace, StoredName: storedName})
	return value.Trashed, err
}

func (backend *HostFilesBackend) SaveText(ctx context.Context, path, expectedDigest, content, storedName string, maxBytes int64) (hostfiles.Trashed, error) {
	staged, err := backend.stage(strings.NewReader(content), maxBytes)
	if err != nil {
		return hostfiles.Trashed{}, err
	}
	defer os.Remove(staged)
	value, err := backend.call(ctx, operationHostFilesSaveText, hostFilesWireRequest{StagingPath: staged, Path: path, ExpectedDigest: expectedDigest, StoredName: storedName, MaxBytes: maxBytes})
	if value.Trashed == nil {
		return hostfiles.Trashed{}, errors.Join(err, errors.New("privileged Broker returned no text-save trash record"))
	}
	return *value.Trashed, err
}

func (backend *HostFilesBackend) RollbackTextSave(ctx context.Context, path, storedPath string) error {
	_, err := backend.call(ctx, operationHostFilesRollback, hostFilesWireRequest{Path: path, StoredPath: storedPath})
	return err
}

func (backend *HostFilesBackend) RemoveRegular(ctx context.Context, path string) error {
	_, err := backend.call(ctx, operationHostFilesRemove, hostFilesWireRequest{Path: path})
	return err
}

func (backend *HostFilesBackend) PrepareScript(ctx context.Context, path string) (hostfiles.Script, error) {
	value, err := backend.call(ctx, operationHostFilesPrepare, hostFilesWireRequest{Path: path})
	if value.Prepared == nil {
		return hostfiles.Script{}, errors.Join(err, errors.New("privileged Broker returned no prepared script"))
	}
	return hostfiles.Script{Path: value.Prepared.Path, Directory: value.Prepared.Directory, Digest: value.Prepared.Digest}, err
}

func (backend *HostFilesBackend) PrepareDirectory(ctx context.Context, path string) (hostfiles.PreparedDirectory, error) {
	value, err := backend.call(ctx, operationHostFilesPrepare, hostFilesWireRequest{Path: path, DirectoryPrepare: true})
	if value.Prepared == nil {
		return hostfiles.PreparedDirectory{}, errors.Join(err, errors.New("privileged Broker returned no prepared directory"))
	}
	return hostfiles.PreparedDirectory{Path: value.Prepared.Path}, err
}

func (backend *HostFilesBackend) SameFilesystem(ctx context.Context, source, destination string) (bool, error) {
	value, err := backend.call(ctx, operationHostFilesSameFS, hostFilesWireRequest{Path: source, Destination: destination})
	if value.SameFilesystem == nil {
		return false, errors.Join(err, errors.New("privileged Broker returned no filesystem relationship"))
	}
	return *value.SameFilesystem, err
}

func (backend *HostFilesBackend) AppendText(ctx context.Context, path, record string) error {
	_, err := backend.call(ctx, operationHostFilesAppend, hostFilesWireRequest{Path: path, Record: record})
	return err
}

func (info HostFileInfo) String() string {
	return fmt.Sprintf("%s (%d bytes)", info.Name, info.Size)
}
