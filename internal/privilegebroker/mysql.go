package privilegebroker

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/runmanager"
)

type mysqlWireRequest struct {
	Instance    mysqlmanager.Instance            `json:"instance"`
	Password    string                           `json:"password,omitempty"`
	Database    string                           `json:"database,omitempty"`
	Path        string                           `json:"path,omitempty"`
	OperationID string                           `json:"operation_id,omitempty"`
	Create      mysqlmanager.CreateDatabaseInput `json:"create"`
	Tools       mysqlmanager.ToolSettings        `json:"tools"`
	BackupID    string                           `json:"backup_id,omitempty"`
	Offset      int64                            `json:"offset,omitempty"`
	Limit       int                              `json:"limit,omitempty"`
	Object      string                           `json:"object,omitempty"`
	SQL         mysqlmanager.SQLRequest          `json:"sql"`
	Content     []byte                           `json:"content,omitempty"`
	Final       bool                             `json:"final,omitempty"`
	SHA256      string                           `json:"sha256,omitempty"`
}

type mysqlWireResponse struct {
	ConnectionTest *mysqlmanager.ConnectionTest  `json:"connection_test,omitempty"`
	Databases      []mysqlmanager.Database       `json:"databases,omitempty"`
	Status         *mysqlmanager.Status          `json:"status,omitempty"`
	Exists         bool                          `json:"exists,omitempty"`
	Dump           *mysqlmanager.DumpResult      `json:"dump,omitempty"`
	ToolStatus     *mysqlmanager.ToolStatus      `json:"tool_status,omitempty"`
	Content        []byte                        `json:"content,omitempty"`
	TotalBytes     int64                         `json:"total_bytes,omitempty"`
	Filename       string                        `json:"filename,omitempty"`
	Objects        []mysqlmanager.DatabaseObject `json:"objects,omitempty"`
	ObjectDetails  *mysqlmanager.ObjectDetails   `json:"object_details,omitempty"`
	SQLResult      *mysqlmanager.SQLResult       `json:"sql_result,omitempty"`
	Artifact       *mysqlmanager.ArtifactResult  `json:"artifact,omitempty"`
}

const maxMySQLArtifactBytes int64 = (2 << 30) + 1

type brokerMySQLService struct {
	brokerMySQLBackend
	db         *sql.DB
	operations interface {
		RequestCancel(context.Context, string) error
	}
	backupRoot string
}

type brokerMySQLBackend interface {
	mysqlmanager.Backend
	mysqlmanager.QueryBackend
}

func NewBrokerMySQLService(database *sql.DB, backend mysqlmanager.Backend, operations interface {
	RequestCancel(context.Context, string) error
}, backupRoot string) (MySQLService, error) {
	if database == nil || backend == nil || operations == nil || strings.TrimSpace(backupRoot) == "" {
		return nil, errors.New("Broker MySQL database and execution backend are required")
	}
	combined, ok := backend.(brokerMySQLBackend)
	if !ok {
		return nil, errors.New("Broker MySQL execution backend must support query browsing")
	}
	return &brokerMySQLService{brokerMySQLBackend: combined, db: database, operations: operations, backupRoot: backupRoot}, nil
}

func (service *brokerMySQLService) ValidateInstance(ctx context.Context, requested mysqlmanager.Instance) error {
	var actual mysqlmanager.Instance
	if err := service.db.QueryRowContext(ctx, `SELECT id,name,host,port,username,tls_mode,ca_path,credential_configured FROM mysql_instances WHERE id=?`, requested.ID).
		Scan(&actual.ID, &actual.Name, &actual.Host, &actual.Port, &actual.Username, &actual.TLSMode, &actual.CAPath, &actual.CredentialConfigured); err != nil {
		return errors.New("MySQL instance is unavailable")
	}
	if actual.ID != requested.ID || actual.Host != requested.Host || actual.Port != requested.Port || actual.Username != requested.Username || actual.TLSMode != requested.TLSMode || actual.CAPath != requested.CAPath || !actual.CredentialConfigured {
		return errors.New("MySQL instance request does not match committed metadata")
	}
	return nil
}

func (service *brokerMySQLService) ValidateInstanceID(ctx context.Context, id string) error {
	var exists bool
	if err := service.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM mysql_instances WHERE id=? AND credential_configured=1)", id).Scan(&exists); err != nil || !exists {
		return errors.New("MySQL instance is unavailable")
	}
	return nil
}

func (service *brokerMySQLService) CancelOperation(ctx context.Context, id string) error {
	return service.operations.RequestCancel(ctx, id)
}

func (service *brokerMySQLService) ArtifactRoot(ctx context.Context) (string, error) {
	var configured string
	err := service.db.QueryRowContext(ctx, "SELECT value FROM mysql_settings WHERE key='backup_root'").Scan(&configured)
	if errors.Is(err, sql.ErrNoRows) {
		return service.backupRoot, nil
	}
	if err != nil || !filepath.IsAbs(configured) {
		return "", errors.New("configured MySQL backup root is invalid")
	}
	return filepath.Clean(configured), nil
}

func (service *brokerMySQLService) PrepareArtifactRoot(ctx context.Context, requested string) error {
	configured, err := service.ArtifactRoot(ctx)
	if err != nil || filepath.Clean(requested) != filepath.Clean(configured) {
		return errors.New("MySQL artifact root does not match the configured backup root")
	}
	if info, statErr := os.Lstat(configured); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("configured MySQL backup root is not a regular directory")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	// Root preparation stays in the Broker, but the requested path must first
	// match the value committed in shared configuration to avoid arbitrary mkdir.
	if err := service.brokerMySQLBackend.PrepareArtifactRoot(ctx, configured); err != nil {
		return err
	}
	info, err := os.Lstat(configured)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("configured MySQL backup root is not a regular directory")
	}
	return nil
}

func (service *brokerMySQLService) ReadBackupChunk(ctx context.Context, id string, offset int64, limit int) ([]byte, int64, string, error) {
	var path, database string
	var total int64
	if err := service.db.QueryRowContext(ctx, "SELECT path, size_bytes, database_name FROM mysql_backups WHERE id = ?", id).Scan(&path, &total, &database); err != nil {
		return nil, 0, "", errors.New("MySQL backup is unavailable")
	}
	root, err := service.ArtifactRoot(ctx)
	if err != nil || !pathWithinRoot(root, path) {
		return nil, 0, "", errors.New("MySQL backup path is outside the configured root")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != total {
		return nil, 0, "", errors.New("MySQL backup file changed")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, 0, "", errors.New("MySQL backup file changed while opening")
	}
	buffer := make([]byte, min(limit, int(max(int64(0), total-offset))))
	read, readErr := file.ReadAt(buffer, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, 0, "", readErr
	}
	return buffer[:read], total, database + "-" + id + ".sql.gz", nil
}

func (service *brokerMySQLService) StoreArtifactChunk(_ context.Context, path string, content []byte, offset int64, final bool) (mysqlmanager.ArtifactResult, error) {
	if offset < 0 || offset > maxMySQLArtifactBytes || int64(len(content)) > maxMySQLArtifactBytes-offset {
		return mysqlmanager.ArtifactResult{}, errors.New("MySQL artifact upload exceeds the 2 GiB limit")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return mysqlmanager.ArtifactResult{}, err
	}
	partial := path + ".upload.partial"
	flags := os.O_WRONLY
	if offset == 0 {
		flags |= os.O_CREATE | os.O_EXCL
	} else {
		info, err := os.Lstat(partial)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return mysqlmanager.ArtifactResult{}, errors.New("MySQL artifact upload partial is not a regular file")
		}
	}
	file, err := os.OpenFile(partial, flags, 0o600)
	if err != nil {
		return mysqlmanager.ArtifactResult{}, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Size() != offset) {
		err = errors.New("MySQL artifact upload offset does not match")
	}
	if err == nil && offset > 0 {
		linkInfo, linkErr := os.Lstat(partial)
		if linkErr != nil || !os.SameFile(linkInfo, info) {
			err = errors.New("MySQL artifact upload partial changed while opening")
		}
	}
	if err == nil && len(content) > 0 {
		_, err = file.WriteAt(content, offset)
	}
	if syncErr := file.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(partial)
		return mysqlmanager.ArtifactResult{}, err
	}
	if !final {
		return mysqlmanager.ArtifactResult{}, nil
	}
	if _, destinationErr := os.Lstat(path); !errors.Is(destinationErr, os.ErrNotExist) {
		_ = os.Remove(partial)
		return mysqlmanager.ArtifactResult{}, errors.New("MySQL artifact destination already exists or is unavailable")
	}
	result, err := mysqlmanager.InspectArtifact(partial, true)
	if err != nil {
		_ = os.Remove(partial)
		return mysqlmanager.ArtifactResult{}, err
	}
	if err := os.Link(partial, path); err != nil {
		_ = os.Remove(partial)
		return mysqlmanager.ArtifactResult{}, err
	}
	if err := os.Remove(partial); err != nil {
		_ = os.Remove(path)
		return mysqlmanager.ArtifactResult{}, err
	}
	return result, nil
}

func (service *brokerMySQLService) VerifyArtifact(_ context.Context, path, expectedSHA256 string, compressed bool) error {
	result, err := mysqlmanager.InspectArtifact(path, compressed)
	if err != nil {
		return err
	}
	if !strings.EqualFold(result.SHA256, expectedSHA256) {
		return errors.New("backup SHA-256 verification failed")
	}
	return nil
}

func (service *brokerMySQLService) SetTools(ctx context.Context, tools mysqlmanager.ToolSettings) error {
	dump, err := trustedMySQLExecutable(tools.DumpExecutable, true)
	if err != nil {
		return err
	}
	client, err := trustedMySQLExecutable(tools.ClientExecutable, false)
	if err != nil {
		return err
	}
	return service.brokerMySQLBackend.SetTools(ctx, mysqlmanager.ToolSettings{DumpExecutable: dump, ClientExecutable: client})
}

func (service *brokerMySQLService) Dump(ctx context.Context, instance mysqlmanager.Instance, database, path string) (mysqlmanager.DumpResult, error) {
	tools := service.brokerMySQLBackend.Tools()
	resolved, err := trustedMySQLExecutable(tools.DumpExecutable, true)
	if err != nil {
		return mysqlmanager.DumpResult{}, err
	}
	if resolved != tools.DumpExecutable {
		tools.DumpExecutable = resolved
		if err := service.brokerMySQLBackend.SetTools(ctx, tools); err != nil {
			return mysqlmanager.DumpResult{}, err
		}
	}
	return service.brokerMySQLBackend.Dump(ctx, instance, database, path)
}

func (service *brokerMySQLService) Import(ctx context.Context, instance mysqlmanager.Instance, database, path string) error {
	tools := service.brokerMySQLBackend.Tools()
	resolved, err := trustedMySQLExecutable(tools.ClientExecutable, false)
	if err != nil {
		return err
	}
	if resolved != tools.ClientExecutable {
		tools.ClientExecutable = resolved
		if err := service.brokerMySQLBackend.SetTools(ctx, tools); err != nil {
			return err
		}
	}
	return service.brokerMySQLBackend.Import(ctx, instance, database, path)
}

func (service *brokerMySQLService) TestTools(ctx context.Context) mysqlmanager.ToolStatus {
	tools := service.brokerMySQLBackend.Tools()
	result := mysqlmanager.ToolStatus{DumpExecutable: tools.DumpExecutable, ClientExecutable: tools.ClientExecutable}
	dump, dumpErr := trustedMySQLExecutable(tools.DumpExecutable, true)
	client, clientErr := trustedMySQLExecutable(tools.ClientExecutable, false)
	if dumpErr != nil || clientErr != nil {
		return result
	}
	if dump != tools.DumpExecutable || client != tools.ClientExecutable {
		if err := service.brokerMySQLBackend.SetTools(ctx, mysqlmanager.ToolSettings{DumpExecutable: dump, ClientExecutable: client}); err != nil {
			return result
		}
	}
	return service.brokerMySQLBackend.TestTools(ctx)
}

func trustedMySQLExecutable(value string, dump bool) (string, error) {
	value = strings.TrimSpace(value)
	base := strings.ToLower(filepath.Base(value))
	base = strings.TrimSuffix(base, ".exe")
	allowed := base == "mysql" || base == "mariadb"
	if dump {
		allowed = base == "mysqldump" || base == "mariadb-dump"
	}
	if !allowed {
		return "", errors.New("MySQL executable is not an allowed client tool")
	}
	resolved := value
	if !filepath.IsAbs(resolved) {
		var err error
		resolved, err = exec.LookPath(resolved)
		if err != nil {
			return "", errors.New("MySQL executable is unavailable")
		}
	}
	return runmanager.ValidateExecutorTrust(resolved)
}

func (server *Server) mysqlOperation(parent context.Context, request wireRequest) wireResponse {
	if server.mysql == nil {
		return wireResponse{Status: statusError, ErrorCode: "mysql_unavailable", Message: "MySQL execution service is unavailable"}
	}
	payload := request.MySQL
	actor, action, err := server.authorizeMySQLOperation(request)
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "mysql_forbidden", Message: "MySQL operation is not authorized"}
	}
	if request.Operation == operationMySQLDump || request.Operation == operationMySQLImport || request.Operation == operationMySQLArtifactStoreChunk ||
		request.Operation == operationMySQLArtifactVerify || request.Operation == operationMySQLArtifactDelete || request.Operation == operationMySQLArtifactCleanup {
		root, rootErr := server.mysql.ArtifactRoot(context.Background())
		allowed := pathWithinRoot(root, payload.Path)
		if request.Operation == operationMySQLDump || request.Operation == operationMySQLArtifactStoreChunk {
			allowed = pathWithinRootForCreate(root, payload.Path)
		}
		if rootErr != nil || !allowed {
			return wireResponse{Status: statusError, ErrorCode: "mysql_path_forbidden", Message: "MySQL artifact path is outside the configured backup root"}
		}
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Hour)
	defer cancel()
	artifactOperation := request.Operation == operationMySQLArtifactPrepare || request.Operation == operationMySQLArtifactStoreChunk ||
		request.Operation == operationMySQLArtifactVerify || request.Operation == operationMySQLArtifactDelete || request.Operation == operationMySQLArtifactCleanup
	if request.Operation != operationMySQLDelete && request.Operation != operationMySQLTestTools && request.Operation != operationMySQLSetTools && request.Operation != operationMySQLCancel && request.Operation != operationMySQLBackupChunk && !artifactOperation {
		if err := server.mysql.ValidateInstance(ctx, payload.Instance); err != nil {
			return wireResponse{Status: statusError, ErrorCode: "mysql_instance_mismatch", Message: "MySQL instance does not match committed metadata"}
		}
	} else if request.Operation == operationMySQLDelete {
		if err := server.mysql.ValidateInstanceID(ctx, payload.Instance.ID); err != nil {
			return wireResponse{Status: statusError, ErrorCode: "mysql_instance_mismatch", Message: "MySQL instance is unavailable"}
		}
	}
	response := wireResponse{Status: statusOK, MySQL: &mysqlWireResponse{}}
	switch request.Operation {
	case operationMySQLStore:
		err = server.mysql.StoreCredential(ctx, payload.Instance, payload.Password)
	case operationMySQLDelete:
		err = server.mysql.DeleteCredential(ctx, payload.Instance.ID)
	case operationMySQLTest:
		var value mysqlmanager.ConnectionTest
		value, err = server.mysql.Test(ctx, payload.Instance)
		response.MySQL.ConnectionTest = &value
	case operationMySQLDatabases:
		response.MySQL.Databases, err = server.mysql.Databases(ctx, payload.Instance)
	case operationMySQLDatabasesAll, operationMySQLObjects, operationMySQLObjectDetails, operationMySQLExecuteSQL:
		switch request.Operation {
		case operationMySQLDatabasesAll:
			response.MySQL.Databases, err = server.mysql.DatabasesIncludingSystem(ctx, payload.Instance)
		case operationMySQLObjects:
			response.MySQL.Objects, err = server.mysql.Objects(ctx, payload.Instance, payload.Database)
		case operationMySQLObjectDetails:
			var value mysqlmanager.ObjectDetails
			value, err = server.mysql.ObjectDetails(ctx, payload.Instance, payload.Database, payload.Object)
			response.MySQL.ObjectDetails = &value
		case operationMySQLExecuteSQL:
			var value mysqlmanager.SQLResult
			value, err = server.mysql.ExecuteSQL(ctx, payload.Instance, payload.SQL)
			response.MySQL.SQLResult = &value
		}
	case operationMySQLStatus:
		var value mysqlmanager.Status
		value, err = server.mysql.Status(ctx, payload.Instance)
		response.MySQL.Status = &value
	case operationMySQLExists:
		response.MySQL.Exists, err = server.mysql.DatabaseExists(ctx, payload.Instance, payload.Database)
	case operationMySQLCreate:
		err = server.mysql.CreateDatabase(ctx, payload.Instance, payload.Create)
	case operationMySQLReplace:
		err = server.mysql.ReplaceDatabase(ctx, payload.Instance, payload.Database)
	case operationMySQLDrop:
		err = server.mysql.DropDatabase(ctx, payload.Instance, payload.Database)
	case operationMySQLClear:
		err = server.mysql.ClearDatabase(ctx, payload.Instance, payload.Database)
	case operationMySQLDump:
		var value mysqlmanager.DumpResult
		value, err = server.mysql.Dump(ctx, payload.Instance, payload.Database, payload.Path)
		response.MySQL.Dump = &value
	case operationMySQLImport:
		err = server.mysql.Import(ctx, payload.Instance, payload.Database, payload.Path)
	case operationMySQLSetTools:
		err = server.mysql.SetTools(ctx, payload.Tools)
	case operationMySQLTestTools:
		value := server.mysql.TestTools(ctx)
		response.MySQL.ToolStatus = &value
	case operationMySQLCancel:
		err = server.mysql.CancelOperation(ctx, payload.OperationID)
	case operationMySQLBackupChunk:
		response.MySQL.Content, response.MySQL.TotalBytes, response.MySQL.Filename, err = server.mysql.ReadBackupChunk(ctx, payload.BackupID, payload.Offset, payload.Limit)
	case operationMySQLArtifactPrepare:
		err = server.mysql.PrepareArtifactRoot(ctx, payload.Path)
	case operationMySQLArtifactStoreChunk:
		var value mysqlmanager.ArtifactResult
		value, err = server.mysql.StoreArtifactChunk(ctx, payload.Path, payload.Content, payload.Offset, payload.Final)
		response.MySQL.Artifact = &value
	case operationMySQLArtifactVerify:
		err = server.mysql.VerifyArtifact(ctx, payload.Path, payload.SHA256, true)
	case operationMySQLArtifactDelete:
		err = server.mysql.DeleteArtifact(ctx, payload.Path)
	case operationMySQLArtifactCleanup:
		err = server.mysql.CleanupArtifacts(ctx, payload.Path)
	}
	result := "succeeded"
	if err != nil {
		result = "failed"
	}
	if action != ActionMySQLRead && server.auditor != nil {
		body, _ := json.Marshal(payload)
		auditErr := server.auditor.Record(context.Background(), AuditRecord{OccurredAt: server.now().UTC(), RequestID: request.RequestID,
			Actor: actor, Action: action, Resource: payload.Instance.ID, Revision: "mysql-instance-v1", ParametersSHA256: parametersDigest(body), Result: result})
		if auditErr != nil && err == nil {
			return wireResponse{Status: statusError, ErrorCode: "audit_failed_after_execution", Message: "MySQL operation completed but result audit failed"}
		}
	}
	if err != nil {
		return mysqlFailureResponse(err)
	}
	return response
}

func mysqlFailureResponse(err error) wireResponse {
	message := strings.ToLower(err.Error())
	// Keep server details private while returning enough category information for an administrator to repair grants or host paths.
	if strings.Contains(message, "access denied") || strings.Contains(message, "command denied") || strings.Contains(message, "insufficient privilege") ||
		strings.Contains(message, "error 1044") || strings.Contains(message, "error 1045") || strings.Contains(message, "error 1142") || strings.Contains(message, "error 1227") {
		return wireResponse{Status: statusError, ErrorCode: "mysql_permission_denied", Message: "MySQL account lacks permission for this database operation"}
	}
	if strings.Contains(message, "executable is unavailable") || strings.Contains(message, "executable file not found") {
		return wireResponse{Status: statusError, ErrorCode: "mysql_tools_unavailable", Message: "MySQL client tools are unavailable in the privileged Broker"}
	}
	if strings.Contains(message, "permission denied") || strings.Contains(message, "access is denied") {
		return wireResponse{Status: statusError, ErrorCode: "mysql_artifact_permission_denied", Message: "Privileged Broker cannot access the configured MySQL backup path"}
	}
	return wireResponse{Status: statusError, ErrorCode: "mysql_failed", Message: "MySQL operation failed"}
}

func (server *Server) authorizeMySQLOperation(request wireRequest) (Actor, Action, error) {
	payload := request.MySQL
	action, recent := mysqlAction(request.Operation)
	if request.Operation == operationMySQLExecuteSQL {
		action = ActionMySQLExecute
		recent = payload.SQL.Mode == mysqlmanager.SQLModeWrite
	}
	body, _ := json.Marshal(payload)
	resource := payload.Instance.ID
	if request.Operation == operationMySQLBackupChunk {
		resource = payload.BackupID
	} else if request.Operation == operationMySQLArtifactPrepare || request.Operation == operationMySQLArtifactStoreChunk ||
		request.Operation == operationMySQLArtifactVerify || request.Operation == operationMySQLArtifactDelete || request.Operation == operationMySQLArtifactCleanup {
		resource = payload.Path
	}
	authorization := AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID, Action: action,
		Resource: resource, Revision: "mysql-instance-v1", ParametersSHA256: parametersDigest(body)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mode := domainAuthorizationCurrentPrivileged
	if recent {
		mode = domainAuthorizationRecentPrivileged
	}
	actor, err := server.authorizeActor(ctx, authorization, mode)
	return actor, action, err
}

func mysqlAction(operation string) (Action, bool) {
	switch operation {
	case operationMySQLStore:
		return ActionMySQLStore, true
	case operationMySQLDelete:
		return ActionMySQLDelete, true
	case operationMySQLCreate:
		return ActionMySQLCreate, false
	case operationMySQLReplace:
		return ActionMySQLReplace, true
	case operationMySQLDrop:
		return ActionMySQLDrop, true
	case operationMySQLClear:
		return ActionMySQLClear, true
	case operationMySQLDump:
		return ActionMySQLDump, false
	case operationMySQLImport:
		return ActionMySQLImport, true
	case operationMySQLSetTools:
		return ActionMySQLSetTools, true
	case operationMySQLCancel:
		return ActionMySQLCancel, false
	case operationMySQLExecuteSQL:
		return ActionMySQLExecute, false
	case operationMySQLArtifactStoreChunk, operationMySQLArtifactDelete:
		return ActionMySQLImport, true
	case operationMySQLArtifactPrepare:
		return ActionMySQLSetTools, true
	case operationMySQLArtifactVerify, operationMySQLArtifactCleanup:
		return ActionMySQLDump, false
	default:
		return ActionMySQLRead, false
	}
}

func pathWithinRoot(root, candidate string) bool {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(candidate) {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return false
	}
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(candidate)))
	if err != nil {
		return false
	}
	canonicalCandidate := filepath.Join(canonicalParent, filepath.Base(candidate))
	relative, err := filepath.Rel(canonicalRoot, canonicalCandidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func pathWithinRootForCreate(root, candidate string) bool {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(candidate) {
		return false
	}
	cleanRoot, cleanCandidate := filepath.Clean(root), filepath.Clean(candidate)
	relative, err := filepath.Rel(cleanRoot, cleanCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return false
	}
	ancestor := filepath.Dir(cleanCandidate)
	for {
		canonicalAncestor, evalErr := filepath.EvalSymlinks(ancestor)
		if evalErr == nil {
			relative, err = filepath.Rel(canonicalRoot, canonicalAncestor)
			return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
		}
		if !errors.Is(evalErr, os.ErrNotExist) || ancestor == filepath.Dir(ancestor) {
			return false
		}
		ancestor = filepath.Dir(ancestor)
	}
}

func validateMySQLRequest(request wireRequest) error {
	if request.MySQL == nil || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
		request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || request.Redis != nil || request.HostFiles != nil {
		return errors.New("MySQL request contains unrelated fields")
	}
	payload := request.MySQL
	artifactOperation := request.Operation == operationMySQLArtifactPrepare || request.Operation == operationMySQLArtifactStoreChunk ||
		request.Operation == operationMySQLArtifactVerify || request.Operation == operationMySQLArtifactDelete || request.Operation == operationMySQLArtifactCleanup
	minimalInstance := request.Operation == operationMySQLDelete || request.Operation == operationMySQLTestTools || request.Operation == operationMySQLSetTools
	minimalInstance = minimalInstance || request.Operation == operationMySQLCancel || request.Operation == operationMySQLBackupChunk || artifactOperation
	validMinimalInstance := validRemoteWebsiteID(payload.Instance.ID)
	if request.Operation == operationMySQLBackupChunk || artifactOperation {
		validMinimalInstance = payload.Instance == (mysqlmanager.Instance{})
	}
	if (!minimalInstance && !validMySQLInstance(payload.Instance)) || (minimalInstance && !validMinimalInstance) || len(payload.Password) > 8<<10 || len(payload.Database) > 64 || len(payload.Object) > 64 || len(payload.SQL.Database) > 64 || strings.ContainsAny(payload.Database+payload.Object+payload.SQL.Database+payload.Path+payload.OperationID+payload.BackupID+payload.SHA256, "\r\n\x00") || len(payload.Path) > 4096 || len(payload.OperationID) > 160 || len(payload.BackupID) > 160 || len(payload.SQL.Statement) > 256<<10 || len(payload.Content) > 1<<20 || len(payload.SHA256) > 64 {
		return errors.New("MySQL request fields are invalid")
	}
	if !validCredentialSessionToken(request.SessionToken) {
		return errors.New("MySQL session authorization is invalid")
	}
	if request.Operation != operationMySQLCancel && payload.OperationID != "" {
		return errors.New("MySQL request contains an unrelated operation ID")
	}
	if request.Operation != operationMySQLBackupChunk && payload.BackupID != "" {
		return errors.New("MySQL request contains unrelated backup download fields")
	}
	if request.Operation != operationMySQLBackupChunk && request.Operation != operationMySQLArtifactStoreChunk && (payload.Offset != 0 || payload.Limit != 0) {
		return errors.New("MySQL request contains unrelated artifact offset fields")
	}
	if request.Operation != operationMySQLArtifactStoreChunk && (len(payload.Content) != 0 || payload.Final) {
		return errors.New("MySQL request contains unrelated artifact content")
	}
	if request.Operation != operationMySQLArtifactVerify && payload.SHA256 != "" {
		return errors.New("MySQL request contains an unrelated artifact digest")
	}
	emptyCreate := payload.Create == (mysqlmanager.CreateDatabaseInput{})
	emptyTools := payload.Tools == (mysqlmanager.ToolSettings{})
	emptySQL := payload.SQL == (mysqlmanager.SQLRequest{})
	if request.Operation != operationMySQLExecuteSQL && !emptySQL {
		return errors.New("MySQL request contains unrelated SQL fields")
	}
	if request.Operation != operationMySQLObjectDetails && payload.Object != "" {
		return errors.New("MySQL request contains an unrelated object")
	}
	switch request.Operation {
	case operationMySQLStore:
		if payload.Password == "" || payload.Database != "" || payload.Path != "" || !emptyCreate || !emptyTools {
			return errors.New("MySQL credential store request is invalid")
		}
	case operationMySQLDelete, operationMySQLTest, operationMySQLDatabases, operationMySQLStatus, operationMySQLTestTools, operationMySQLDatabasesAll:
		if payload.Password != "" || payload.Database != "" || payload.Object != "" || payload.Path != "" || !emptyCreate || !emptyTools || !emptySQL {
			return errors.New("MySQL request contains operation-forbidden fields")
		}
	case operationMySQLObjects:
		if payload.Password != "" || payload.Database == "" || payload.Object != "" || payload.Path != "" || !emptyCreate || !emptyTools || !emptySQL {
			return errors.New("MySQL object list request is invalid")
		}
	case operationMySQLObjectDetails:
		if payload.Password != "" || payload.Database == "" || payload.Object == "" || payload.Path != "" || !emptyCreate || !emptyTools || !emptySQL {
			return errors.New("MySQL object details request is invalid")
		}
	case operationMySQLExecuteSQL:
		validMode := payload.SQL.Mode == mysqlmanager.SQLModeReadOnly || payload.SQL.Mode == mysqlmanager.SQLModeWrite
		if payload.Password != "" || payload.Database != "" || payload.Object != "" || payload.Path != "" || !emptyCreate || !emptyTools || payload.SQL.Database == "" || payload.SQL.Statement == "" || payload.SQL.Actor != (mysqlmanager.Actor{}) || !validMode || payload.SQL.Timeout <= 0 || payload.SQL.Timeout > 2*time.Minute || payload.SQL.MaxRows <= 0 || payload.SQL.MaxRows > 1000 {
			return errors.New("MySQL SQL execution request is invalid")
		}
	case operationMySQLCreate:
		if payload.Password != "" || payload.Database != "" || payload.Path != "" || emptyCreate || !emptyTools {
			return errors.New("MySQL create request is invalid")
		}
	case operationMySQLExists, operationMySQLReplace, operationMySQLDrop, operationMySQLClear:
		if payload.Password != "" || payload.Database == "" || payload.Path != "" || !emptyCreate || !emptyTools {
			return errors.New("MySQL database mutation request is invalid")
		}
	case operationMySQLDump, operationMySQLImport:
		if payload.Password != "" || payload.Database == "" || payload.Path == "" || !filepath.IsAbs(payload.Path) || !emptyCreate || !emptyTools {
			return errors.New("MySQL artifact request is invalid")
		}
	case operationMySQLSetTools:
		if payload.Password != "" || payload.Database != "" || payload.Path != "" || !emptyCreate || emptyTools {
			return errors.New("MySQL tool settings request is invalid")
		}
	case operationMySQLCancel:
		if payload.Password != "" || payload.Database != "" || payload.Path != "" || payload.OperationID == "" || !emptyCreate || !emptyTools {
			return errors.New("MySQL cancellation request is invalid")
		}
	case operationMySQLBackupChunk:
		if payload.Password != "" || payload.Database != "" || payload.Path != "" || payload.BackupID == "" || payload.Offset < 0 || payload.Limit <= 0 || payload.Limit > 3<<20 || !emptyCreate || !emptyTools {
			return errors.New("MySQL backup download request is invalid")
		}
	case operationMySQLArtifactPrepare, operationMySQLArtifactDelete, operationMySQLArtifactCleanup:
		if payload.Password != "" || payload.Database != "" || payload.Object != "" || payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.Offset != 0 || payload.Limit != 0 || !emptyCreate || !emptyTools || !emptySQL {
			return errors.New("MySQL artifact operation request is invalid")
		}
	case operationMySQLArtifactStoreChunk:
		if payload.Password != "" || payload.Database != "" || payload.Object != "" || payload.Path == "" || !filepath.IsAbs(payload.Path) || payload.Offset < 0 || payload.Limit != 0 || !emptyCreate || !emptyTools || !emptySQL {
			return errors.New("MySQL artifact upload request is invalid")
		}
	case operationMySQLArtifactVerify:
		if payload.Password != "" || payload.Database != "" || payload.Object != "" || payload.Path == "" || !filepath.IsAbs(payload.Path) || len(payload.SHA256) != 64 || !emptyCreate || !emptyTools || !emptySQL {
			return errors.New("MySQL artifact verification request is invalid")
		}
	}
	return nil
}

func validMySQLInstance(instance mysqlmanager.Instance) bool {
	validTLS := instance.TLSMode == mysqlmanager.TLSDisabled || instance.TLSMode == mysqlmanager.TLSPreferred || instance.TLSMode == mysqlmanager.TLSRequired || instance.TLSMode == mysqlmanager.TLSVerifyIdentity
	return validRemoteWebsiteID(instance.ID) && len(instance.Host) > 0 && len(instance.Host) <= 253 && instance.Port > 0 && instance.Port <= 65535 && validTLS &&
		len(instance.Username) > 0 && len(instance.Username) <= 320 && len(instance.CAPath) <= 4096 &&
		!strings.ContainsAny(instance.ID+instance.Host+instance.Username+instance.CAPath, "\r\n\x00") && instance.Password == ""
}

type MySQLBackend struct {
	client *Client
	mu     sync.Mutex
	tools  mysqlmanager.ToolSettings
}

func NewMySQLBackend(client *Client, tools mysqlmanager.ToolSettings) *MySQLBackend {
	return &MySQLBackend{client: client, tools: tools}
}

func (backend *MySQLBackend) InitializeTools(tools mysqlmanager.ToolSettings) {
	backend.mu.Lock()
	backend.tools = tools
	backend.mu.Unlock()
}

func (backend *MySQLBackend) call(ctx context.Context, operation string, payload mysqlWireRequest) (mysqlWireResponse, error) {
	if backend == nil || backend.client == nil {
		return mysqlWireResponse{}, errors.New("privileged Broker MySQL service is unavailable")
	}
	request := wireRequest{Version: ProtocolVersion, Operation: operation, MySQL: &payload}
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return mysqlWireResponse{}, errors.New("privileged Broker MySQL authorization is missing")
	}
	request.RequestID, request.SessionToken = authorization.RequestID, authorization.SessionToken
	response, err := backend.client.call(ctx, request)
	if err != nil {
		return mysqlWireResponse{}, err
	}
	if response.MySQL == nil {
		return mysqlWireResponse{}, errors.New("privileged Broker returned an invalid MySQL response")
	}
	return *response.MySQL, nil
}

func (backend *MySQLBackend) DownloadBackup(ctx context.Context, id string, destination io.Writer) (string, int64, error) {
	var offset int64
	var filename string
	var total int64
	for {
		value, err := backend.call(ctx, operationMySQLBackupChunk, mysqlWireRequest{BackupID: id, Offset: offset, Limit: 3 << 20})
		if err != nil {
			return "", 0, err
		}
		if offset == 0 {
			filename, total = value.Filename, value.TotalBytes
		}
		if value.TotalBytes != total || value.Filename != filename || offset+int64(len(value.Content)) > total {
			return "", 0, errors.New("privileged Broker returned inconsistent MySQL backup content")
		}
		if len(value.Content) > 0 {
			if _, err := destination.Write(value.Content); err != nil {
				return "", 0, err
			}
			offset += int64(len(value.Content))
		}
		if offset == total {
			return filename, total, nil
		}
		if len(value.Content) == 0 {
			return "", 0, io.ErrUnexpectedEOF
		}
	}
}

func (backend *MySQLBackend) PrepareArtifactRoot(ctx context.Context, root string) error {
	_, err := backend.call(ctx, operationMySQLArtifactPrepare, mysqlWireRequest{Path: root})
	return err
}

func (backend *MySQLBackend) StoreArtifact(ctx context.Context, path string, source io.Reader, compressed bool) (mysqlmanager.ArtifactResult, error) {
	reader := source
	var pipeReader *io.PipeReader
	if !compressed {
		var pipeWriter *io.PipeWriter
		pipeReader, pipeWriter = io.Pipe()
		reader = pipeReader
		go func() {
			writer := gzip.NewWriter(pipeWriter)
			_, copyErr := io.Copy(writer, source)
			closeErr := writer.Close()
			_ = pipeWriter.CloseWithError(errors.Join(copyErr, closeErr))
		}()
		defer pipeReader.Close()
	}
	buffer := make([]byte, 1<<20)
	var offset int64
	for {
		read, readErr := io.ReadFull(reader, buffer)
		final := errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)
		if readErr != nil && !final {
			_ = backend.DeleteArtifact(context.WithoutCancel(ctx), path)
			return mysqlmanager.ArtifactResult{}, readErr
		}
		value, err := backend.call(ctx, operationMySQLArtifactStoreChunk, mysqlWireRequest{Path: path, Content: buffer[:read], Offset: offset, Final: final})
		if err != nil {
			_ = backend.DeleteArtifact(context.WithoutCancel(ctx), path)
			return mysqlmanager.ArtifactResult{}, err
		}
		offset += int64(read)
		if final {
			if value.Artifact == nil {
				_ = backend.DeleteArtifact(context.WithoutCancel(ctx), path)
				return mysqlmanager.ArtifactResult{}, errors.New("privileged Broker returned no MySQL artifact result")
			}
			return *value.Artifact, nil
		}
	}
}

func (backend *MySQLBackend) VerifyArtifact(ctx context.Context, path, sha256 string, _ bool) error {
	_, err := backend.call(ctx, operationMySQLArtifactVerify, mysqlWireRequest{Path: path, SHA256: sha256})
	return err
}

func (backend *MySQLBackend) DeleteArtifact(ctx context.Context, path string) error {
	_, err := backend.call(ctx, operationMySQLArtifactDelete, mysqlWireRequest{Path: path})
	return err
}

func (backend *MySQLBackend) CleanupArtifacts(ctx context.Context, root string) error {
	_, err := backend.call(ctx, operationMySQLArtifactCleanup, mysqlWireRequest{Path: root})
	return err
}

func (backend *MySQLBackend) StoreCredential(ctx context.Context, instance mysqlmanager.Instance, password string) error {
	_, err := backend.call(ctx, operationMySQLStore, mysqlWireRequest{Instance: instance, Password: password})
	return err
}
func (backend *MySQLBackend) DeleteCredential(ctx context.Context, id string) error {
	_, err := backend.call(ctx, operationMySQLDelete, mysqlWireRequest{Instance: mysqlmanager.Instance{ID: id}})
	return err
}
func (backend *MySQLBackend) Test(ctx context.Context, instance mysqlmanager.Instance) (mysqlmanager.ConnectionTest, error) {
	value, err := backend.call(ctx, operationMySQLTest, mysqlWireRequest{Instance: instance})
	if value.ConnectionTest == nil {
		return mysqlmanager.ConnectionTest{}, errors.Join(err, errors.New("privileged Broker returned no MySQL connection test"))
	}
	return *value.ConnectionTest, err
}
func (backend *MySQLBackend) Databases(ctx context.Context, instance mysqlmanager.Instance) ([]mysqlmanager.Database, error) {
	value, err := backend.call(ctx, operationMySQLDatabases, mysqlWireRequest{Instance: instance})
	return value.Databases, err
}
func (backend *MySQLBackend) DatabasesIncludingSystem(ctx context.Context, instance mysqlmanager.Instance) ([]mysqlmanager.Database, error) {
	value, err := backend.call(ctx, operationMySQLDatabasesAll, mysqlWireRequest{Instance: instance})
	return value.Databases, err
}
func (backend *MySQLBackend) Objects(ctx context.Context, instance mysqlmanager.Instance, database string) ([]mysqlmanager.DatabaseObject, error) {
	value, err := backend.call(ctx, operationMySQLObjects, mysqlWireRequest{Instance: instance, Database: database})
	return value.Objects, err
}
func (backend *MySQLBackend) ObjectDetails(ctx context.Context, instance mysqlmanager.Instance, database, object string) (mysqlmanager.ObjectDetails, error) {
	value, err := backend.call(ctx, operationMySQLObjectDetails, mysqlWireRequest{Instance: instance, Database: database, Object: object})
	if value.ObjectDetails == nil {
		return mysqlmanager.ObjectDetails{}, errors.Join(err, errors.New("privileged Broker returned no MySQL object details"))
	}
	return *value.ObjectDetails, err
}
func (backend *MySQLBackend) ExecuteSQL(ctx context.Context, instance mysqlmanager.Instance, request mysqlmanager.SQLRequest) (mysqlmanager.SQLResult, error) {
	request.Actor = mysqlmanager.Actor{}
	value, err := backend.call(ctx, operationMySQLExecuteSQL, mysqlWireRequest{Instance: instance, SQL: request})
	if value.SQLResult == nil {
		return mysqlmanager.SQLResult{}, errors.Join(err, errors.New("privileged Broker returned no MySQL SQL result"))
	}
	return *value.SQLResult, err
}
func (backend *MySQLBackend) Status(ctx context.Context, instance mysqlmanager.Instance) (mysqlmanager.Status, error) {
	value, err := backend.call(ctx, operationMySQLStatus, mysqlWireRequest{Instance: instance})
	if value.Status == nil {
		return mysqlmanager.Status{}, errors.Join(err, errors.New("privileged Broker returned no MySQL status"))
	}
	return *value.Status, err
}
func (backend *MySQLBackend) DatabaseExists(ctx context.Context, instance mysqlmanager.Instance, database string) (bool, error) {
	value, err := backend.call(ctx, operationMySQLExists, mysqlWireRequest{Instance: instance, Database: database})
	return value.Exists, err
}
func (backend *MySQLBackend) CreateDatabase(ctx context.Context, instance mysqlmanager.Instance, input mysqlmanager.CreateDatabaseInput) error {
	_, err := backend.call(ctx, operationMySQLCreate, mysqlWireRequest{Instance: instance, Create: input})
	return err
}
func (backend *MySQLBackend) ReplaceDatabase(ctx context.Context, instance mysqlmanager.Instance, database string) error {
	_, err := backend.call(ctx, operationMySQLReplace, mysqlWireRequest{Instance: instance, Database: database})
	return err
}
func (backend *MySQLBackend) DropDatabase(ctx context.Context, instance mysqlmanager.Instance, database string) error {
	_, err := backend.call(ctx, operationMySQLDrop, mysqlWireRequest{Instance: instance, Database: database})
	return err
}
func (backend *MySQLBackend) ClearDatabase(ctx context.Context, instance mysqlmanager.Instance, database string) error {
	_, err := backend.call(ctx, operationMySQLClear, mysqlWireRequest{Instance: instance, Database: database})
	return err
}
func (backend *MySQLBackend) Dump(ctx context.Context, instance mysqlmanager.Instance, database, path string) (mysqlmanager.DumpResult, error) {
	value, err := backend.call(ctx, operationMySQLDump, mysqlWireRequest{Instance: instance, Database: database, Path: path})
	if value.Dump == nil {
		return mysqlmanager.DumpResult{}, errors.Join(err, errors.New("privileged Broker returned no MySQL dump result"))
	}
	return *value.Dump, err
}
func (backend *MySQLBackend) Import(ctx context.Context, instance mysqlmanager.Instance, database, path string) error {
	_, err := backend.call(ctx, operationMySQLImport, mysqlWireRequest{Instance: instance, Database: database, Path: path})
	return err
}
func (backend *MySQLBackend) Tools() mysqlmanager.ToolSettings {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.tools
}
func (backend *MySQLBackend) SetTools(ctx context.Context, tools mysqlmanager.ToolSettings) error {
	if _, err := backend.call(ctx, operationMySQLSetTools, mysqlWireRequest{Instance: mysqlmanager.Instance{ID: "tools"}, Tools: tools}); err != nil {
		return err
	}
	backend.mu.Lock()
	backend.tools = tools
	backend.mu.Unlock()
	return nil
}
func (backend *MySQLBackend) TestTools(ctx context.Context) mysqlmanager.ToolStatus {
	value, _ := backend.call(ctx, operationMySQLTestTools, mysqlWireRequest{Instance: mysqlmanager.Instance{ID: "tools"}})
	if value.ToolStatus == nil {
		return mysqlmanager.ToolStatus{}
	}
	return *value.ToolStatus
}

func (backend *MySQLBackend) CancelOperation(ctx context.Context, id string) error {
	_, err := backend.call(ctx, operationMySQLCancel, mysqlWireRequest{Instance: mysqlmanager.Instance{ID: "operations"}, OperationID: id})
	return err
}

var _ mysqlmanager.Backend = (*MySQLBackend)(nil)
var _ mysqlmanager.QueryBackend = (*MySQLBackend)(nil)
var _ mysqlmanager.QueryBackend = (*brokerMySQLService)(nil)
