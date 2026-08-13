package mysqlmanager

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Backend is the secret-bearing MySQL execution boundary. Implementations own
// credential storage, database connections, client option files, and client
// process launch. Callers never receive a password or command invocation.
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
	Dump(context.Context, Instance, string, string) (DumpResult, error)
	Import(context.Context, Instance, string, string) error
	Tools() ToolSettings
	SetTools(context.Context, ToolSettings) error
	TestTools(context.Context) ToolStatus
}

type RemoteOperationCanceller interface {
	CancelOperation(context.Context, string) error
}

type DumpResult struct {
	Warning string
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

func (backend *localBackend) Dump(ctx context.Context, instance Instance, database, destinationPath string) (DumpResult, error) {
	manager := backend.manager
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
	output, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
	return result, nil
}

func (backend *localBackend) Import(ctx context.Context, instance Instance, target, sourcePath string) error {
	manager := backend.manager
	optionPath, cleanup, err := manager.clientOptionFile(instance)
	if err != nil {
		return err
	}
	defer cleanup()
	file, err := os.Open(sourcePath)
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
