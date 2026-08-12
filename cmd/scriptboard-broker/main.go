package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/hostsecurity"
	"scriptboard/internal/mfa"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/passkey"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/providercredential"
	"scriptboard/internal/remotewebsite"
	"scriptboard/internal/secretredaction"
	"scriptboard/internal/secretstore"
	"scriptboard/internal/shutdownsignal"
)

func main() {
	if handled, err := runAsWindowsService(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, secretredaction.String("privileged Broker service error: "+err.Error()))
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, secretredaction.String("privileged Broker error: "+err.Error()))
		os.Exit(1)
	}
}

func run(arguments []string) error {
	ctx, stop := shutdownsignal.Context(context.Background())
	defer stop()
	return runContext(ctx, arguments)
}

func runContext(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("scriptboard-broker", flag.ContinueOnError)
	stateRoot := flags.String("state-root", "", "absolute ScriptBoard State Root")
	endpoint := flags.String("endpoint", "", "local IPC endpoint override")
	allowedIdentity := flags.String("allowed-identity", "", "authorized Web service OS identity")
	developmentCurrentUser := flags.Bool("development-current-user", false, "authorize the current OS user for local development")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected privileged Broker arguments: %v", flags.Args())
	}
	absolute, err := filepath.Abs(strings.TrimSpace(*stateRoot))
	if err != nil || strings.TrimSpace(*stateRoot) == "" || !filepath.IsAbs(*stateRoot) {
		return errors.New("privileged Broker requires an absolute --state-root")
	}
	database, err := openBrokerDatabase(absolute)
	if err != nil {
		return err
	}
	defer database.Close()
	audit := auditlog.New(database)
	if _, err := audit.Verify(context.Background()); err != nil {
		return fmt.Errorf("verify audit chain before privileged Broker startup: %w", err)
	}
	vault, err := secretstore.New(absolute)
	if err != nil {
		return err
	}
	checkpoint, err := auditcheckpoint.New(auditcheckpoint.Options{StateRoot: absolute, SecretStore: vault})
	if err != nil {
		return err
	}
	if err := checkpoint.VerifyOrBootstrap(context.Background(), audit, time.Now().UTC()); err != nil {
		return fmt.Errorf("verify signed audit checkpoint before privileged Broker startup: %w", err)
	}
	mfaStore, err := mfa.New(mfa.Options{StateRoot: absolute, SecretStore: vault})
	if err != nil {
		return fmt.Errorf("initialize Broker-owned MFA state: %w", err)
	}
	passkeyStore, err := passkey.New(passkey.Options{StateRoot: absolute, SecretStore: vault})
	if err != nil {
		return fmt.Errorf("initialize Broker-owned passkey state: %w", err)
	}
	remoteWebsites, err := remotewebsite.New(remotewebsite.Options{StateRoot: absolute, SecretStore: vault})
	if err != nil {
		return fmt.Errorf("initialize Broker-owned remote website credentials: %w", err)
	}
	providers, err := providercredential.New(providercredential.Options{StateRoot: absolute, SecretStore: vault})
	if err != nil {
		return fmt.Errorf("initialize Broker-owned provider credentials: %w", err)
	}
	if err := providers.MigrateLegacy(context.Background(), database); err != nil {
		return fmt.Errorf("migrate Assistant provider credentials in Broker: %w", err)
	}
	mysqlExecutionManager, err := mysqlmanager.New(mysqlmanager.Options{DB: database, StateRoot: absolute, SecretStore: vault, Audit: func(event mysqlmanager.AuditEvent) {
		_, appendErr := audit.Append(context.Background(), auditlog.Event{OccurredAt: fmt.Sprintf("%d", time.Now().UTC().Unix()), Action: event.Action,
			Target: event.Target, Result: event.Result, SourceAddress: "local-privileged-broker", ActorUserID: event.Actor.UserID,
			ActorUsername: event.Actor.Username, ActorRole: "system"})
		if appendErr == nil {
			_ = checkpoint.Write(context.Background(), audit, time.Now().UTC())
		}
	}})
	if err != nil {
		return fmt.Errorf("initialize Broker-owned MySQL execution backend: %w", err)
	}
	mysqlService, err := privilegebroker.NewBrokerMySQLService(database, mysqlExecutionManager.ExecutionBackend(), mysqlExecutionManager, mysqlExecutionManager.BackupRoot())
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve privileged Broker executable: %w", err)
	}
	brokerFiles, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: []string{
		absolute, filepath.Dir(vault.KeyPath()), filepath.Dir(executable),
	}})
	if err != nil {
		return fmt.Errorf("configure Broker-owned Host Files access: %w", err)
	}
	if err := brokerFiles.Protect(mysqlExecutionManager.BackupRoot()); err != nil {
		return fmt.Errorf("protect MySQL backup root from Host Files: %w", err)
	}
	hostFilesStagingRoot := filepath.Join(absolute, "inbox", "host-files-broker")
	if err := os.MkdirAll(hostFilesStagingRoot, 0o750); err != nil {
		return fmt.Errorf("prepare Broker Host Files exchange root: %w", err)
	}
	legacyExternal := externaltrigger.New(database, externaltrigger.Options{SecretsDirectory: filepath.Join(absolute, "secrets"), SecretStore: vault})
	hostFileOperationStore, err := privilegebroker.NewBrokerHostFileOperationStore(database)
	if err != nil {
		return err
	}
	hostFileMoveEngine := hostfiles.NewMoveEngine(brokerFiles, hostFileOperationStore)
	if err := hostFileMoveEngine.Recover(context.Background()); err != nil {
		return fmt.Errorf("recover Broker-owned Host Files operations: %w", err)
	}
	hostFilesService, err := privilegebroker.NewBrokerHostFilesServiceWithMoves(brokerFiles, hostFilesStagingRoot, hostFileMoveEngine, ctx, database, legacyExternal)
	if err != nil {
		return err
	}
	if err := legacyExternal.MigrateSecrets(); err != nil {
		return fmt.Errorf("migrate External Interface secrets in Broker: %w", err)
	}
	if err := legacyExternal.MigrateRemoteWebsiteCredentials(context.Background(), remoteWebsites); err != nil {
		return fmt.Errorf("migrate remote website credentials in Broker: %w", err)
	}
	if err := legacyExternal.PurgeLegacyKeySecrets(context.Background()); err != nil {
		return fmt.Errorf("purge recoverable External Interface keys in Broker: %w", err)
	}
	databaseSecurity, err := privilegebroker.NewDatabaseSecurity(database, audit, time.Now)
	if err != nil {
		return err
	}
	databaseSecurity.SetAuditAnchor(checkpoint)
	directHostSecurity := hostsecurity.NewManager(hostsecurity.Options{CapabilityCacheTTL: -1, LoginCacheTTL: -1})
	executor, err := privilegebroker.NewHostSecurityExecutor(directHostSecurity)
	if err != nil {
		return err
	}
	transport, err := privilegebroker.Listen(privilegebroker.TransportOptions{
		StateRoot: absolute, Endpoint: *endpoint, AllowedIdentity: *allowedIdentity,
		DevelopmentCurrentUser: *developmentCurrentUser,
	})
	if err != nil {
		return err
	}
	defer transport.Close()
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		Authorizer: databaseSecurity, Executor: executor, Auditor: databaseSecurity,
		Checkpoint: brokerCheckpointService{store: checkpoint, audit: audit}, Now: time.Now,
		MFA: mfaStore, Passkeys: passkeyStore, RemoteWebsites: remoteWebsites, Providers: providers,
		MySQL: mysqlService, HostFiles: hostFilesService,
	})
	if err != nil {
		return err
	}
	server.Start()
	_ = mysqlExecutionManager.ReconcilePlans(context.Background())
	var mysqlBackground sync.WaitGroup
	mysqlBackground.Add(2)
	go func() {
		defer mysqlBackground.Done()
		_ = mysqlExecutionManager.RecoverInterrupted(ctx)
	}()
	go func() {
		defer mysqlBackground.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = mysqlExecutionManager.RunDuePlans(ctx)
			}
		}
	}()
	<-ctx.Done()
	mysqlBackground.Wait()
	return server.Close()
}

type brokerCheckpointService struct {
	store *auditcheckpoint.Store
	audit *auditlog.Store
}

func (service brokerCheckpointService) Verify(ctx context.Context) (int64, error) {
	if err := service.store.VerifyOrBootstrap(ctx, service.audit, time.Now().UTC()); err != nil {
		return 0, err
	}
	return service.store.CheckpointEventID(), nil
}

func (service brokerCheckpointService) Write(ctx context.Context) (int64, error) {
	if err := service.store.Write(ctx, service.audit, time.Now().UTC()); err != nil {
		return 0, err
	}
	return service.store.CheckpointEventID(), nil
}

func openBrokerDatabase(stateRoot string) (*sql.DB, error) {
	databasePath := filepath.ToSlash(filepath.Join(stateRoot, "app.db"))
	database, err := sql.Open("sqlite", "file:"+databasePath+"?mode=rw&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open privileged Broker database: %w", err)
	}
	var integrity string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = database.Close()
		return nil, fmt.Errorf("privileged Broker database integrity check failed: result=%q error=%v", integrity, err)
	}
	return database, nil
}
