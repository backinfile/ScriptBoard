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
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/hostsecurity"
	"scriptboard/internal/privilegebroker"
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
	})
	if err != nil {
		return err
	}
	server.Start()
	<-ctx.Done()
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
