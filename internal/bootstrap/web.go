package bootstrap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/assistant/pirpc"
	"scriptboard/internal/assistant/runtimehost"
	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/config"
	"scriptboard/internal/customdashboard"
	"scriptboard/internal/installation"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/platformservice"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/runnerhost"
	updatepkg "scriptboard/internal/update"
	webapp "scriptboard/internal/web"
)

// RunWeb loads configuration, composes every Web dependency, and serves until
// shutdown. No partially constructed runtime escapes this interface.
func RunWeb(runContext context.Context, arguments []string, getenv func(string) string, stdout io.Writer) error {
	loaded, err := config.Load(arguments, getenv)
	if err != nil {
		return err
	}
	if err := validateNetworkConfiguration(loaded.Listen, loaded.TLSCert, loaded.TLSKey); err != nil {
		return err
	}
	securityEventToken, err := readSecurityEventToken(loaded.SecurityEventTokenFile)
	if err != nil {
		return err
	}
	installRoot := applicationInstallRoot(loaded.StateRoot)
	dependencies, err := webDependencies(loaded, installRoot)
	if err != nil {
		return err
	}
	updateShutdown := make(chan struct{}, 1)
	var requestRestart func() error
	if canRestartManagedService(loaded.StateRoot, loaded.ConfigPath) {
		requestRestart = func() error { return platformservice.RequestRestart(time.Second) }
	}
	application, err := webapp.Open(webapp.Config{
		StateRoot: loaded.StateRoot, InstallRoot: installRoot, ConfigPath: loaded.ConfigPath, TLSKey: loaded.TLSKey,
		RunTimeoutGrace: loaded.RunTimeoutGrace, ExecutorChains: loaded.ExecutorChains, AdminUsername: loaded.AdminUsername, AdminPasswordFile: loaded.AdminPasswordFile, TrustedProxies: loaded.TrustedProxies,
		AllowedHosts: loaded.AllowedHosts, CanonicalExternalURL: loaded.CanonicalExternalURL,
		SecurityEventEndpoint: loaded.SecurityEventEndpoint, SecurityEventToken: securityEventToken,
		SecurityEventTokenFile: loaded.SecurityEventTokenFile, SecurityEventAllowPrivate: loaded.SecurityEventAllowPrivate,
		NotificationEmailRelayEndpoint: loaded.NotificationEmailRelayEndpoint, NotificationEmailRecipient: loaded.NotificationEmailRecipient,
		NotificationEmailRelayTokenFile: loaded.NotificationEmailRelayTokenFile,
		UpdateCheck:                     loaded.UpdateCheck, UpdateInterval: loaded.UpdateInterval,
		RunnerIdentityMode: loaded.RunnerIdentityMode,
		RequestShutdown: func() {
			select {
			case updateShutdown <- struct{}{}:
			default:
			}
		},
		RequestRestart: requestRestart, AssistantProcessLauncher: dependencies.assistantLauncher,
		RunnerProcessLauncher: dependencies.runnerLauncher, PrivilegedBrokerEndpoint: dependencies.brokerEndpoint,
		ApplicationProbe: dependencies.applicationProbe, KubernetesFactory: dependencies.kubernetesFactory,
		AuditCheckpoint: dependencies.auditCheckpoint, MFAStore: dependencies.mfaStore, PasskeyStore: dependencies.passkeyStore,
		RegistryConnections: dependencies.registryConnections,
		ProviderCredentials: dependencies.providerCredentials, MySQLBackend: dependencies.mysqlBackend,
		HostFilesBackend: dependencies.hostFilesBackend, StateBackups: dependencies.stateBackups,
	})
	if err != nil {
		return err
	}
	defer application.Close()

	listener, err := net.Listen("tcp", loaded.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", loaded.Listen, err)
	}
	defer listener.Close()
	if _, err := updatepkg.WriteRuntimeMarker(loaded.StateRoot, application.ValidationOperationID()); err != nil {
		return fmt.Errorf("write runtime marker: %w", err)
	}
	defer updatepkg.RemoveRuntimeMarker(loaded.StateRoot)

	server := &http.Server{
		Handler: application.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 15 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	scheme := "http"
	if loaded.TLSCert != "" {
		scheme = "https"
	}
	fmt.Fprintln(stdout, "ScriptBoard 已启动："+scheme+"://"+listener.Addr().String())
	go func() {
		select {
		case <-runContext.Done():
		case <-updateShutdown:
		}
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(runContext), 30*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if loaded.TLSCert != "" {
		err = server.ServeTLS(listener, loaded.TLSCert, loaded.TLSKey)
	} else {
		err = server.Serve(listener)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

type composedWebDependencies struct {
	assistantLauncher   pirpc.ProcessLauncher
	runnerLauncher      runmanager.ProcessLauncher
	auditCheckpoint     webapp.AuditCheckpoint
	mfaStore            webapp.MFAStore
	passkeyStore        webapp.PasskeyStore
	registryConnections customdashboard.RegistryConnections
	providerCredentials *privilegebroker.ProviderCredentials
	mysqlBackend        mysqlmanager.Backend
	hostFilesBackend    *privilegebroker.HostFilesBackend
	stateBackups        webapp.StateBackupService
	applicationProbe    appstatus.Probe
	kubernetesFactory   clusterstatus.Factory
	brokerEndpoint      string
}

func webDependencies(loaded config.Config, installRoot string) (composedWebDependencies, error) {
	return webDependenciesWithIdentity(loaded, installRoot, platformservice.ValidateWebRuntimeIdentity)
}

func webDependenciesWithIdentity(loaded config.Config, installRoot string, validateIdentity func() error) (composedWebDependencies, error) {
	var result composedWebDependencies
	if installRoot == "" {
		return result, nil
	}
	if err := validateIdentity(); err != nil {
		return result, fmt.Errorf("refuse managed Web service with unsafe OS identity: %w", err)
	}
	endpoint, err := runtimehost.DefaultEndpoint(loaded.StateRoot)
	if err != nil {
		return result, fmt.Errorf("resolve isolated Runtime Host endpoint: %w", err)
	}
	assistantDial := runtimehost.Dial(endpoint)
	result.assistantLauncher = runtimehost.NewClientLauncher(func(ctx context.Context) (net.Conn, error) {
		return connectOnDemandHost(ctx, platformservice.EnsureAIRuntimeHostRunning, assistantDial, "isolated AI Runtime Host")
	})
	runnerEndpoint, err := runnerhost.DefaultEndpoint(loaded.StateRoot)
	if err != nil {
		return result, fmt.Errorf("resolve Runner Host endpoint: %w", err)
	}
	runnerDial := runnerhost.Dial(runnerEndpoint)
	result.runnerLauncher = runnerhost.NewClientLauncher(func(ctx context.Context) (net.Conn, error) {
		return connectOnDemandHost(ctx, platformservice.EnsureRunnerHostRunning, runnerDial, "Runner Host")
	}, runnerRuntimeIdentity(loaded.RunnerIdentityMode))
	result.brokerEndpoint, err = privilegebroker.DefaultEndpoint(loaded.StateRoot)
	if err != nil {
		return result, fmt.Errorf("resolve privileged Broker endpoint: %w", err)
	}
	client := privilegebroker.NewClient(privilegebroker.ClientOptions{Dial: privilegebroker.Dial(result.brokerEndpoint)})
	result.auditCheckpoint = privilegebroker.NewRemoteCheckpoint(client)
	result.mfaStore = privilegebroker.NewRemoteMFA(client)
	result.passkeyStore = privilegebroker.NewRemotePasskey(client)
	result.registryConnections = privilegebroker.NewRegistryConnections(client)
	result.providerCredentials = privilegebroker.NewProviderCredentials(client)
	result.mysqlBackend = privilegebroker.NewMySQLBackend(client, mysqlmanager.ToolSettings{DumpExecutable: "mysqldump", ClientExecutable: "mysql"})
	result.hostFilesBackend = privilegebroker.NewHostFilesBackend(client, filepath.Join(loaded.StateRoot, "inbox", "host-files-broker"))
	result.stateBackups = privilegebroker.NewStateBackups(client)
	result.applicationProbe, result.kubernetesFactory = brokerRuntimeDependencies(client)
	return result, nil
}

func brokerRuntimeDependencies(client *privilegebroker.Client) (appstatus.Probe, clusterstatus.Factory) {
	return privilegebroker.NewApplicationProbe(client), privilegebroker.NewKubernetesFactory(client)
}

func runnerRuntimeIdentity(mode string) string {
	if mode == config.RunnerIdentityIsolated {
		return "scriptboard-runner"
	}
	if runtime.GOOS == "windows" {
		return "LocalSystem"
	}
	return "root"
}

func connectOnDemandHost(ctx context.Context, start func(context.Context) error, dial func(context.Context) (net.Conn, error), label string) (net.Conn, error) {
	// SCM/systemd may report Running before the host has bound its IPC endpoint.
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := start(readyCtx); err != nil {
		return nil, fmt.Errorf("start %s on demand: %w", label, err)
	}
	var lastErr error
	for {
		attemptCtx, cancelAttempt := context.WithTimeout(readyCtx, 250*time.Millisecond)
		connection, err := dial(attemptCtx)
		cancelAttempt()
		if err == nil {
			return connection, nil
		}
		lastErr = err
		select {
		case <-readyCtx.Done():
			return nil, fmt.Errorf("connect to %s after demand start: %w", label, errors.Join(readyCtx.Err(), lastErr))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func readSecurityEventToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read security event token file: %w", err)
	}
	if len(body) > 4096 || strings.TrimSpace(string(body)) == "" {
		return "", errors.New("security event token file must contain 1 to 4096 bytes")
	}
	return strings.TrimSpace(string(body)), nil
}

func validateNetworkConfiguration(address, certificate, key string) error {
	if (certificate == "") != (key == "") {
		return errors.New("TLS certificate and key must be configured together")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if certificate != "" {
		if _, err := tls.LoadX509KeyPair(certificate, key); err != nil {
			return fmt.Errorf("invalid TLS certificate or key: %w", err)
		}
	}
	return nil
}

func applicationInstallRoot(stateRoot string) string {
	metadata, err := installation.Detect(stateRoot)
	if err != nil {
		return ""
	}
	return metadata.InstallRoot
}

func canRestartManagedService(stateRoot, configPath string) bool {
	metadata, err := installation.Detect(stateRoot)
	if err != nil {
		return false
	}
	loaded, loadErr := config.Load([]string{"--config", metadata.ConfigPath}, os.Getenv)
	if loadErr != nil {
		return false
	}
	matches, err := platformservice.MatchesExecutable(installation.ServiceEntryExecutable(metadata), metadata.ConfigPath, metadata.StateRoot, loaded.RunnerIdentityMode)
	if err != nil || !matches {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	currentExecutable, currentErr := os.Stat(executable)
	serviceExecutable, serviceErr := os.Stat(installation.ServiceEntryExecutable(metadata))
	currentConfig, currentConfigErr := os.Stat(configPath)
	serviceConfig, serviceConfigErr := os.Stat(metadata.ConfigPath)
	return currentErr == nil && serviceErr == nil && os.SameFile(currentExecutable, serviceExecutable) &&
		currentConfigErr == nil && serviceConfigErr == nil && os.SameFile(currentConfig, serviceConfig)
}
