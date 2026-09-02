package mysqlmanager

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Backend is the secret-bearing MySQL execution boundary. Implementations own
// credential storage, database connections, client option files, backup
// artifacts, and client process launch. Callers never receive a password,
// command invocation, or direct filesystem handle for an artifact.
type Backend interface {
	StoreCredential(context.Context, Instance, string) error
	DeleteCredential(context.Context, string) error
	Test(context.Context, Instance) (ConnectionTest, error)
	Databases(context.Context, Instance) ([]Database, error)
	Status(context.Context, Instance) (Status, error)
	DatabaseExists(context.Context, Instance, string) (bool, error)
	CreateDatabase(context.Context, Instance, CreateDatabaseInput) error
	ReplaceDatabase(context.Context, Instance, string) error
	DropDatabase(context.Context, Instance, string) error
	ClearDatabase(context.Context, Instance, string) error
	Dump(context.Context, Instance, string, string) (DumpResult, error)
	Import(context.Context, Instance, string, string) error
	PrepareArtifactRoot(context.Context, string) error
	StoreArtifact(context.Context, string, io.Reader, bool) (ArtifactResult, error)
	VerifyArtifact(context.Context, string, string, bool) error
	DeleteArtifact(context.Context, string) error
	CleanupArtifacts(context.Context, string) error
	DownloadBackup(context.Context, string, io.Writer) (string, int64, error)
	Tools() ToolSettings
	SetTools(context.Context, ToolSettings) error
	TestTools(context.Context) ToolStatus
}

type RemoteOperationCanceller interface {
	CancelOperation(context.Context, string) error
}

type DumpResult struct {
	Warning   string
	SizeBytes int64
	SHA256    string
}

func NewLocalBackend(options Options) (Backend, error) {
	manager, err := New(options)
	if err != nil {
		return nil, err
	}
	return manager.backend, nil
}

func (m *Manager) ExecutionBackend() Backend {
	return m.backend
}

// localBackend preserves standalone behavior while keeping the Manager on the
// same capability-safe interface used by the managed deployment backend.
type localBackend struct{ manager *Manager }

func (m *Manager) clientOptionFile(instance Instance) (string, func(), error) {
	password, err := m.secrets.getForInstance(instance)
	if err != nil {
		return "", func() {}, err
	}
	directory := filepath.Join(m.stateRoot, "secrets")
	file, err := os.CreateTemp(directory, ".mysql-client-*.cnf")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	_ = file.Chmod(0o600)
	escape := func(value string) string {
		value = strings.ReplaceAll(value, "\\", "\\\\")
		value = strings.ReplaceAll(value, "\n", "\\n")
		value = strings.ReplaceAll(value, "\r", "\\r")
		return strings.ReplaceAll(value, "\"", "\\\"")
	}
	sslMode := map[TLSMode]string{TLSDisabled: "DISABLED", TLSPreferred: "PREFERRED", TLSRequired: "REQUIRED", TLSVerifyIdentity: "VERIFY_IDENTITY"}[instance.TLSMode]
	body := fmt.Sprintf("[client]\nhost=\"%s\"\nport=%d\nuser=\"%s\"\npassword=\"%s\"\nssl-mode=%s\n",
		escape(instance.Host), instance.Port, escape(instance.Username), escape(password), sslMode)
	if instance.CAPath != "" {
		body += "ssl-ca=\"" + escape(instance.CAPath) + "\"\n"
	}
	if _, err := io.WriteString(file, body); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func (backend *localBackend) StoreCredential(_ context.Context, instance Instance, password string) error {
	return backend.manager.secrets.set(instance, password)
}

func (backend *localBackend) DeleteCredential(_ context.Context, id string) error {
	return backend.manager.secrets.delete(id)
}

func (backend *localBackend) Test(ctx context.Context, instance Instance) (ConnectionTest, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return ConnectionTest{}, err
	}
	return backend.manager.server.Test(ctx, instance, password)
}

func (backend *localBackend) Databases(ctx context.Context, instance Instance) ([]Database, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return nil, err
	}
	return backend.manager.server.Databases(ctx, instance, password)
}

func (backend *localBackend) DatabasesIncludingSystem(ctx context.Context, instance Instance) ([]Database, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return nil, err
	}
	server, ok := backend.manager.server.(queryDatabaseServer)
	if !ok {
		return nil, errors.New("MySQL query browsing is unavailable")
	}
	return server.DatabasesIncludingSystem(ctx, instance, password)
}

func (backend *localBackend) Objects(ctx context.Context, instance Instance, database string) ([]DatabaseObject, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return nil, err
	}
	server, ok := backend.manager.server.(queryDatabaseServer)
	if !ok {
		return nil, errors.New("MySQL query browsing is unavailable")
	}
	return server.Objects(ctx, instance, password, database)
}

func (backend *localBackend) ObjectDetails(ctx context.Context, instance Instance, database, object string) (ObjectDetails, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return ObjectDetails{}, err
	}
	server, ok := backend.manager.server.(queryDatabaseServer)
	if !ok {
		return ObjectDetails{}, errors.New("MySQL query browsing is unavailable")
	}
	return server.ObjectDetails(ctx, instance, password, database, object)
}

func (backend *localBackend) ExecuteSQL(ctx context.Context, instance Instance, request SQLRequest) (SQLResult, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return SQLResult{}, err
	}
	server, ok := backend.manager.server.(queryDatabaseServer)
	if !ok {
		return SQLResult{}, errors.New("MySQL query browsing is unavailable")
	}
	return server.ExecuteSQL(ctx, instance, password, request)
}

func (backend *localBackend) Status(ctx context.Context, instance Instance) (Status, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return Status{}, err
	}
	return backend.manager.server.Status(ctx, instance, password)
}

func (backend *localBackend) DatabaseExists(ctx context.Context, instance Instance, name string) (bool, error) {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return false, err
	}
	return backend.manager.server.DatabaseExists(ctx, instance, password, name)
}

func (backend *localBackend) CreateDatabase(ctx context.Context, instance Instance, input CreateDatabaseInput) error {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return err
	}
	return backend.manager.server.CreateDatabase(ctx, instance, password, input)
}

func (backend *localBackend) ReplaceDatabase(ctx context.Context, instance Instance, name string) error {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return err
	}
	return backend.manager.server.ReplaceDatabase(ctx, instance, password, name)
}

func (backend *localBackend) DropDatabase(ctx context.Context, instance Instance, name string) error {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return err
	}
	return backend.manager.server.DropDatabase(ctx, instance, password, name)
}

func (backend *localBackend) ClearDatabase(ctx context.Context, instance Instance, name string) error {
	password, err := backend.manager.secrets.getForInstance(instance)
	if err != nil {
		return err
	}
	return backend.manager.server.ClearDatabase(ctx, instance, password, name)
}

func (backend *localBackend) Dump(ctx context.Context, instance Instance, database, destinationPath string) (DumpResult, error) {
	manager := backend.manager
	directory := filepath.Dir(destinationPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return DumpResult{}, err
	}
	temporary, err := os.CreateTemp(directory, ".mysql-backup-*.partial")
	if err != nil {
		return DumpResult{}, err
	}
	temporaryPath := temporary.Name()
	if closeErr := temporary.Close(); closeErr != nil {
		_ = os.Remove(temporaryPath)
		return DumpResult{}, closeErr
	}
	if removeErr := os.Remove(temporaryPath); removeErr != nil {
		return DumpResult{}, removeErr
	}
	defer os.Remove(temporaryPath)
	optionPath, cleanup, err := manager.clientOptionFile(instance)
	if err != nil {
		return DumpResult{}, err
	}
	defer cleanup()
	password, err := manager.secrets.getForInstance(instance)
	if err != nil {
		return DumpResult{}, err
	}
	result := DumpResult{}
	if tables, tableErr := manager.server.NonTransactionalTables(ctx, instance, password, database); tableErr == nil && len(tables) > 0 {
		result.Warning = fmt.Sprintf("%d non-InnoDB tables are not covered by the consistent transaction snapshot", len(tables))
	}
	output, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return DumpResult{}, err
	}
	compressed := gzip.NewWriter(output)
	stderr := &boundedBuffer{maximum: 64 << 10}
	runErr := manager.runner.Run(ctx, manager.localTools().DumpExecutable, manager.dumpArguments(ctx, optionPath, database), nil, compressed, stderr)
	closeErr := compressed.Close()
	if syncErr := output.Sync(); closeErr == nil {
		closeErr = syncErr
	}
	if fileCloseErr := output.Close(); closeErr == nil {
		closeErr = fileCloseErr
	}
	if runErr != nil || closeErr != nil {
		cause := runErr
		if cause == nil {
			cause = closeErr
		}
		return DumpResult{}, fmt.Errorf("mysqldump failed: %w%s", cause, sanitizedCommandError(stderr.String(), password, optionPath))
	}
	info, err := os.Stat(temporaryPath)
	if err != nil {
		return DumpResult{}, err
	}
	if info.Size() == 0 {
		return DumpResult{}, errors.New("mysqldump produced an empty backup")
	}
	hash, err := fileSHA256(temporaryPath)
	if err != nil {
		return DumpResult{}, err
	}
	if err := commitArtifactNoReplace(temporaryPath, destinationPath); err != nil {
		return DumpResult{}, err
	}
	result.SizeBytes, result.SHA256 = info.Size(), hash
	return result, nil
}

func (*localBackend) DeleteArtifact(_ context.Context, path string) error {
	var result error
	for _, candidate := range []string{path, path + ".upload.partial"} {
		info, statErr := os.Lstat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			result = errors.Join(result, statErr)
			continue
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			result = errors.Join(result, errors.New("MySQL backup artifact is not a regular file"))
			continue
		}
		if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (backend *localBackend) Import(ctx context.Context, instance Instance, target, sourcePath string) error {
	manager := backend.manager
	optionPath, cleanup, err := manager.clientOptionFile(instance)
	if err != nil {
		return err
	}
	defer cleanup()
	file, err := openRegularArtifact(sourcePath)
	if err != nil {
		return err
	}
	defer file.Close()
	var input io.Reader = file
	if strings.HasSuffix(strings.ToLower(sourcePath), ".gz") {
		compressed, gzipErr := gzip.NewReader(file)
		if gzipErr != nil {
			return gzipErr
		}
		defer compressed.Close()
		input = compressed
	}
	stderr := &boundedBuffer{maximum: 64 << 10}
	if err := manager.runner.Run(ctx, manager.localTools().ClientExecutable, mysqlImportArguments(optionPath, target), input, io.Discard, stderr); err != nil {
		password, _ := manager.secrets.getForInstance(instance)
		return fmt.Errorf("mysql import failed: %w%s", err, sanitizedCommandError(stderr.String(), password, optionPath))
	}
	return nil
}

func (backend *localBackend) Tools() ToolSettings { return backend.manager.localTools() }

func (backend *localBackend) SetTools(ctx context.Context, settings ToolSettings) error {
	return backend.manager.setLocalTools(ctx, settings)
}

func (backend *localBackend) TestTools(ctx context.Context) ToolStatus {
	return backend.manager.testLocalTools(ctx)
}

var _ Backend = (*localBackend)(nil)
var _ QueryBackend = (*localBackend)(nil)
