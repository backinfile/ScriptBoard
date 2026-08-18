package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/auditnotification"
	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/config"
	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/hostsecurity"
	"scriptboard/internal/mfa"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/passkey"
	"scriptboard/internal/privatepath"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/providercredential"
	"scriptboard/internal/registryconnection"
	"scriptboard/internal/secretstore"
	"scriptboard/internal/securityevents"
	"scriptboard/internal/statebackup"
)

func RunBroker(ctx context.Context, arguments []string, getenv func(string) string) error {
	flags := flag.NewFlagSet("scriptboard-broker", flag.ContinueOnError)
	stateRoot := flags.String("state-root", "", "absolute ScriptBoard State Root")
	configPath := flags.String("config", "", "absolute ScriptBoard configuration path")
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
	var emailConfig config.Config
	if strings.TrimSpace(*configPath) != "" {
		if !filepath.IsAbs(*configPath) {
			return errors.New("privileged Broker requires an absolute --config path")
		}
		emailConfig, err = config.Load([]string{"--config", *configPath}, getenv)
		if err != nil {
			return fmt.Errorf("load Broker notification configuration: %w", err)
		}
		configuredRoot, rootErr := filepath.Abs(emailConfig.StateRoot)
		if rootErr != nil || !samePath(configuredRoot, absolute) {
			return errors.New("Broker --state-root does not match the configured State Root")
		}
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
	var emailManager *securityevents.Manager
	var emailPoller *auditnotification.Poller
	if emailConfig.NotificationEmailRelayEndpoint != "" {
		if err := ProtectBrokerSecretDirectory(emailConfig.NotificationEmailRelayTokenFile); err != nil {
			return fmt.Errorf("protect Broker-owned email relay credentials: %w", err)
		}
		token, tokenErr := ReadBrokerSecretFile(emailConfig.NotificationEmailRelayTokenFile)
		if tokenErr != nil {
			return fmt.Errorf("read Broker-owned email relay token: %w", tokenErr)
		}
		emailManager, err = securityevents.New(securityevents.Options{
			StateRoot: absolute, Endpoint: emailConfig.NotificationEmailRelayEndpoint, Token: token,
			AllowPrivate: emailConfig.NotificationEmailRelayAllowPrivate, Channel: "email-outbox",
			EnvelopeType: "scriptboard.email-notification", Recipient: emailConfig.NotificationEmailRecipient,
			NotificationsOnly: true, DisableLocalAlerts: true,
		})
		if err != nil {
			return fmt.Errorf("configure Broker-owned email relay: %w", err)
		}
		defer emailManager.Close()
		emailPoller, err = auditnotification.New(auditnotification.Options{DB: database, StateRoot: absolute, Observe: emailManager.Queue})
		if err != nil {
			return fmt.Errorf("configure Broker-owned audit notification cursor: %w", err)
		}
		if err := emailPoller.Start(ctx); err != nil {
			return fmt.Errorf("start Broker-owned email notifications: %w", err)
		}
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
	providers, err := providercredential.New(providercredential.Options{StateRoot: absolute, SecretStore: vault})
	if err != nil {
		return fmt.Errorf("initialize Broker-owned provider credentials: %w", err)
	}
	if err := providers.MigrateLegacy(context.Background(), database); err != nil {
		return fmt.Errorf("migrate Assistant provider credentials in Broker: %w", err)
	}
	registryConnections, err := registryconnection.New(registryconnection.Options{StateRoot: absolute, SecretStore: vault})
	if err != nil {
		return fmt.Errorf("initialize Broker-owned Registry connections: %w", err)
	}
	if err := registryConnections.MigrateLegacy(context.Background(), database, filepath.Join(absolute, "secrets")); err != nil {
		return fmt.Errorf("migrate Registry connections in Broker: %w", err)
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
	protectedPaths := []string{absolute, filepath.Dir(vault.KeyPath()), filepath.Dir(executable)}
	if emailConfig.NotificationEmailRelayTokenFile != "" {
		protectedPaths = append(protectedPaths, emailConfig.NotificationEmailRelayTokenFile)
	}
	brokerFiles, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: protectedPaths})
	if err != nil {
		return fmt.Errorf("configure Broker-owned Host Files access: %w", err)
	}
	if err := brokerFiles.Protect(mysqlExecutionManager.BackupRoot()); err != nil {
		return fmt.Errorf("protect MySQL backup root from Host Files: %w", err)
	}
	stateRestoreStagingRoot, err := statebackup.StagingRoot(absolute)
	if err != nil {
		return fmt.Errorf("resolve protected state restore staging root: %w", err)
	}
	if err := brokerFiles.Protect(stateRestoreStagingRoot); err != nil {
		return fmt.Errorf("protect state restore staging root from Host Files: %w", err)
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
	applications := appstatus.NewSystemProbe()
	server, err := privilegebroker.NewServer(privilegebroker.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		Authorizer: databaseSecurity, Executor: executor, Auditor: databaseSecurity,
		Checkpoint: brokerCheckpointService{store: checkpoint, audit: audit}, Now: time.Now,
		MFA: mfaStore, Passkeys: passkeyStore, Providers: providers,
		MySQL: mysqlService, HostFiles: hostFilesService,
		Registry:     registryConnections,
		Applications: applications, Kubernetes: brokerKubernetesService{db: database, factory: clusterstatus.HTTPFactory{}},
		StateBackups: &brokerStateBackupService{stateRoot: absolute, database: database, checkpoint: checkpoint, audit: audit},
	})
	if err != nil {
		_ = applications.Close()
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
	if emailPoller != nil {
		emailPoller.Wait()
	}
	return server.Close()
}

type brokerKubernetesService struct {
	db      *sql.DB
	factory clusterstatus.Factory
}

func (service brokerKubernetesService) Open(ctx context.Context, connection clusterstatus.Connection) (clusterstatus.Client, error) {
	return service.factory.Open(ctx, connection)
}

func (service brokerKubernetesService) ResolveConnection(ctx context.Context, id string) (clusterstatus.Connection, bool, error) {
	var connection clusterstatus.Connection
	err := service.db.QueryRowContext(ctx, `SELECT id, name, kubeconfig_path, context_name, operation_mode FROM kubernetes_connection WHERE id=?`, id).Scan(
		&connection.ID, &connection.Name, &connection.KubeconfigPath, &connection.Context, &connection.Mode,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return clusterstatus.Connection{}, false, nil
	}
	return connection, err == nil, err
}

func (service brokerKubernetesService) OpenCandidate(ctx context.Context, connection clusterstatus.Connection) (clusterstatus.Client, error) {
	factory, ok := service.factory.(interface {
		OpenCandidate(context.Context, clusterstatus.Connection) (clusterstatus.Client, error)
	})
	if !ok {
		return nil, errors.New("Kubernetes factory does not support candidate connections")
	}
	return factory.OpenCandidate(ctx, connection)
}

func samePath(first, second string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(first), filepath.Clean(second))
	}
	return filepath.Clean(first) == filepath.Clean(second)
}

func ReadBrokerSecretFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("email relay token file must be an absolute path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4096 {
		return "", errors.New("email relay token file must be a regular file containing 1 to 4096 bytes")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(body))
	if token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("email relay token is invalid")
	}
	return token, nil
}

func ProtectBrokerSecretDirectory(path string) error {
	directory := filepath.Dir(strings.TrimSpace(path))
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Base(directory), "broker-secrets") {
		return errors.New("email relay token must be inside a dedicated broker-secrets directory")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("email relay credential directory must be a real directory")
	}
	return privatepath.ProtectDirectory(directory)
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
