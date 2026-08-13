package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"scriptboard/internal/identity"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/argon2"
	"scriptboard/internal/appstatus"
	"scriptboard/internal/assistant"
	"scriptboard/internal/assistant/pirpc"
	"scriptboard/internal/assistant/raster"
	"scriptboard/internal/assistant/runtimeinstall"
	"scriptboard/internal/assistant/toolbroker"
	auditdomain "scriptboard/internal/audit"
	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/customdashboard"
	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/hostsecurity"
	"scriptboard/internal/hoststatus"
	"scriptboard/internal/instancelock"
	"scriptboard/internal/mfa"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/passkey"
	"scriptboard/internal/privatepath"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/registryconnection"
	"scriptboard/internal/remotewebsite"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/scheduler"
	"scriptboard/internal/secretredaction"
	"scriptboard/internal/secretstore"
	"scriptboard/internal/securitybaseline"
	"scriptboard/internal/securityevents"
	"scriptboard/internal/servicelogs"
	"scriptboard/internal/statebackup"
	"scriptboard/internal/store/migrations"
	storesqlite "scriptboard/internal/store/sqlite"
	updatepkg "scriptboard/internal/update"
	"scriptboard/internal/uploadinbox"
	"scriptboard/internal/websitemonitor"
)

const initialPasswordFilename = "initial-admin-password"
const currentSchemaVersion = buildinfo.DatabaseSchemaVersion

const (
	passwordMemory                         uint32 = 64 * 1024
	passwordIterations                     uint32 = 3
	passwordParallelism                    uint8  = 2
	passwordSaltLength                            = 16
	passwordKeyLength                             = 32
	maxPasswordBytes                              = 256
	maxLoginRequestBytes                   int64  = 16 << 10
	maxLocaleRequestBytes                  int64  = 4 << 10
	maxFormRequestBytes                    int64  = 8 << 20
	maxAssistantRuntimeOfflineRequestBytes int64  = runtimeinstall.MaxArchiveBytes + runtimeinstall.MaxManifestBytes + runtimeinstall.MaxSignatureBytes + (1 << 20)
	loginRateBucketCount                          = 1 << 14
	maxLoginFailureEntries                        = 2 * loginRateBucketCount
)

const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

var passwordWorkSlots = make(chan struct{}, 2)

const unauthenticatedFormReadTimeout = 15 * time.Second
const boundedFormReadTimeout = 30 * time.Second

//go:embed ui/assets/* ui/templates/*
var webFiles embed.FS

func mustWebAsset(path string) string {
	content, err := webFiles.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(content)
}

func mustWebTemplate(name string) *template.Template {
	path := "ui/templates/" + name + ".html"
	return template.Must(template.New(name).Funcs(webTemplateFunctions()).Parse(
		mustWebAsset("ui/templates/deferred-region.html") +
			mustWebAsset("ui/templates/settings-navigation.html") +
			mustWebAsset(path),
	))
}

func webTemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"assetVersion": func() string { return webAssetVersion },
		"join":         strings.Join,
		"stringSlice":  func(values ...string) []string { return values },
		"addInt":       func(value, delta int) int { return value + delta },
		"shortDigest": func(value string) string {
			if len(value) <= 12 {
				return value
			}
			return value[:12] + "…"
		},
		"displayTime": func(input any) string {
			value, ok := input.(time.Time)
			if pointer, pointerOK := input.(*time.Time); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
			if !ok {
				return "-"
			}
			if value.IsZero() {
				return "-"
			}
			return value.Local().Format("2006-01-02 15:04:05")
		},
		"machineTime": func(value time.Time) string {
			if value.IsZero() {
				return ""
			}
			return value.UTC().Format(time.RFC3339)
		},
		"mysqlOperationCancelable": func(phase string) bool { return !mysqlOperationTerminal(phase) },
		"humanBytes":               humanBytes,
		"humanRate":                func(value float64) string { return humanBytes(uint64(math.Max(0, value))) + "/s" },
		"percent":                  func(value float64) string { return fmt.Sprintf("%.1f%%", value) },
		"applicationSortURL":       applicationSortURL,
		"containerSortURL":         containerSortURL,
		"containerStatusURL":       containerStatusURL,
		"kubernetesSortURL":        kubernetesSortURL,
		"kubernetesStatusURL":      kubernetesStatusURL,
		"serviceLogService":        serviceLogServiceLabel,
		"duration":                 humanDuration,
		"localDuration": func(locale webLocale, value time.Duration) string {
			if locale == localeSimplifiedChinese {
				return humanDuration(value)
			}
			if value < 0 {
				value = 0
			}
			days := int(value / (24 * time.Hour))
			value %= 24 * time.Hour
			hours := int(value / time.Hour)
			value %= time.Hour
			minutes := int(value / time.Minute)
			if days > 0 {
				return fmt.Sprintf("%d d %d hr", days, hours)
			}
			if hours > 0 {
				return fmt.Sprintf("%d hr %d min", hours, minutes)
			}
			return fmt.Sprintf("%d min", minutes)
		},
		"slice": func(values ...string) []string { return values },
		"jsonText": func(value json.RawMessage) string {
			var output bytes.Buffer
			if json.Indent(&output, value, "", "  ") == nil {
				return output.String()
			}
			return string(value)
		},
		"deref": func(value *float64) float64 {
			if value == nil {
				return 0
			}
			return *value
		},
		"t": func(locale webLocale, key string) string {
			return webText(locale, key)
		},
		"localTime": func(locale webLocale, input any) string {
			value, ok := input.(time.Time)
			if pointer, pointerOK := input.(*time.Time); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
			if !ok || value.IsZero() {
				return webText(locale, "common.not_available")
			}
			if locale == localeSimplifiedChinese {
				return value.Local().Format("2006年01月02日 15:04")
			}
			return value.Local().Format("Jan 2, 2006 15:04")
		},
		"instanceMachineTime": func(value time.Time) string {
			if value.IsZero() {
				return ""
			}
			return value.Format(time.RFC3339)
		},
		"instanceTime": func(locale webLocale, value time.Time) string {
			if value.IsZero() {
				return webText(locale, "common.not_available")
			}
			if locale == localeSimplifiedChinese {
				return value.Format("2006年1月2日 15:04")
			}
			return value.Format("Jan 2, 2006 15:04")
		},
		"statusText": func(locale webLocale, status string) string {
			if label := webText(locale, "run.status."+status); label != "run.status."+status {
				return label
			}
			return status
		},
		"runSourceText": func(locale webLocale, source string) string {
			key := map[string]string{
				"manual":             "run.source.manual",
				"admin/manual":       "run.source.manual",
				"quick_run":          "run.source.quick_run",
				"admin/quick-run":    "run.source.quick_run",
				"schedule":           "run.source.scheduled",
				"scheduler":          "run.source.scheduled",
				"admin/schedule-now": "run.source.schedule_now",
				"one_time":           "run.source.one_time",
			}[source]
			if key == "" {
				return source
			}
			return webText(locale, key)
		},
		"resultText": func(locale webLocale, result string) string {
			if label := webText(locale, "result."+result); label != "result."+result {
				return label
			}
			return result
		},
		"roleText": func(locale webLocale, role any) string {
			roleName := fmt.Sprint(role)
			if label := webText(locale, "users.role."+roleName); label != "users.role."+roleName {
				return label
			}
			return roleName
		},
		"filesystemRoleText": func(locale webLocale, role string) string {
			if label := webText(locale, "overview.storage_role."+role); label != "overview.storage_role."+role {
				return label
			}
			return role
		},
	}
}

func humanBytes(value any) string {
	var bytes uint64
	switch number := value.(type) {
	case uint64:
		bytes = number
	case uint:
		bytes = uint64(number)
	case int64:
		if number > 0 {
			bytes = uint64(number)
		}
	case int:
		if number > 0 {
			bytes = uint64(number)
		}
	default:
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	amount := float64(bytes)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", bytes, units[unit])
	}
	return fmt.Sprintf("%.1f %s", amount, units[unit])
}

func humanDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	days := int(value / (24 * time.Hour))
	value %= 24 * time.Hour
	hours := int(value / time.Hour)
	value %= time.Hour
	minutes := int(value / time.Minute)
	if days > 0 {
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
	}
	return fmt.Sprintf("%d 分钟", minutes)
}

const (
	sessionCookieName        = "scriptboard_session"
	loginCSRFCookieName      = "scriptboard_login_csrf"
	loginChallengeCookieName = "scriptboard_login_challenge"
)

type contextKey string

const (
	sessionContextKey   contextKey = "session"
	secureContextKey    contextKey = "secure-request"
	requestIDContextKey contextKey = "request-id"
)

type Config struct {
	StateRoot                       string
	ConfigPath                      string
	InstallRoot                     string
	TLSKey                          string
	FileTopology                    hostfiles.Topology
	RunTimeoutGrace                 time.Duration
	SchedulerNow                    func() time.Time
	SchedulerTick                   time.Duration
	ExecutorChains                  map[string][]string
	AdminUsername                   string
	AdminPasswordFile               string
	TrustedProxies                  []string
	AllowedHosts                    []string
	CanonicalExternalURL            string
	WebsiteMonitorOptions           websitemonitor.Options
	CustomDashboardClient           *http.Client
	UpdateCheck                     bool
	UpdateInterval                  time.Duration
	UpdateSource                    updatepkg.ReleaseSource
	RequestShutdown                 func()
	RequestRestart                  func() error
	ApplicationProbe                appstatus.Probe
	KubernetesFactory               clusterstatus.Factory
	AssistantRuntimeSource          runtimeinstall.Source
	HostSecurity                    hostsecurity.Service
	ServiceLogs                     servicelogs.Reader
	PrivilegedBrokerEndpoint        string
	AssistantProcessLauncher        pirpc.ProcessLauncher
	RunnerProcessLauncher           runmanager.ProcessLauncher
	AuditCheckpoint                 AuditCheckpoint
	MFAStore                        MFAStore
	PasskeyStore                    PasskeyStore
	RemoteWebsiteService            RemoteWebsiteService
	RegistryConnections             customdashboard.RegistryConnections
	ProviderCredentials             *privilegebroker.ProviderCredentials
	MySQLBackend                    mysqlmanager.Backend
	HostFilesBackend                *privilegebroker.HostFilesBackend
	StateBackups                    StateBackupService
	SecurityEventEndpoint           string
	SecurityEventToken              string
	SecurityEventTokenFile          string
	SecurityEventAllowPrivate       bool
	SecurityEventClient             *http.Client
	NotificationEmailRelayEndpoint  string
	NotificationEmailRecipient      string
	NotificationEmailRelayTokenFile string
}

type AuditCheckpoint interface {
	VerifyOrBootstrap(context.Context, *auditlog.Store, time.Time) error
	Write(context.Context, *auditlog.Store, time.Time) error
	CheckpointEventID() int64
}

type MFAStore interface {
	Status(string) (mfa.Status, error)
	Begin(string, string) (mfa.Enrollment, error)
	Confirm(string, string) ([]string, error)
	Verify(string, string) (bool, error)
	Reset(string) error
}

type PasskeyStore interface {
	User(string, string) (passkey.User, error)
	List(string) ([]passkey.CredentialView, error)
	Add(string, string, webauthn.Credential) error
	Update(string, webauthn.Credential) error
	Delete(string, string) error
	Reset(string) error
}

type RemoteWebsiteService interface {
	Store(context.Context, string, string, string) error
	Fetch(context.Context, string, string) (json.RawMessage, error)
	Delete(context.Context, string) error
}

type StateBackupService interface {
	Create(context.Context, string, []byte) (statebackup.Artifact, error)
	Inspect(context.Context, string, []byte) (statebackup.Manifest, error)
	Stage(context.Context, string, []byte, string) (statebackup.Stage, error)
	List(context.Context) ([]statebackup.Stage, error)
	Discard(context.Context, string) error
}

type contextualMFAStore interface {
	BeginContext(context.Context, string, string) (mfa.Enrollment, error)
	ConfirmContext(context.Context, string, string) ([]string, error)
	ResetContext(context.Context, string) error
}

type contextualPasskeyStore interface {
	AddContext(context.Context, string, string, webauthn.Credential) error
	DeleteContext(context.Context, string, string) error
	ResetContext(context.Context, string) error
}

func beginMFAWithContext(ctx context.Context, store MFAStore, userID, account string) (mfa.Enrollment, error) {
	if contextual, ok := store.(contextualMFAStore); ok {
		return contextual.BeginContext(ctx, userID, account)
	}
	return store.Begin(userID, account)
}

func confirmMFAWithContext(ctx context.Context, store MFAStore, userID, code string) ([]string, error) {
	if contextual, ok := store.(contextualMFAStore); ok {
		return contextual.ConfirmContext(ctx, userID, code)
	}
	return store.Confirm(userID, code)
}

func resetMFAWithContext(ctx context.Context, store MFAStore, userID string) error {
	if contextual, ok := store.(contextualMFAStore); ok {
		return contextual.ResetContext(ctx, userID)
	}
	return store.Reset(userID)
}

func addPasskeyWithContext(ctx context.Context, store PasskeyStore, userID, name string, credential webauthn.Credential) error {
	if contextual, ok := store.(contextualPasskeyStore); ok {
		return contextual.AddContext(ctx, userID, name, credential)
	}
	return store.Add(userID, name, credential)
}

func deletePasskeyWithContext(ctx context.Context, store PasskeyStore, userID, credentialID string) error {
	if contextual, ok := store.(contextualPasskeyStore); ok {
		return contextual.DeleteContext(ctx, userID, credentialID)
	}
	return store.Delete(userID, credentialID)
}

type App struct {
	db                    *sql.DB
	stateRoot             string
	assistant             *assistant.Service
	assistantRuntime      *assistantRuntimeCoordinator
	assistantRuntimes     *runtimeinstall.Manager
	assistantTools        *assistantToolExecutor
	assistantRaster       *raster.Processor
	assistantBroker       *toolbroker.Broker
	files                 *hostfiles.Manager
	hostFilesBackend      *privilegebroker.HostFilesBackend
	auditLog              *auditlog.Store
	auditCheckpoint       AuditCheckpoint
	securityEvents        *securityevents.Manager
	auditCheckpointStop   context.CancelFunc
	auditCheckpointWG     sync.WaitGroup
	uploadInbox           *uploadinbox.Store
	execUploadExts        map[string]struct{}
	fileOperations        *sqliteFileOperationStore
	fileMoves             *hostfiles.MoveEngine
	fileOperationCtx      context.Context
	fileOperationStop     context.CancelFunc
	fileOperationWG       sync.WaitGroup
	runs                  *runmanager.Manager
	scheduler             *scheduler.Manager
	hostStatus            *hoststatus.Monitor
	hostSecurity          hostsecurity.Service
	securityHistory       *securitybaseline.HistoryStore
	serviceLogs           servicelogs.Reader
	securityDraftMu       sync.Mutex
	securityDrafts        map[string]securityFirewallDraft
	applicationStatus     *appstatus.Monitor
	kubernetesStatus      *clusterstatus.Manager
	logStreamSlots        chan struct{}
	logHistorySlots       chan struct{}
	shellStatusCache      *shellStatusCache
	websiteMonitor        *websitemonitor.Manager
	customDashboards      *customdashboard.Manager
	externalTriggers      *externaltrigger.Manager
	externalReconcileStop context.CancelFunc
	externalReconcileWG   sync.WaitGroup
	remoteWebsites        RemoteWebsiteService
	stateBackups          StateBackupService
	externalAuthLimit     *externaltrigger.Limiter
	externalLimit         *externaltrigger.Limiter
	mysql                 *mysqlmanager.Manager
	mfa                   MFAStore
	passkeys              PasskeyStore
	passkeyCeremonies     *passkeyCeremonyStore
	loginChallenges       *loginChallengeStore
	mysqlContext          context.Context
	mysqlCancel           context.CancelFunc
	mysqlWG               sync.WaitGroup
	instanceLock          *instancelock.Lock
	handler               http.Handler
	routeSpecs            []RouteSpec
	loginMu               sync.Mutex
	loginSlots            chan struct{}
	loginFailures         map[string]loginFailure
	loginLastPrune        time.Time
	loginRateSalt         [32]byte
	activeRequestsMu      sync.Mutex
	activeRequests        map[string]map[uint64]context.CancelFunc
	activeRequestID       uint64
	credentialOverride    bool
	trustedProxies        []*net.IPNet
	allowedHosts          map[string]struct{}
	canonicalExternalURL  string
	updates               *updatepkg.Manager
	requestRestart        func() error
	instanceID            string
	restartRequested      atomic.Bool
	updateCancel          context.CancelFunc
	updateContext         context.Context
	updateResultsWake     chan struct{}
	validation            atomic.Bool
	validationID          string
}

type loginFailure struct {
	count        int
	blockedUntil time.Time
	updatedAt    time.Time
}

func Open(config Config) (*App, error) {
	if err := rejectIncompatibleExistingStateRoot(config.StateRoot); err != nil {
		return nil, err
	}
	stateRoot, err := prepareStateRoot(config.StateRoot)
	if err != nil {
		return nil, err
	}
	credentialStore, err := secretstore.New(stateRoot)
	if err != nil {
		return nil, err
	}
	mfaStore := config.MFAStore
	if mfaStore == nil {
		mfaStore, err = mfa.New(mfa.Options{StateRoot: stateRoot, SecretStore: credentialStore})
		if err != nil {
			return nil, err
		}
	}
	passkeyStore := config.PasskeyStore
	if passkeyStore == nil {
		passkeyStore, err = passkey.New(passkey.Options{StateRoot: stateRoot, SecretStore: credentialStore})
		if err != nil {
			return nil, err
		}
	}
	installRoot := strings.TrimSpace(config.InstallRoot)
	if installRoot == "" {
		if executable, executableErr := os.Executable(); executableErr == nil {
			installRoot = filepath.Dir(executable)
		}
	}
	instanceDigest := sha256.Sum256([]byte(stateRoot))
	files, err := hostfiles.Open(hostfiles.Options{
		ProtectedPaths: []string{stateRoot, filepath.Dir(credentialStore.KeyPath()), installRoot, config.ConfigPath, config.AdminPasswordFile, config.SecurityEventTokenFile, config.NotificationEmailRelayTokenFile, config.TLSKey},
		InstanceID:     hex.EncodeToString(instanceDigest[:]), Topology: config.FileTopology,
	})
	if err != nil {
		return nil, fmt.Errorf("configure host filesystem access: %w", err)
	}
	instanceLock, err := instancelock.Acquire(stateRoot)
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			_ = instanceLock.Close()
		}
	}()
	validationID, validating := updatepkg.PendingValidation(stateRoot, buildinfo.Current())

	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		return nil, err
	}

	trustedProxies, err := parseTrustedProxies(config.TrustedProxies)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	uploadInboxStore, err := uploadinbox.New(filepath.Join(stateRoot, "inbox", "uploads"))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	allowedHosts, err := parseAllowedHosts(config.AllowedHosts)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	var loginRateSalt [32]byte
	if _, err := rand.Read(loginRateSalt[:]); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("生成登录限流密钥: %w", err)
	}
	hostSecurityService := config.HostSecurity
	if hostSecurityService == nil {
		hostSecurityReader := hostsecurity.NewManager(hostsecurity.Options{})
		brokerEndpoint := strings.TrimSpace(config.PrivilegedBrokerEndpoint)
		if brokerEndpoint == "" {
			brokerEndpoint, err = privilegebroker.DefaultEndpoint(stateRoot)
			if err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("resolve privileged Broker endpoint: %w", err)
			}
		}
		brokerClient := privilegebroker.NewClient(privilegebroker.ClientOptions{Dial: privilegebroker.Dial(brokerEndpoint)})
		hostSecurityService, err = privilegebroker.NewHostSecurityService(hostSecurityReader, brokerClient)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure privileged host security Broker: %w", err)
		}
	}
	application := &App{
		db: db, stateRoot: stateRoot, files: files, hostFilesBackend: config.HostFilesBackend, stateBackups: config.StateBackups, uploadInbox: uploadInboxStore, instanceLock: instanceLock, mfa: mfaStore,
		passkeys: passkeyStore, passkeyCeremonies: newPasskeyCeremonyStore(), loginChallenges: newLoginChallengeStore(),
		execUploadExts: buildExecutableUploadExtensions(config.ExecutorChains),
		loginSlots:     make(chan struct{}, 2), loginFailures: make(map[string]loginFailure), trustedProxies: trustedProxies,
		allowedHosts: allowedHosts, canonicalExternalURL: config.CanonicalExternalURL,
		loginRateSalt:  loginRateSalt,
		logStreamSlots: make(chan struct{}, 8), logHistorySlots: make(chan struct{}, 4),
		updateResultsWake: make(chan struct{}, 1),
		hostSecurity:      hostSecurityService,
		serviceLogs:       config.ServiceLogs,
		securityDrafts:    make(map[string]securityFirewallDraft),
		requestRestart:    config.RequestRestart,
		instanceID:        fmt.Sprintf("%d-%x", os.Getpid(), time.Now().UnixNano()),
	}
	if application.serviceLogs == nil {
		application.serviceLogs = servicelogs.New(servicelogs.Options{})
	}
	application.securityHistory, err = securitybaseline.NewHistoryStore(stateRoot)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize security baseline history: %w", err)
	}
	application.auditLog = auditlog.New(db)
	if _, err := application.auditLog.Verify(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verify audit hash chain: %w", err)
	}
	application.securityEvents, err = securityevents.New(securityevents.Options{
		StateRoot: stateRoot, Endpoint: config.SecurityEventEndpoint, Token: config.SecurityEventToken,
		AllowPrivate: config.SecurityEventAllowPrivate, Client: config.SecurityEventClient,
		BrokerEmailRelayEndpoint: config.NotificationEmailRelayEndpoint, BrokerEmailRecipient: config.NotificationEmailRecipient,
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure security event forwarding: %w", err)
	}
	application.auditLog.SetObserver(application.securityEvents.Observe)
	application.auditCheckpoint = config.AuditCheckpoint
	if application.auditCheckpoint == nil {
		application.auditCheckpoint, err = auditcheckpoint.New(auditcheckpoint.Options{StateRoot: stateRoot, SecretStore: credentialStore})
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize external audit checkpoint: %w", err)
		}
	}
	if err := application.auditCheckpoint.VerifyOrBootstrap(context.Background(), application.auditLog, time.Now().UTC()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verify external audit checkpoint: %w", err)
	}
	application.externalTriggers = externaltrigger.New(db, externaltrigger.Options{SecretsDirectory: filepath.Join(stateRoot, "secrets"), SecretStore: credentialStore})
	if !validating {
		if err := application.externalTriggers.ReconcileInvocations(context.Background(), time.Now().UTC().Add(-5*time.Minute)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("reconcile External Interface invocations: %w", err)
		}
	}
	application.remoteWebsites = config.RemoteWebsiteService
	if application.remoteWebsites == nil {
		application.remoteWebsites, err = remotewebsite.New(remotewebsite.Options{StateRoot: stateRoot, SecretStore: credentialStore})
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize remote website credential service: %w", err)
		}
		if err := application.externalTriggers.MigrateSecrets(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate External Interface secrets: %w", err)
		}
		if err := application.externalTriggers.MigrateRemoteWebsiteCredentials(context.Background(), application.remoteWebsites); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate remote website credentials: %w", err)
		}
		if err := application.externalTriggers.PurgeLegacyKeySecrets(context.Background()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("purge recoverable External Interface keys: %w", err)
		}
	}
	application.externalAuthLimit = externaltrigger.NewLimiter(externaltrigger.LimiterOptions{
		SourceRequestsPerMinute: 60, SourceConcurrent: 8, GlobalRequestsPerMinute: 600, GlobalConcurrent: 32,
	})
	application.externalLimit = externaltrigger.NewLimiter(externaltrigger.LimiterOptions{RequestsPerMinute: 60, Concurrent: 4})
	application.mysql, err = mysqlmanager.New(mysqlmanager.Options{DB: db, StateRoot: stateRoot, SecretStore: credentialStore, Backend: config.MySQLBackend, Audit: func(event mysqlmanager.AuditEvent) {
		application.recordAuditWithActor(event.Action, event.Target, event.Result, "mysqlmanager", event.Actor.UserID, event.Actor.Username, "")
	}})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize MySQL management module: %w", err)
	}
	if err := application.files.Protect(application.mysql.BackupRoot()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("protect MySQL backup root: %w", err)
	}
	application.mysqlContext, application.mysqlCancel = context.WithCancel(context.Background())
	assistantOptions := assistant.Options{StateRoot: stateRoot, SecretStore: credentialStore}
	if config.ProviderCredentials != nil {
		assistantOptions.ProviderCredentials = config.ProviderCredentials
	}
	application.assistant, err = assistant.New(db, assistantOptions)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize assistant module: %w", err)
	}
	application.assistantRaster = raster.New(2)
	if !validating {
		if _, err := application.assistant.RecoverInterruptedTurns(context.Background()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("recover assistant turns: %w", err)
		}
	}
	assistantSettings, err := application.assistant.Settings(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("load assistant settings: %w", err)
	}
	application.assistantRuntime = newAssistantRuntimeCoordinatorWithLauncher(stateRoot, application.assistant, assistantSettings.MaxActiveConversations, config.AssistantProcessLauncher)
	if config.ProviderCredentials != nil {
		application.assistantRuntime.SetProviderSessions(brokerAssistantProviderSessions{providers: config.ProviderCredentials})
	}
	application.assistantRuntime.SetApprovalAudit(func(actor assistant.Actor, conversationID, approvalID, result string) {
		var role identity.Role
		_ = application.db.QueryRow("SELECT role FROM users WHERE id = ?", actor.UserID).Scan(&role)
		action := "assistant_tool_approval"
		application.recordAuditWithActor(action, fmt.Sprintf("conversation=%s approval=%s", conversationID, approvalID), result, "assistant", actor.UserID, actor.Username, role)
	})
	runtimeSource := config.AssistantRuntimeSource
	if runtimeSource == nil {
		runtimeSource = runtimeinstall.NewGitHubSource()
	}
	currentBuild := buildinfo.Current()
	application.assistantRuntimes = runtimeinstall.NewManager(runtimeinstall.Config{
		StateRoot:     stateRoot,
		Compatibility: runtimeinstall.Compatibility{ScriptBoardVersion: currentBuild.Version, ScriptBoardTag: currentBuild.Tag},
		Source:        runtimeSource,
		SwitchGuard:   application.assistantRuntime.CanSwitchRuntime,
		Protected:     application.assistant.ReferencedRuntimeVersions,
	})
	application.fileOperations = newSQLiteFileOperationStore(db)
	if application.hostFilesBackend == nil {
		application.fileMoves = hostfiles.NewMoveEngine(files, application.fileOperations)
		application.fileOperationCtx, application.fileOperationStop = context.WithCancel(context.Background())
	}
	if !validating {
		if err := application.initializeAdmin(stateRoot); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := application.applyCredentialOverride(config.AdminUsername, config.AdminPasswordFile); err != nil {
			_ = db.Close()
			return nil, err
		}
		_, _ = auditdomain.CleanupExpiredEventsBefore(db, stateRoot, time.Now().UTC().AddDate(-1, 0, 0), application.auditCheckpoint.CheckpointEventID())
		if err := application.auditCheckpoint.Write(context.Background(), application.auditLog, time.Now().UTC()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("refresh external audit checkpoint after retention: %w", err)
		}
	}
	timeoutGrace := config.RunTimeoutGrace
	if timeoutGrace <= 0 {
		timeoutGrace = 30 * time.Second
	}
	application.runs = runmanager.NewWithLauncher(db, application.files, stateRoot, timeoutGrace, config.ExecutorChains, config.RunnerProcessLauncher, application.auditLog)
	if application.fileMoves != nil {
		if err := application.fileMoves.Recover(context.Background()); err != nil {
			application.runs.Close()
			_ = db.Close()
			return nil, fmt.Errorf("recover filesystem operations: %w", err)
		}
	}
	if validating {
		if _, entered := application.runs.EnterMaintenance(); !entered {
			_ = db.Close()
			return nil, errors.New("validation mode cannot start while a Run is active")
		}
		application.validation.Store(true)
		application.validationID = validationID
	}
	if !validating {
		if cleaned, cleanupErr := application.runs.CleanupLogs(90*24*time.Hour, 1<<30); cleanupErr != nil {
			_ = db.Close()
			return nil, cleanupErr
		} else if cleaned > 0 {
			application.recordAudit("cleanup_run_logs", fmt.Sprintf("%d logs", cleaned), "succeeded", "system")
		}
	}
	if validating {
		application.scheduler = scheduler.NewPaused(db, application.runs, application.loadVariables, config.SchedulerNow, config.SchedulerTick, application.recordAudit)
	} else {
		application.scheduler = scheduler.New(db, application.runs, application.loadVariables, config.SchedulerNow, config.SchedulerTick, application.recordAudit)
	}
	if application.hostFilesBackend != nil {
		application.scheduler.SetScriptPreparer(func(scheduleID string) (hostfiles.Script, hostfiles.PreparedDirectory, error) {
			requestID, tokenErr := randomToken(18)
			if tokenErr != nil {
				return hostfiles.Script{}, hostfiles.PreparedDirectory{}, tokenErr
			}
			script, prepareErr := application.hostFilesBackend.PrepareSchedule(context.Background(), "scheduler-"+requestID, scheduleID)
			return script, hostfiles.PreparedDirectory{Path: script.Directory}, prepareErr
		})
	}
	probe, _ := hoststatus.NewSystemProbe(stateRoot, installRoot)
	application.hostStatus, err = hoststatus.New(db, probe, hoststatus.Options{SkipInitialCleanup: validating})
	if err != nil {
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, err
	}
	applicationProbe := config.ApplicationProbe
	if applicationProbe == nil {
		applicationProbe = appstatus.NewSystemProbe()
	}
	application.applicationStatus, err = appstatus.New(db, applicationProbe, appstatus.Options{})
	if err != nil {
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, err
	}
	if config.WebsiteMonitorOptions.LoadVariables == nil {
		config.WebsiteMonitorOptions.LoadVariables = func(context.Context) (map[string]string, error) {
			return application.loadVariables()
		}
	}
	existingWebsiteTransition := config.WebsiteMonitorOptions.OnTransition
	config.WebsiteMonitorOptions.OnTransition = func(transition websitemonitor.Transition) {
		result := "succeeded"
		action := "website_monitor_recovered"
		if transition.State == websitemonitor.StateDown {
			action, result = "website_monitor_down", "failed"
		}
		application.recordAudit(action, transition.MonitorID, result, "website-monitor")
		if existingWebsiteTransition != nil {
			existingWebsiteTransition(transition)
		}
	}
	application.websiteMonitor, err = websitemonitor.New(db, config.WebsiteMonitorOptions)
	if err != nil {
		application.applicationStatus.Close()
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, err
	}
	registryConnections := config.RegistryConnections
	if registryConnections == nil {
		registryConnections, err = registryconnection.New(registryconnection.Options{StateRoot: stateRoot, SecretStore: credentialStore, Client: config.CustomDashboardClient})
		if err != nil {
			application.websiteMonitor.Close()
			application.applicationStatus.Close()
			application.hostStatus.Close()
			application.scheduler.Close()
			application.runs.Close()
			_ = db.Close()
			return nil, fmt.Errorf("initialize local Registry connections: %w", err)
		}
		if local, ok := registryConnections.(*registryconnection.Service); ok {
			if err := local.MigrateLegacy(context.Background(), db, filepath.Join(stateRoot, "secrets")); err != nil {
				return nil, fmt.Errorf("migrate local Registry connections: %w", err)
			}
		}
	}
	application.customDashboards, err = customdashboard.New(customdashboard.Options{DB: db, Client: config.CustomDashboardClient, RegistryConnections: registryConnections, Paused: validating})
	if err != nil {
		application.websiteMonitor.Close()
		application.applicationStatus.Close()
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, fmt.Errorf("initialize custom dashboards: %w", err)
	}
	kubernetesFactory := config.KubernetesFactory
	if kubernetesFactory == nil {
		kubernetesFactory = clusterstatus.HTTPFactory{}
	}
	application.kubernetesStatus, err = clusterstatus.New(clusterstatus.Options{DB: db, Factory: kubernetesFactory})
	if err != nil {
		application.customDashboards.Close()
		application.websiteMonitor.Close()
		application.applicationStatus.Close()
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, fmt.Errorf("initialize Kubernetes monitoring: %w", err)
	}
	application.assistantTools = newAssistantToolExecutor(application)
	application.assistantRuntime.SetTurnSettled(application.assistantTools.releaseTurn)
	application.assistantBroker, err = toolbroker.New(stateRoot, application.assistantTools)
	if err != nil {
		application.kubernetesStatus.Close()
		application.customDashboards.Close()
		application.websiteMonitor.Close()
		application.applicationStatus.Close()
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, fmt.Errorf("initialize Assistant Tool Broker: %w", err)
	}
	application.assistantRuntime.SetBroker(application.assistantBroker)
	if !validating {
		application.hostStatus.Start(context.Background())
		application.applicationStatus.Start(context.Background())
		application.kubernetesStatus.Start(context.Background())
		if config.MySQLBackend == nil {
			_ = application.mysql.ReconcilePlans(context.Background())
			application.mysqlWG.Add(2)
			go func() {
				defer application.mysqlWG.Done()
				_ = application.mysql.RecoverInterrupted(application.mysqlContext)
			}()
			go func() {
				defer application.mysqlWG.Done()
				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-application.mysqlContext.Done():
						return
					case <-ticker.C:
						_ = application.mysql.RunDuePlans(application.mysqlContext)
					}
				}
			}()
		}
	}
	application.updateContext, application.updateCancel = context.WithCancel(context.Background())
	application.updates = updatepkg.NewManager(updatepkg.ManagerConfig{
		StateRoot: stateRoot, CheckEnabled: config.UpdateCheck, CheckInterval: config.UpdateInterval,
		Source: config.UpdateSource, RequestShutdown: config.RequestShutdown,
	})
	if validating {
		go application.monitorUpdateValidation(validationID)
	} else {
		application.updates.Start(application.updateContext)
	}
	go application.monitorUpdateResults()
	application.shellStatusCache = newShellStatusCache(5*time.Second, time.Now, application.loadShellStatus)
	application.handler = application.routes()
	if !validating {
		externalContext, externalStop := context.WithCancel(context.Background())
		application.externalReconcileStop = externalStop
		application.externalReconcileWG.Add(1)
		go application.monitorExternalInvocationReconciliation(externalContext)
	}
	auditCheckpointContext, auditCheckpointStop := context.WithCancel(context.Background())
	application.auditCheckpointStop = auditCheckpointStop
	application.auditCheckpointWG.Add(1)
	go application.monitorAuditCheckpoint(auditCheckpointContext)
	opened = true
	return application, nil
}

func buildExecutableUploadExtensions(configured map[string][]string) map[string]struct{} {
	extensions := make(map[string]struct{})
	for _, extension := range []string{
		".appimage", ".bat", ".cmd", ".com", ".cpl", ".desktop", ".dll", ".exe", ".hta",
		".jar", ".js", ".jse", ".msi", ".ps1", ".py", ".reg", ".scr", ".service", ".sh",
		".vbe", ".vbs", ".wsf", ".wsh",
	} {
		extensions[extension] = struct{}{}
	}
	for extension := range configured {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if strings.HasPrefix(extension, ".") {
			extensions[extension] = struct{}{}
		}
	}
	return extensions
}

func (a *App) executableUploadRequiresPublication(name string) bool {
	_, active := a.execUploadExts[strings.ToLower(filepath.Ext(name))]
	return active
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) monitorAuditCheckpoint(ctx context.Context) {
	defer a.auditCheckpointWG.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = a.auditCheckpoint.Write(ctx, a.auditLog, now.UTC())
		}
	}
}

func (a *App) ValidationOperationID() string {
	return a.validationID
}

func (a *App) monitorExternalInvocationReconciliation(ctx context.Context) {
	defer a.externalReconcileWG.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = a.externalTriggers.ReconcileInvocations(ctx, now.UTC().Add(-5*time.Minute))
		}
	}
}

func (a *App) beginUpdateMaintenance() (int, bool) {
	a.scheduler.PauseAndWait()
	active, entered := a.runs.EnterMaintenance()
	if !entered {
		a.scheduler.Resume()
		return active, false
	}
	return 0, true
}

func (a *App) endUpdateMaintenance() {
	a.runs.LeaveMaintenance()
	a.scheduler.Resume()
}

func (a *App) monitorUpdateValidation(operationID string) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.updateContext.Done():
			return
		case <-ticker.C:
			operation, err := updatepkg.LoadOperation(a.stateRoot, operationID)
			if err != nil {
				continue
			}
			switch operation.Phase {
			case updatepkg.PhaseCommitted, updatepkg.PhaseRolledBack, updatepkg.PhaseFailedSafe:
				a.validation.Store(false)
				a.runs.LeaveMaintenance()
				a.scheduler.Resume()
				a.hostStatus.Start(context.Background())
				a.applicationStatus.Start(context.Background())
				a.customDashboards.Start()
				a.updates.Start(a.updateContext)
				a.signalUpdateResults()
				return
			}
		}
	}
}

func (a *App) monitorUpdateResults() {
	for {
		shouldPoll := a.inspectUpdateResult()
		var timer *time.Timer
		var retry <-chan time.Time
		if shouldPoll {
			timer = time.NewTimer(500 * time.Millisecond)
			retry = timer.C
		}
		select {
		case <-a.updateContext.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-a.updateResultsWake:
			if timer != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-retry:
		}
	}
}

func (a *App) inspectUpdateResult() bool {
	if a.validation.Load() {
		return false
	}
	active, err := updatepkg.LoadActive(a.stateRoot)
	if err != nil {
		_, statErr := os.Stat(filepath.Join(a.stateRoot, "updates", "active.json"))
		return statErr == nil
	}
	operation, err := updatepkg.LoadOperation(a.stateRoot, active.OperationID)
	if err != nil {
		return true
	}
	var action, result string
	switch operation.Phase {
	case updatepkg.PhasePrepared:
		return false
	case updatepkg.PhaseCommitted:
		action, result = "update_succeeded", "succeeded"
	case updatepkg.PhaseRolledBack:
		action, result = "update_rolled_back", "failed"
	case updatepkg.PhaseNeedsRecovery:
		action, result = "update_recovery_required", "failed"
	case updatepkg.PhaseFailedSafe:
		action, result = "update_failed_safe", "failed"
	default:
		return true
	}
	root, err := updatepkg.OperationDirectory(a.stateRoot, operation.ID)
	if err != nil {
		return true
	}
	imported := filepath.Join(root, "audit-imported")
	if _, err := os.Stat(imported); err == nil {
		return false
	}
	a.recordAudit(action, operation.TargetVersion, result, "system")
	_ = os.WriteFile(imported, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
	return false
}

func (a *App) signalUpdateResults() {
	select {
	case a.updateResultsWake <- struct{}{}:
	default:
	}
}

func parseTrustedProxies(values []string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			result = append(result, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("无效可信代理 %q", value)
		}
		result = append(result, network)
	}
	return result, nil
}

const maximumForwardedChainLength = 8

func (a *App) applyTrustedProxy(request *http.Request) (*http.Request, error) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	peer := net.ParseIP(host)
	trusted := false
	for _, network := range a.trustedProxies {
		if peer != nil && network.Contains(peer) {
			trusted = true
			break
		}
	}
	if !trusted {
		request.Header.Del("Forwarded")
		request.Header.Del("X-Forwarded-For")
		request.Header.Del("X-Forwarded-Host")
		request.Header.Del("X-Forwarded-Proto")
		return request, nil
	}
	if len(request.Header.Values("Forwarded")) != 0 {
		return nil, errors.New("the Forwarded header is not accepted; use the configured X-Forwarded contract")
	}
	forwardedFor, err := singleForwardedHeader(request.Header, "X-Forwarded-For")
	if err != nil {
		return nil, err
	}
	forwarded, err := validatedForwardedValues(forwardedFor, func(value string) bool {
		return net.ParseIP(value) != nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid X-Forwarded-For: %w", err)
	}
	for index := len(forwarded) - 1; index >= 0; index-- {
		client := net.ParseIP(forwarded[index])
		clientTrusted := false
		for _, network := range a.trustedProxies {
			if network.Contains(client) {
				clientTrusted = true
				break
			}
		}
		if !clientTrusted {
			request.RemoteAddr = client.String()
			break
		}
	}
	rawProto, err := singleForwardedHeader(request.Header, "X-Forwarded-Proto")
	if err != nil {
		return nil, err
	}
	forwardedProto, err := validatedForwardedValues(rawProto, func(value string) bool {
		return strings.EqualFold(value, "http") || strings.EqualFold(value, "https")
	})
	if err != nil {
		return nil, fmt.Errorf("invalid X-Forwarded-Proto: %w", err)
	}
	if len(forwardedProto) > 0 && strings.EqualFold(forwardedProto[len(forwardedProto)-1], "https") {
		request = request.WithContext(context.WithValue(request.Context(), secureContextKey, true))
	}
	rawHost, err := singleForwardedHeader(request.Header, "X-Forwarded-Host")
	if err != nil {
		return nil, err
	}
	forwardedHost, err := validatedForwardedValues(rawHost, func(value string) bool {
		_, normalizeErr := normalizeHTTPHost(value)
		return normalizeErr == nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid X-Forwarded-Host: %w", err)
	}
	if len(forwardedHost) > 0 {
		request.Host = forwardedHost[len(forwardedHost)-1]
	}
	return request, nil
}

func singleForwardedHeader(header http.Header, name string) (string, error) {
	values := header.Values(name)
	if len(values) > 1 {
		return "", fmt.Errorf("multiple %s header fields are not allowed", name)
	}
	if len(values) == 0 {
		return "", nil
	}
	return values[0], nil
}

func validatedForwardedValues(raw string, valid func(string) bool) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maximumForwardedChainLength {
		return nil, errors.New("forwarded chain is too long")
	}
	values := make([]string, len(parts))
	for index, part := range parts {
		values[index] = strings.TrimSpace(part)
		if values[index] == "" || !valid(values[index]) {
			return nil, fmt.Errorf("invalid forwarded value at position %d", index+1)
		}
	}
	return values, nil
}

func isSecureRequest(request *http.Request) bool {
	secure, _ := request.Context().Value(secureContextKey).(bool)
	return request.TLS != nil || secure
}

func (a *App) ResetAdminCredentials(username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	if !validUsername(username) {
		return "", errors.New("管理员用户名无效")
	}
	passwordBytes := make([]byte, 24)
	if _, err := rand.Read(passwordBytes); err != nil {
		return "", err
	}
	password := base64.RawURLEncoding.EncodeToString(passwordBytes)
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	transaction, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec("UPDATE users SET username = ?, password_hash = ?, auth_version = auth_version + 1, updated_at = ? WHERE role = 'administrator'", username, hash, time.Now().UTC().Unix()); err != nil {
		return "", err
	}
	if _, err := transaction.Exec("DELETE FROM sessions"); err != nil {
		return "", err
	}
	passwordPath := filepath.Join(a.stateRoot, "secrets", initialPasswordFilename)
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := transaction.Commit(); err != nil {
		_ = os.Remove(passwordPath)
		return "", err
	}
	if err := a.mfa.Reset("administrator"); err != nil {
		a.recordAudit("admin_reset_mfa", username, "failed", "local-cli")
		return "", fmt.Errorf("reset administrator MFA after credentials changed: %w", err)
	}
	if err := a.passkeys.Reset("administrator"); err != nil {
		a.recordAudit("admin_reset_passkeys", username, "failed", "local-cli")
		return "", fmt.Errorf("reset administrator passkeys after credentials changed: %w", err)
	}
	a.cancelAllAuthenticatedRequests()
	a.recordAudit("admin_reset", username, "succeeded", "local-cli")
	return password, nil
}

func (a *App) applyCredentialOverride(username, passwordFile string) error {
	password := ""
	if passwordFile != "" {
		if !filepath.IsAbs(passwordFile) {
			return errors.New("administrator password file path must be absolute")
		}
		info, err := os.Lstat(passwordFile)
		if err != nil {
			return fmt.Errorf("inspect administrator password file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("administrator password file must be a regular file without links")
		}
		file, err := os.Open(passwordFile)
		if err != nil {
			return fmt.Errorf("读取管理员密码文件: %w", err)
		}
		content, readErr := io.ReadAll(io.LimitReader(file, maxPasswordBytes+3))
		closeErr := file.Close()
		if readErr != nil {
			return fmt.Errorf("读取管理员密码文件: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭管理员密码文件: %w", closeErr)
		}
		if len(content) > maxPasswordBytes+2 {
			return errors.New("管理员密码文件过大")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(content), "\n"), "\r")
	}
	if username == "" && password == "" {
		return nil
	}
	var currentUsername, currentHash string
	if err := a.db.QueryRow("SELECT username, password_hash FROM users WHERE role = 'administrator'").Scan(&currentUsername, &currentHash); err != nil {
		return err
	}
	if username == "" {
		username = currentUsername
	} else {
		username = strings.TrimSpace(username)
	}
	if !validUsername(username) {
		return errors.New("管理员用户名覆盖无效")
	}
	changed := username != currentUsername
	newHash := currentHash
	if password != "" {
		if err := validatePasswordPolicy(password, username); err != nil {
			return errors.New("管理员密码覆盖不符合密码策略")
		}
		if !verifyPassword(password, currentHash) {
			changed = true
			hash, err := hashPassword(password)
			if err != nil {
				return err
			}
			newHash = hash
		}
	}
	a.credentialOverride = true
	if !changed {
		return nil
	}
	transaction, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec("UPDATE users SET username = ?, password_hash = ?, auth_version = auth_version + 1, updated_at = ? WHERE role = 'administrator'", username, newHash, time.Now().UTC().Unix()); err != nil {
		return err
	}
	if _, err := transaction.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	a.cancelAllAuthenticatedRequests()
	a.recordAudit("startup_credential_override", username, "succeeded", "system")
	return nil
}

func (a *App) Close() error {
	if a.externalReconcileStop != nil {
		a.externalReconcileStop()
		a.externalReconcileWG.Wait()
	}
	if a.auditCheckpointStop != nil {
		a.auditCheckpointStop()
		a.auditCheckpointWG.Wait()
	}
	if a.mysqlCancel != nil {
		a.mysqlCancel()
		a.mysqlWG.Wait()
	}
	if a.assistantRuntime != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.assistantRuntime.Close(ctx)
		cancel()
	}
	if a.assistantBroker != nil {
		_ = a.assistantBroker.Close()
	}
	if a.fileOperationStop != nil {
		a.fileOperationStop()
		a.fileOperationWG.Wait()
	}
	if a.updateCancel != nil {
		a.updateCancel()
	}
	if a.hostStatus != nil {
		a.hostStatus.Close()
	}
	if a.applicationStatus != nil {
		a.applicationStatus.Close()
	}
	if a.kubernetesStatus != nil {
		a.kubernetesStatus.Close()
	}
	if a.websiteMonitor != nil {
		a.websiteMonitor.Close()
	}
	if a.customDashboards != nil {
		a.customDashboards.Close()
	}
	if a.scheduler != nil {
		a.scheduler.Close()
	}
	if a.runs != nil {
		a.runs.Close()
	}
	if a.auditLog != nil {
		a.auditLog.SetObserver(nil)
	}
	if a.securityEvents != nil {
		_ = a.securityEvents.Close()
	}
	var checkpointErr error
	if a.auditCheckpoint != nil && a.auditLog != nil {
		checkpointErr = a.auditCheckpoint.Write(context.Background(), a.auditLog, time.Now().UTC())
	}
	_, _ = a.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	dbErr := a.db.Close()
	lockErr := a.instanceLock.Close()
	if dbErr != nil {
		return dbErr
	}
	if checkpointErr != nil {
		return checkpointErr
	}
	return lockErr
}

func rejectIncompatibleExistingStateRoot(state string) error {
	if strings.TrimSpace(state) == "" {
		return nil
	}
	absolute, err := filepath.Abs(state)
	if err != nil {
		return fmt.Errorf("resolve existing State Root: %w", err)
	}
	info, err := os.Stat(absolute)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing State Root: %w", err)
	}
	if !info.IsDir() {
		return nil
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return fmt.Errorf("resolve existing State Root: %w", err)
	}
	databasePath := filepath.Join(real, "app.db")
	databaseInfo, err := os.Stat(databasePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing SQLite database: %w", err)
	}
	if databaseInfo.Size() == 0 {
		return nil
	}
	schemaVersion, err := storesqlite.HeaderUserVersion(databasePath)
	if err != nil {
		return fmt.Errorf("inspect existing SQLite schema without modifying it: %w", err)
	}
	if !migrations.Compatible(currentSchemaVersion, schemaVersion) {
		return fmt.Errorf("database schema version %d is incompatible with schema %d; use a new State Root", schemaVersion, currentSchemaVersion)
	}
	return nil
}

func prepareStateRoot(state string) (string, error) {
	if strings.TrimSpace(state) == "" {
		return "", errors.New("State Root cannot be empty")
	}
	stateAbsolute, err := filepath.Abs(state)
	if err != nil {
		return "", fmt.Errorf("resolve absolute State Root: %w", err)
	}
	if filepath.Dir(filepath.Clean(stateAbsolute)) == filepath.Clean(stateAbsolute) {
		return "", errors.New("State Root cannot be a filesystem or volume root")
	}
	if err := os.MkdirAll(stateAbsolute, 0o700); err != nil {
		return "", fmt.Errorf("create State Root %q: %w", state, err)
	}
	stateReal, err := filepath.EvalSymlinks(stateAbsolute)
	if err != nil {
		return "", fmt.Errorf("resolve State Root: %w", err)
	}
	stateReal, err = filepath.Abs(stateReal)
	if err != nil {
		return "", fmt.Errorf("resolve absolute State Root: %w", err)
	}
	filesystemRoot, err := hostfiles.IsFilesystemRoot(stateReal)
	if err != nil {
		return "", fmt.Errorf("resolve State Root filesystem: %w", err)
	}
	if filesystemRoot {
		return "", errors.New("State Root cannot resolve to a filesystem or volume root")
	}
	if err := privatepath.ProtectDirectory(stateReal); err != nil {
		return "", fmt.Errorf("protect State Root %q: %w", stateReal, err)
	}
	return stateReal, nil
}

func openDatabase(path string) (*sql.DB, error) {
	db, schemaVersion, err := storesqlite.OpenSQLite(path, currentSchemaVersion, func(version int) bool {
		return migrations.Compatible(currentSchemaVersion, version)
	})
	if err != nil {
		return nil, fmt.Errorf("open application database for schema %d: %w", currentSchemaVersion, err)
	}
	if err := migrations.Apply(db, schemaVersion, migrations.Options{
		CurrentVersion: currentSchemaVersion,
		RandomToken:    randomToken,
		HashToken:      hashToken,
		Now:            time.Now,
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Persist page one before returning so future startup can reject an old
	// database by reading its header without opening it in writable mode.
	if err := storesqlite.Checkpoint(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("checkpoint initialized SQLite schema: %w", err)
	}
	return db, nil
}

func (a *App) initializeAdmin(stateRoot string) error {
	transaction, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("开始 admin 初始化事务: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	var exists int
	if err := transaction.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE role = 'administrator')").Scan(&exists); err != nil {
		return fmt.Errorf("检查 admin: %w", err)
	}
	if exists != 0 {
		return transaction.Commit()
	}

	passwordBytes := make([]byte, 24)
	if _, err := rand.Read(passwordBytes); err != nil {
		return fmt.Errorf("生成初始密码: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(passwordBytes)
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	if _, err := transaction.Exec(
		"INSERT INTO users (id, username, password_hash, role, enabled, auth_version, mfa_required_at, created_at, updated_at) VALUES ('administrator', 'admin', ?, 'administrator', 1, 1, ?, ?, ?)",
		hash, time.Now().UTC().Add(24*time.Hour).Unix(), now, now,
	); err != nil {
		return fmt.Errorf("创建 admin: %w", err)
	}

	secretsRoot := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(secretsRoot, 0o700); err != nil {
		return fmt.Errorf("创建秘密目录: %w", err)
	}
	passwordPath := filepath.Join(secretsRoot, initialPasswordFilename)
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0o600); err != nil {
		return fmt.Errorf("写入初始密码: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		_ = os.Remove(passwordPath)
		return fmt.Errorf("提交 admin 初始化: %w", err)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成密码 salt: %w", err)
	}
	passwordWorkSlots <- struct{}{}
	key := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordParallelism, passwordKeyLength)
	<-passwordWorkSlots
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		passwordMemory,
		passwordIterations,
		passwordParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(password, encoded string) bool {
	return verifyPasswordContext(context.Background(), password, encoded)
}

func verifyPasswordContext(ctx context.Context, password, encoded string) bool {
	if len(password) > maxPasswordBytes || !utf8.ValidString(password) {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version, memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	if memory != passwordMemory || iterations != passwordIterations || parallelism != passwordParallelism {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != passwordSaltLength {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != passwordKeyLength {
		return false
	}
	select {
	case passwordWorkSlots <- struct{}{}:
		defer func() { <-passwordWorkSlots }()
	case <-ctx.Done():
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func serveWebAsset(response http.ResponseWriter, request *http.Request, contentType, body string) {
	response.Header().Set("Content-Type", contentType)
	if request.URL.Query().Has("v") {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		response.Header().Set("Cache-Control", "no-cache, must-revalidate")
	}
	_, _ = io.WriteString(response, body)
}

type pageResponseWriter struct {
	http.ResponseWriter
	status    int
	buffering bool
	committed bool
	body      bytes.Buffer
}

func (w *pageResponseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	w.committed = true
	w.status = status
	contentType := w.Header().Get("Content-Type")
	w.buffering = status >= 400 || (strings.HasPrefix(contentType, "text/html") && status < 300)
	if !w.buffering {
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *pageResponseWriter) Write(value []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		return w.body.Write(value)
	}
	return w.ResponseWriter.Write(value)
}

func (w *pageResponseWriter) Flush() {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(w.status)
		body := w.body.Bytes()
		if w.status >= 400 {
			body = secretredaction.Bytes(body)
		}
		_, _ = w.ResponseWriter.Write(body)
		w.body.Reset()
		w.buffering = false
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *pageResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *pageResponseWriter) finish(a *App, request *http.Request) {
	if !w.buffering {
		return
	}
	body := w.body.Bytes()
	if w.status >= 400 {
		body = secretredaction.Bytes(body)
	}
	if w.status >= 400 && strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		body = renderApplicationError(request, w.status, strings.TrimSpace(string(body)))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	locale := resolveWebLocale(request)
	publicPage := strings.HasPrefix(request.URL.Path, "/public/")
	if request.URL.Path != "/login" && !publicPage {
		if request.Header.Get("X-ScriptBoard-Navigation") == "pjax" {
			body = []byte(prepareApplicationDocument(body, locale))
		} else {
			body = a.addApplicationShell(request, body)
		}
	}
	w.Header().Set("Content-Language", string(locale))
	if !publicPage {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(body)
}

const listPageSize = 20

func isDeferredDataShell(request *http.Request) bool {
	return request.Header.Get("X-ScriptBoard-Navigation") == "pjax" &&
		request.Header.Get("X-ScriptBoard-Data") == "shell"
}

type paginationView struct {
	Page, PageCount, Total, Start, End int
	PreviousURL, NextURL               string
	HasPrevious, HasNext               bool
}

type overviewRunView struct {
	ID         string    `json:"id"`
	ScriptPath string    `json:"scriptPath"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"startedAt"`
}

type overviewResponse struct {
	hoststatus.Overview
	ActiveRuns    []overviewRunView `json:"activeRuns"`
	HostUptime    time.Duration     `json:"hostUptime"`
	ServiceUptime time.Duration     `json:"serviceUptime"`
}

func validOverviewRange(value string) bool {
	switch value {
	case "", hoststatus.Range15Minutes, hoststatus.Range1Hour, hoststatus.Range6Hours, hoststatus.Range24Hours:
		return true
	default:
		return false
	}
}

func (a *App) loadOverview(request *http.Request, selectedRange string) (overviewResponse, error) {
	if selectedRange == "" {
		selectedRange = hoststatus.Range1Hour
	}
	overview, err := a.hostStatus.Overview(request.Context(), selectedRange)
	if err != nil {
		return overviewResponse{}, err
	}
	runs, err := a.activeOverviewRuns()
	if err != nil {
		return overviewResponse{}, err
	}
	now := time.Now().UTC()
	response := overviewResponse{Overview: overview, ActiveRuns: runs}
	if !overview.Facts.BootedAt.IsZero() {
		response.HostUptime = now.Sub(overview.Facts.BootedAt)
	}
	if !overview.Facts.ServiceStartedAt.IsZero() {
		response.ServiceUptime = now.Sub(overview.Facts.ServiceStartedAt)
	}
	return response, nil
}

func (a *App) overviewPage(response http.ResponseWriter, request *http.Request) {
	selectedRange := request.URL.Query().Get("range")
	if !validOverviewRange(selectedRange) {
		selectedRange = hoststatus.Range1Hour
	}
	if selectedRange == "" {
		selectedRange = hoststatus.Range1Hour
	}
	view, err := a.loadOverview(request, selectedRange)
	if err != nil {
		http.Error(response, "无法读取宿主状态："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = overviewTemplate.Execute(response, struct {
		overviewResponse
		Range  string
		Locale webLocale
	}{overviewResponse: view, Range: selectedRange, Locale: resolveWebLocale(request)})
}

func (a *App) overviewData(response http.ResponseWriter, request *http.Request) {
	selectedRange := request.URL.Query().Get("range")
	if !validOverviewRange(selectedRange) {
		http.Error(response, "无效的概览时间范围", http.StatusBadRequest)
		return
	}
	if selectedRange == "" {
		selectedRange = hoststatus.Range1Hour
	}
	view, err := a.loadOverview(request, selectedRange)
	if err != nil {
		http.Error(response, "无法读取宿主状态："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(view)
}

func (a *App) activeOverviewRuns() ([]overviewRunView, error) {
	rows, err := a.db.Query(`SELECT id, script_path, status, started_at, created_at FROM runs
		WHERE status IN ('starting', 'running', 'stopping', 'timing_out') ORDER BY created_at DESC LIMIT 5`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []overviewRunView
	for rows.Next() {
		var value overviewRunView
		var started sql.NullInt64
		var created int64
		if err := rows.Scan(&value.ID, &value.ScriptPath, &value.Status, &started, &created); err != nil {
			return nil, err
		}
		stamp := created
		if started.Valid {
			stamp = started.Int64
		}
		value.StartedAt = time.Unix(0, stamp).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}

func newPagination(request *http.Request, total int) paginationView {
	pageCount := max(1, (total+listPageSize-1)/listPageSize)
	page := 1
	if parsed, err := strconv.Atoi(request.URL.Query().Get("page")); err == nil && parsed > 0 {
		page = min(parsed, pageCount)
	}
	start := min((page-1)*listPageSize, total)
	end := min(start+listPageSize, total)
	view := paginationView{
		Page: page, PageCount: pageCount, Total: total, Start: start, End: end,
		HasPrevious: page > 1, HasNext: page < pageCount,
	}
	pageURL := func(target int) string {
		query := request.URL.Query()
		query.Set("page", strconv.Itoa(target))
		return "?" + query.Encode()
	}
	if view.HasPrevious {
		view.PreviousURL = pageURL(page - 1)
	}
	if view.HasNext {
		view.NextURL = pageURL(page + 1)
	}
	return view
}

func renderApplicationError(request *http.Request, status int, message string) []byte {
	destination, label := "/resources/files", "返回文件"
	switch {
	case strings.HasPrefix(request.URL.Path, "/monitor/websites"):
		destination, label = "/monitor/websites", "返回网站监控"
	case strings.HasPrefix(request.URL.Path, "/monitor"):
		destination, label = "/monitor", "返回概览"
	case strings.HasPrefix(request.URL.Path, "/settings/account"):
		destination, label = "/settings/account", "返回账户设置"
	case strings.HasPrefix(request.URL.Path, "/settings/display"):
		destination, label = "/settings/display", "返回状态显示设置"
	case strings.HasPrefix(request.URL.Path, "/config/quick-runs"):
		destination, label = "/config/quick-runs", "返回快捷执行"
	case strings.HasPrefix(request.URL.Path, "/config/schedules"):
		destination, label = "/config/schedules", "返回计划"
	case strings.HasPrefix(request.URL.Path, "/resources/variables"):
		destination, label = "/resources/variables", "返回变量"
	case strings.HasPrefix(request.URL.Path, "/history/audit"):
		destination, label = "/history/audit", "返回审计"
	}
	var page bytes.Buffer
	summaryKey := "error.internal"
	switch status {
	case http.StatusBadRequest:
		summaryKey = "error.bad_request"
	case http.StatusUnauthorized:
		summaryKey = "error.unauthorized"
	case http.StatusForbidden:
		summaryKey = "error.forbidden"
	case http.StatusNotFound:
		summaryKey = "error.not_found"
	case http.StatusConflict:
		summaryKey = "error.conflict"
	}
	locale := resolveWebLocale(request)
	_ = applicationErrorTemplate.Execute(&page, struct {
		Status             int
		Message, Summary   string
		Destination, Label string
		Locale             webLocale
	}{Status: status, Message: message, Summary: webText(locale, summaryKey), Destination: destination, Label: label, Locale: locale})
	return page.Bytes()
}

var appCSS = mustWebAsset("ui/assets/app.css")

var appJS = mustWebAsset("ui/assets/app.js")

var markdownItJS = mustWebAsset("ui/assets/markdown-it.min.js")

var domPurifyJS = mustWebAsset("ui/assets/purify.min.js")

var highlightJS = mustWebAsset("ui/assets/highlight.min.js")

var highlightPowerShellJS = mustWebAsset("ui/assets/highlight-powershell.min.js")

var highlightDOSJS = mustWebAsset("ui/assets/highlight-dos.min.js")

var scriptboardFaviconICO = mustWebAsset("ui/assets/favicon.ico")

var webAssetVersion = func() string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		appCSS,
		appJS,
		markdownItJS,
		domPurifyJS,
		highlightJS,
		highlightPowerShellJS,
		highlightDOSJS,
	}, "\x00")))
	return hex.EncodeToString(digest[:6])
}()

type auditView struct {
	OccurredAt time.Time
	Action     string
	Target     string
	Result     string
	Source     string
	Actor      string
	ActorRole  string
	RequestID  string
	Assurance  string
	Revision   string
	Digest     string
}

var (
	errInvalidDateRange = errors.New("invalid date range")
	errDateRangeOrder   = errors.New("start date is after end date")
)

type localDateRange struct {
	FromDate    string
	ToDate      string
	From        time.Time
	ToExclusive time.Time
	HasFromDate bool
	HasToDate   bool
}

func parseLocalDateRange(values url.Values) (localDateRange, error) {
	dateRange := localDateRange{
		FromDate: strings.TrimSpace(values.Get("from")),
		ToDate:   strings.TrimSpace(values.Get("to")),
	}
	var err error
	if dateRange.FromDate != "" {
		dateRange.From, err = time.ParseInLocation(time.DateOnly, dateRange.FromDate, time.Local)
		if err != nil {
			return localDateRange{}, errInvalidDateRange
		}
		dateRange.HasFromDate = true
	}
	if dateRange.ToDate != "" {
		to, parseErr := time.ParseInLocation(time.DateOnly, dateRange.ToDate, time.Local)
		err = parseErr
		if err != nil {
			return localDateRange{}, errInvalidDateRange
		}
		dateRange.ToExclusive = to.AddDate(0, 0, 1)
		dateRange.HasToDate = true
	}
	if dateRange.HasFromDate && dateRange.HasToDate && dateRange.From.After(dateRange.ToExclusive.AddDate(0, 0, -1)) {
		return localDateRange{}, errDateRangeOrder
	}
	return dateRange, nil
}

type auditFilters struct {
	Query              string
	FromDate           string
	ToDate             string
	FromUnix           int64
	ToExclusiveUnix    int64
	HasFromDate        bool
	HasToDate          bool
	HasActiveSelection bool
}

func parseAuditFilters(values url.Values) (auditFilters, error) {
	dateRange, err := parseLocalDateRange(values)
	if err != nil {
		return auditFilters{}, err
	}
	filters := auditFilters{
		Query:       strings.TrimSpace(values.Get("q")),
		FromDate:    dateRange.FromDate,
		ToDate:      dateRange.ToDate,
		FromUnix:    dateRange.From.Unix(),
		HasFromDate: dateRange.HasFromDate,
		HasToDate:   dateRange.HasToDate,
	}
	if dateRange.HasToDate {
		filters.ToExclusiveUnix = dateRange.ToExclusive.Unix()
	}
	filters.HasActiveSelection = filters.Query != "" || filters.HasFromDate || filters.HasToDate
	return filters, nil
}

func (a *App) auditPage(response http.ResponseWriter, request *http.Request) {
	filters, err := parseAuditFilters(request.URL.Query())
	if err != nil {
		key := "common.invalid_date_range"
		if errors.Is(err, errDateRangeOrder) {
			key = "common.invalid_date_order"
		}
		http.Error(response, webText(resolveWebLocale(request), key), http.StatusBadRequest)
		return
	}
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = auditTemplate.Execute(response, struct {
			Events       []auditView
			Pagination   paginationView
			Filters      auditFilters
			Locale       webLocale
			DeferredData bool
		}{Filters: filters, Locale: locale, DeferredData: true})
		return
	}
	like := "%" + filters.Query + "%"
	var total int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE (? = '' OR action LIKE ? OR target LIKE ? OR result LIKE ? OR source_address LIKE ? OR actor_username LIKE ? OR actor_role LIKE ? OR request_id LIKE ? OR authentication_assurance LIKE ? OR resource_revision LIKE ? OR resource_digest_sha256 LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)`,
		filters.Query, like, like, like, like, like, like, like, like, like, like,
		filters.HasFromDate, filters.FromUnix,
		filters.HasToDate, filters.ToExclusiveUnix).Scan(&total); err != nil {
		http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	rows, err := a.db.Query(`SELECT occurred_at, action, target, result, source_address, actor_username, actor_role,
		request_id, authentication_assurance, resource_revision, resource_digest_sha256 FROM audit_events
		WHERE (? = '' OR action LIKE ? OR target LIKE ? OR result LIKE ? OR source_address LIKE ? OR actor_username LIKE ? OR actor_role LIKE ? OR request_id LIKE ? OR authentication_assurance LIKE ? OR resource_revision LIKE ? OR resource_digest_sha256 LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)
		ORDER BY occurred_at DESC LIMIT ? OFFSET ?`,
		filters.Query, like, like, like, like, like, like, like, like, like, like,
		filters.HasFromDate, filters.FromUnix,
		filters.HasToDate, filters.ToExclusiveUnix,
		listPageSize, pagination.Start)
	if err != nil {
		http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var events []auditView
	for rows.Next() {
		var event auditView
		var occurredAt int64
		if err := rows.Scan(&occurredAt, &event.Action, &event.Target, &event.Result, &event.Source, &event.Actor, &event.ActorRole,
			&event.RequestID, &event.Assurance, &event.Revision, &event.Digest); err != nil {
			http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
			return
		}
		event.Action = secretredaction.String(event.Action)
		event.Target = secretredaction.String(event.Target)
		event.Result = secretredaction.String(event.Result)
		event.Source = secretredaction.String(event.Source)
		event.Actor = secretredaction.String(event.Actor)
		event.ActorRole = secretredaction.String(event.ActorRole)
		event.RequestID = secretredaction.String(event.RequestID)
		event.Assurance = secretredaction.String(event.Assurance)
		event.Revision = secretredaction.String(event.Revision)
		event.Digest = secretredaction.String(event.Digest)
		event.OccurredAt = time.Unix(occurredAt, 0).UTC()
		events = append(events, event)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = auditTemplate.Execute(response, struct {
		Events       []auditView
		Pagination   paginationView
		Filters      auditFilters
		Locale       webLocale
		DeferredData bool
	}{Events: events, Pagination: pagination, Filters: filters, Locale: locale})
}

func (a *App) auditDownload(response http.ResponseWriter, _ *http.Request) {
	var anchor, tail string
	if err := a.db.QueryRow("SELECT anchor_hash, tail_hash FROM audit_chain_state WHERE id = 1").Scan(&anchor, &tail); err != nil {
		http.Error(response, "无法读取审计哈希链", http.StatusInternalServerError)
		return
	}
	rows, err := a.db.Query("SELECT id, occurred_at, action, target, result, source_address, actor_user_id, actor_username, actor_role, request_id, authentication_assurance, resource_revision, resource_digest_sha256, previous_hash, event_hash FROM audit_events ORDER BY id")
	if err != nil {
		http.Error(response, "无法导出审计事件", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	response.Header().Set("Content-Type", "text/csv; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="scriptboard-audit.csv"`)
	writer := csv.NewWriter(response)
	_ = writer.Write([]string{"id", "occurred_at", "action", "target", "result", "source_address", "actor_user_id", "actor_username", "actor_role", "request_id", "authentication_assurance", "resource_revision", "resource_digest_sha256", "previous_hash", "event_hash", "chain_anchor", "chain_tail"})
	for rows.Next() {
		var id, occurred int64
		var action, target, result, source, actorUserID, actorUsername, actorRole, requestID, assurance, revision, digest, previousHash, eventHash string
		if rows.Scan(&id, &occurred, &action, &target, &result, &source, &actorUserID, &actorUsername, &actorRole, &requestID, &assurance, &revision, &digest, &previousHash, &eventHash) != nil {
			return
		}
		record := []string{strconv.FormatInt(id, 10), time.Unix(occurred, 0).UTC().Format(time.RFC3339), action, target, result, source, actorUserID, actorUsername, actorRole, requestID, assurance, revision, digest, previousHash, eventHash, anchor, tail}
		for index := range record {
			record[index] = spreadsheetSafeCSVCell(secretredaction.String(record[index]))
		}
		_ = writer.Write(record)
	}
	writer.Flush()
}

func spreadsheetSafeCSVCell(value string) string {
	leadingControl := false
	trimmed := strings.TrimLeftFunc(value, func(character rune) bool {
		switch character {
		case '\t', '\r', '\n':
			leadingControl = true
		}
		return unicode.IsSpace(character) || character == '\u200b' || character == '\ufeff'
	})
	if trimmed == "" {
		if leadingControl {
			return "'" + value
		}
		return value
	}
	if leadingControl {
		return "'" + value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

func (a *App) schedulesPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = schedulesTemplate.Execute(response, struct {
			Schedules    []scheduler.Schedule
			Groups       []scheduleGroup
			CSRFToken    string
			Locale       webLocale
			DeferredData bool
			CanExecute   bool
			CanManage    bool
			CanReadFiles bool
		}{
			CSRFToken: current.csrfToken, Locale: locale, DeferredData: true,
			CanExecute: identity.Allows(current.role, identity.PermissionExecute), CanManage: identity.Allows(current.role, identity.PermissionManageExecution),
			CanReadFiles: identity.Allows(current.role, identity.PermissionReadFiles),
		})
		return
	}
	groups, err := a.loadScheduleGroups()
	if err != nil {
		http.Error(response, "无法读取计划分组", http.StatusInternalServerError)
		return
	}
	schedules, err := a.scheduler.List()
	if err != nil {
		http.Error(response, "无法读取计划", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = schedulesTemplate.Execute(response, struct {
		Schedules    []scheduler.Schedule
		Groups       []scheduleGroup
		CSRFToken    string
		Locale       webLocale
		DeferredData bool
		CanManage    bool
	}{
		Schedules: schedules, Groups: organizeScheduleGroups(groups, schedules, locale),
		CSRFToken: current.csrfToken, Locale: locale, CanManage: identity.Allows(current.role, identity.PermissionManageExecution),
	})
}

func (a *App) createSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	values, err := a.scheduleRequest(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := a.scheduler.Create(values)
	if err != nil {
		if isScheduleCronError(err) {
			a.renderScheduleCronSubmissionError(response, request, err)
			return
		}
		http.Error(response, "无法创建计划："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "create_schedule", id, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) scheduleRequest(request *http.Request) (scheduler.CreateRequest, error) {
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		return scheduler.CreateRequest{}, errors.New("计划名称无效")
	}
	groupID, groupName, err := a.resolveScheduleGroup(request.FormValue("group_id"))
	if err != nil {
		return scheduler.CreateRequest{}, errors.New("计划分组不存在")
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 0 || parsed > 24*60*60 {
			return scheduler.CreateRequest{}, errors.New("超时必须是 0 到 86400 秒")
		}
		timeoutSeconds = parsed
	}
	scriptPath, err := a.hostCanonicalExisting(request.Context(), request.FormValue("script"))
	if err != nil {
		return scheduler.CreateRequest{}, fmt.Errorf("计划脚本无效: %w", err)
	}
	info, _, err := a.hostInfo(request.Context(), scriptPath)
	if err != nil || !info.Mode().IsRegular() {
		return scheduler.CreateRequest{}, errors.New("计划脚本必须是普通主机文件")
	}
	return scheduler.CreateRequest{
		Name: name, GroupID: groupID, GroupName: groupName,
		ScriptPath: scriptPath, ArgumentsTemplate: request.FormValue("arguments"),
		Expression: request.FormValue("expression"), TimeoutSeconds: timeoutSeconds,
		AllowOverlap: request.FormValue("disallow_overlap") == "",
	}, nil
}

func (a *App) updateSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	values, err := a.scheduleRequest(request)
	if err == nil {
		err = a.scheduler.Update(request.PathValue("id"), values)
	}
	if err != nil {
		if isScheduleCronError(err) {
			a.renderScheduleCronSubmissionError(response, request, err)
			return
		}
		http.Error(response, "无法更新计划："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "update_schedule", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) toggleSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	enabled := request.FormValue("enabled") == "1"
	if err := a.scheduler.SetEnabled(request.PathValue("id"), enabled); err != nil {
		http.Error(response, "无法更改计划状态", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "toggle_schedule", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) runScheduleNow(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	id, err := a.scheduler.RunNowAs(request.PathValue("id"), current.userID, current.username)
	if err != nil {
		http.Error(response, "无法立即执行计划："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "run_schedule_now", request.PathValue("id"), "accepted")
	response.Header().Set(assistantResourceIDHeader, id)
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) deleteSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除计划需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	if err := a.scheduler.Delete(request.PathValue("id")); err != nil {
		http.Error(response, "无法删除计划", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "delete_schedule", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

type quickRunView struct {
	ID                string
	Name              string
	ScriptPath        string
	DirectoryURL      string
	ArgumentsTemplate string
	TimeoutSeconds    int
	GroupID           string
	Valid             bool
	Locked            bool
	RecentRuns        []quickRunHistoryView
	LastStartedAt     time.Time
	LastDuration      string
	HasLastDuration   bool
	ScriptSHA256      string
	Revision          int64
}

type quickRunHistoryView struct {
	ID          string
	Status      string
	Icon        string
	StartedAt   time.Time
	Duration    string
	HasDuration bool
}

type overlapView struct {
	Action, Script, Arguments, Timeout, CSRFToken string
	Locale                                        webLocale
}

type quickRunCreateRequest struct {
	Name              string
	ScriptPath        string
	ArgumentsTemplate string
	TimeoutSeconds    int
	SourceRunID       *string
	GroupID           *string
}

func (a *App) createQuickRun(ctx context.Context, values quickRunCreateRequest) (string, error) {
	prepared, err := a.hostPrepareScript(ctx, values.ScriptPath)
	if err != nil {
		return "", err
	}
	id, err := randomToken(18)
	if err != nil {
		return "", err
	}
	transaction, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	var sortOrder int
	if err := transaction.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM quick_runs WHERE group_id IS ?", values.GroupID).Scan(&sortOrder); err != nil {
		return "", err
	}
	now := time.Now().UTC().Unix()
	if _, err := transaction.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, source_run_id, sort_order, created_at, group_id, script_sha256, revision, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, values.Name, prepared.Path, hostfiles.ComparisonKey(prepared.Path), values.ArgumentsTemplate, values.TimeoutSeconds,
		values.SourceRunID, sortOrder, now, values.GroupID, prepared.Digest, now,
	); err != nil {
		return "", err
	}
	if err := transaction.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (a *App) saveQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "快捷执行名称无效", http.StatusBadRequest)
		return
	}
	source, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		http.Error(response, "来源运行不存在", http.StatusNotFound)
		return
	}
	if source.ScriptKind == "one_time" {
		http.Error(response, "One-time Runs cannot be saved directly as Quick Runs", http.StatusConflict)
		return
	}
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusConflict)
		return
	}
	id, err := a.createQuickRun(request.Context(), quickRunCreateRequest{
		Name: name, ScriptPath: source.ScriptPath, ArgumentsTemplate: source.ArgumentsTemplate,
		TimeoutSeconds: source.TimeoutSeconds, SourceRunID: &source.ID, GroupID: groupID,
	})
	if err != nil {
		http.Error(response, "无法保存快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordQuickRunAuditForRequest(request, "create_quick_run", id, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
	destination := "/config/quick-runs"
	if request.Header.Get("X-ScriptBoard-Navigation") == "pjax" {
		destination = "/history/runs/" + url.PathEscape(source.ID)
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
}

func (a *App) createQuickRunFromFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "快捷执行名称无效", http.StatusBadRequest)
		return
	}
	scriptPath, err := a.hostCanonicalExisting(request.Context(), request.FormValue("script"))
	if err != nil {
		writeHostFileError(response, "脚本不存在或不可运行", err)
		return
	}
	info, _, err := a.hostInfo(request.Context(), scriptPath)
	if err != nil || !info.Mode().IsRegular() || !isScriptExtension(scriptPath) {
		http.Error(response, "脚本不存在或不可运行", http.StatusBadRequest)
		return
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 0 || parsed > 24*60*60 {
			http.Error(response, "超时必须是 0 到 86400 秒", http.StatusBadRequest)
			return
		}
		timeoutSeconds = parsed
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	argumentsTemplate := request.FormValue("arguments")
	if err := runmanager.ValidateArgumentsTemplate(argumentsTemplate, variables); err != nil {
		http.Error(response, "参数无效："+err.Error(), http.StatusBadRequest)
		return
	}
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusConflict)
		return
	}
	id, err := a.createQuickRun(request.Context(), quickRunCreateRequest{
		Name: name, ScriptPath: scriptPath, ArgumentsTemplate: argumentsTemplate,
		TimeoutSeconds: timeoutSeconds, SourceRunID: nil, GroupID: groupID,
	})
	if err != nil {
		http.Error(response, "无法保存快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordQuickRunAuditForRequest(request, "create_quick_run", id, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
	destination := "/config/quick-runs"
	if request.Header.Get("X-ScriptBoard-Navigation") == "pjax" {
		if returnTo := safeFilesReturnTo(request.FormValue("return_to")); returnTo != "" {
			destination = returnTo
		}
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
}

func (a *App) quickRunsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = quickRunsTemplate.Execute(response, struct {
			QuickRuns    []quickRunView
			Groups       []quickRunGroup
			CSRFToken    string
			Locale       webLocale
			DeferredData bool
			CanExecute   bool
			CanManage    bool
			CanReadFiles bool
		}{
			CSRFToken: current.csrfToken, Locale: locale, DeferredData: true,
			CanExecute: identity.Allows(current.role, identity.PermissionExecute), CanManage: identity.Allows(current.role, identity.PermissionManageExecution),
			CanReadFiles: identity.Allows(current.role, identity.PermissionReadFiles),
		})
		return
	}
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "无法读取快捷执行分组", http.StatusInternalServerError)
		return
	}
	rows, err := a.db.Query(`SELECT id, name, script_path, arguments_template, timeout_seconds, group_id, locked, script_sha256, revision
		FROM quick_runs ORDER BY sort_order, created_at`)
	if err != nil {
		http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
		return
	}
	var quickRuns []quickRunView
	for rows.Next() {
		var quick quickRunView
		var groupID sql.NullString
		if err := rows.Scan(&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds, &groupID, &quick.Locked, &quick.ScriptSHA256, &quick.Revision); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
			return
		}
		if prepared, prepareErr := a.hostPrepareScript(request.Context(), quick.ScriptPath); prepareErr == nil && quick.ScriptSHA256 != "" && subtle.ConstantTimeCompare([]byte(prepared.Digest), []byte(quick.ScriptSHA256)) == 1 {
			quick.Valid = true
		}
		if groupID.Valid {
			quick.GroupID = groupID.String
		}
		quick.DirectoryURL = filesURL(filepath.Dir(quick.ScriptPath))
		quickRuns = append(quickRuns, quick)
	}
	_ = rows.Close()
	if err := a.loadQuickRunHistory(quickRuns, locale); err != nil {
		http.Error(response, "Unable to read Quick Run history", http.StatusInternalServerError)
		return
	}
	groupIndexes := make(map[string]int, len(groups))
	for index := range groups {
		groupIndexes[groups[index].ID] = index
		groups[index].QuickRunCount = 0
	}
	var ungrouped []quickRunView
	for _, quick := range quickRuns {
		if index, ok := groupIndexes[quick.GroupID]; ok {
			groups[index].Items = append(groups[index].Items, quick)
			groups[index].QuickRunCount++
		} else {
			ungrouped = append(ungrouped, quick)
		}
	}
	if len(ungrouped) > 0 {
		groups = append(groups, quickRunGroup{
			ID: "ungrouped", Name: webText(locale, "quick_runs.ungrouped"),
			QuickRunCount: len(ungrouped), Items: ungrouped, Ungrouped: true,
		})
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := quickRunsTemplate.Execute(response, struct {
		QuickRuns    []quickRunView
		Groups       []quickRunGroup
		CSRFToken    string
		Locale       webLocale
		DeferredData bool
		CanExecute   bool
		CanManage    bool
		CanReadFiles bool
	}{
		QuickRuns: quickRuns, Groups: groups, CSRFToken: current.csrfToken, Locale: locale,
		CanExecute: identity.Allows(current.role, identity.PermissionExecute), CanManage: identity.Allows(current.role, identity.PermissionManageExecution),
		CanReadFiles: identity.Allows(current.role, identity.PermissionReadFiles),
	}); err != nil {
		http.Error(response, "Unable to render Quick Runs: "+err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) startQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	quick, err := a.loadQuickRun(request.PathValue("id"))
	if err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	prepared, err := a.hostPrepareScript(request.Context(), quick.ScriptPath)
	if err != nil || quick.ScriptSHA256 == "" || subtle.ConstantTimeCompare([]byte(prepared.Digest), []byte(quick.ScriptSHA256)) != 1 {
		http.Error(response, "快捷执行脚本已变化，请由管理员重新发布", http.StatusConflict)
		return
	}
	preparedDirectory, err := a.hostPrepareDirectory(request.Context(), prepared.Directory)
	if err != nil {
		http.Error(response, "快捷执行工作目录不可用", http.StatusConflict)
		return
	}
	if a.runs.IsActiveScript(quick.ScriptPath) && request.FormValue("confirm_overlap") != "yes" {
		current := request.Context().Value(sessionContextKey).(session)
		response.WriteHeader(http.StatusConflict)
		_ = overlapTemplate.Execute(response, overlapView{Action: "/config/quick-runs/" + url.PathEscape(quick.ID) + "/start", Script: quick.ScriptPath, CSRFToken: current.csrfToken, Locale: resolveWebLocale(request)})
		return
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath: quick.ScriptPath, ExpectedDigest: quick.ScriptSHA256, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds,
		SourceType: "admin/quick-run", SourceName: quick.Name, SourceID: quick.ID, Variables: variables,
		InitiatorUserID: current.userID, InitiatorUsername: current.username,
		PreparedScript: &prepared, PreparedDirectory: &preparedDirectory,
	})
	if err != nil {
		http.Error(response, "无法启动快捷执行："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordQuickRunAuditForRequest(request, "start_quick_run", quick.ID, "accepted")
	response.Header().Set(assistantResourceIDHeader, id)
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) moveQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	direction := request.FormValue("direction")
	operator, order := "<", "DESC"
	if direction == "down" {
		operator, order = ">", "ASC"
	} else if direction != "up" {
		http.Error(response, "排序方向无效", http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法调整快捷执行顺序", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	var currentOrder int
	var groupID sql.NullString
	if err := transaction.QueryRow("SELECT sort_order, group_id FROM quick_runs WHERE id = ?", request.PathValue("id")).Scan(&currentOrder, &groupID); err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	var groupValue any
	if groupID.Valid {
		groupValue = groupID.String
	}
	var neighborID string
	var neighborOrder int
	query := "SELECT id, sort_order FROM quick_runs WHERE group_id IS ? AND sort_order " + operator + " ? ORDER BY sort_order " + order + " LIMIT 1"
	if scanErr := transaction.QueryRow(query, groupValue, currentOrder).Scan(&neighborID, &neighborOrder); scanErr == nil {
		_, err = transaction.Exec("UPDATE quick_runs SET sort_order = CASE id WHEN ? THEN ? WHEN ? THEN ? END WHERE id IN (?, ?)", request.PathValue("id"), neighborOrder, neighborID, currentOrder, request.PathValue("id"), neighborID)
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		err = scanErr
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法调整快捷执行顺序", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "move_quick_run", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) deleteQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除快捷执行需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	var externalReferences int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM external_trigger_entries WHERE action_type = 'quick_run' AND target = ?", id).Scan(&externalReferences); err != nil {
		http.Error(response, "Unable to check External Interface references", http.StatusInternalServerError)
		return
	}
	if externalReferences != 0 {
		http.Error(response, "Quick Run is still referenced by an External Interface", http.StatusConflict)
		return
	}
	result, err := a.db.Exec("DELETE FROM quick_runs WHERE id = ? AND locked = 0", id)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil {
		http.Error(response, "无法删除快捷执行", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		if quick, loadErr := a.loadQuickRun(id); loadErr == nil && quick.Locked {
			http.Error(response, "快捷执行已锁定，请先解锁", http.StatusConflict)
			return
		}
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "delete_quick_run", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) runEvents(response http.ResponseWriter, request *http.Request) {
	lastSequence := int64(0)
	if value := request.URL.Query().Get("after"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			lastSequence = parsed
		}
	}
	if value := request.Header.Get("Last-Event-ID"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			lastSequence = parsed
		}
	}
	runID := request.PathValue("id")
	if _, err := a.runs.GetMetadata(runID); err != nil {
		http.Error(response, "运行不存在", http.StatusNotFound)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "当前连接不支持 SSE", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	status, err := a.runs.FollowEvents(request.Context(), runID, lastSequence, func(event runmanager.Event) error {
		payload, _ := json.Marshal(map[string]any{"source": event.Source, "text": event.Data, "time": event.Time, "encoding_error": event.EncodingError})
		if _, err := fmt.Fprintf(response, "id: %d\nevent: output\ndata: %s\n\n", event.Sequence, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(response, "event: complete\ndata: %s\n\n", status)
	flusher.Flush()
}

type variableView struct {
	Name       string
	Value      string
	IsPassword bool
}

func (a *App) variablesPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = variablesTemplate.Execute(response, struct {
			Variables    []variableView
			CSRFToken    string
			Pagination   paginationView
			Locale       webLocale
			DeferredData bool
		}{CSRFToken: current.csrfToken, Locale: locale, DeferredData: true})
		return
	}
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM variables").Scan(&total); err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	rows, err := a.db.Query("SELECT name, value, is_password FROM variables ORDER BY name LIMIT ? OFFSET ?", listPageSize, pagination.Start)
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	var variables []variableView
	for rows.Next() {
		var variable variableView
		if err := rows.Scan(&variable.Name, &variable.Value, &variable.IsPassword); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取变量", http.StatusInternalServerError)
			return
		}
		variables = append(variables, variable)
	}
	_ = rows.Close()
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = variablesTemplate.Execute(response, struct {
		Variables    []variableView
		CSRFToken    string
		Pagination   paginationView
		Locale       webLocale
		DeferredData bool
	}{Variables: variables, CSRFToken: current.csrfToken, Pagination: pagination, Locale: locale})
}

var variableNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (a *App) createVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := request.FormValue("name")
	value := request.FormValue("value")
	isPassword := request.FormValue("is_password") == "1"
	if !variableNamePattern.MatchString(name) || len([]byte(value)) > 4<<10 {
		http.Error(response, "变量名称或值无效", http.StatusBadRequest)
		return
	}
	var count int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM variables").Scan(&count); err != nil || count >= 1000 {
		http.Error(response, "变量数量已达到上限", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Unix()
	if _, err := a.db.Exec("INSERT INTO variables (name, value, is_password, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", name, value, isPassword, now, now); err != nil {
		http.Error(response, "变量已存在或无法保存", http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "create_variable", name, "succeeded")
	http.Redirect(response, request, "/resources/variables", http.StatusSeeOther)
}

func (a *App) updateVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	original := request.PathValue("name")
	name, value := request.FormValue("name"), request.FormValue("value")
	isPassword := request.FormValue("is_password") == "1"
	if !variableNamePattern.MatchString(name) || len([]byte(value)) > 4<<10 {
		http.Error(response, "变量名称或值无效", http.StatusBadRequest)
		return
	}
	if name != original || isPassword {
		var externalReferences int
		if err := a.db.QueryRow("SELECT COUNT(*) FROM external_trigger_entries WHERE action_type = 'variable' AND target = ?", original).Scan(&externalReferences); err != nil {
			http.Error(response, "Unable to check External Interface references", http.StatusInternalServerError)
			return
		}
		if externalReferences != 0 {
			http.Error(response, "Variable is still referenced by an External Interface", http.StatusConflict)
			return
		}
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法更新变量", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	result, err := transaction.Exec("UPDATE variables SET name = ?, value = ?, is_password = ?, updated_at = ? WHERE name = ?", name, value, isPassword, time.Now().UTC().Unix(), original)
	if err == nil && name != original {
		oldReference, newReference := "{{"+original+"}}", "{{"+name+"}}"
		_, err = transaction.Exec("UPDATE quick_runs SET arguments_template = replace(arguments_template, ?, ?), revision = revision + 1 WHERE arguments_template LIKE ?", oldReference, newReference, "%"+oldReference+"%")
		if err == nil {
			_, err = transaction.Exec("UPDATE schedules SET arguments_template = replace(arguments_template, ?, ?)", oldReference, newReference)
		}
		if err == nil {
			_, err = transaction.Exec("UPDATE website_monitors SET config_json = replace(config_json, ?, ?) WHERE deleted_at IS NULL", oldReference, newReference)
		}
	}
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
		err = transaction.Commit()
	}
	if err != nil || count == 0 {
		http.Error(response, "变量不存在、名称冲突或无法更新", http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "update_variable", original, "succeeded")
	http.Redirect(response, request, "/resources/variables", http.StatusSeeOther)
}

func (a *App) deleteVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除变量需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	name := request.PathValue("name")
	reference := "%{{" + name + "}}%"
	var references int
	if err := a.db.QueryRow("SELECT (SELECT COUNT(*) FROM quick_runs WHERE arguments_template LIKE ?) + (SELECT COUNT(*) FROM schedules WHERE deleted = 0 AND arguments_template LIKE ?) + (SELECT COUNT(*) FROM external_trigger_entries WHERE action_type = 'variable' AND target = ?) + (SELECT COUNT(*) FROM website_monitors WHERE deleted_at IS NULL AND instr(config_json, ?) > 0)", reference, reference, name, "{{"+name+"}}").Scan(&references); err != nil {
		http.Error(response, "无法检查变量引用", http.StatusInternalServerError)
		return
	}
	if references != 0 {
		http.Error(response, "变量仍被快捷执行、计划或网站监控引用", http.StatusConflict)
		return
	}
	result, err := a.db.Exec("DELETE FROM variables WHERE name = ?", name)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil || count == 0 {
		http.Error(response, "变量不存在", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "delete_variable", name, "succeeded")
	http.Redirect(response, request, "/resources/variables", http.StatusSeeOther)
}

func (a *App) stopRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	current := request.Context().Value(sessionContextKey).(session)
	if current.role == identity.RoleOperator {
		run, err := a.runs.GetMetadata(id)
		if err != nil {
			http.Error(response, "无法读取运行："+err.Error(), http.StatusNotFound)
			return
		}
		if run.InitiatorUserID == "" || run.InitiatorUserID != current.userID {
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
	}
	if err := a.runs.Stop(id); err != nil {
		http.Error(response, "无法停止运行："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "stop_run", id, "accepted")
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) startRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if a.runs.IsActiveScript(request.FormValue("script")) && request.FormValue("confirm_overlap") != "yes" {
		current := request.Context().Value(sessionContextKey).(session)
		response.WriteHeader(http.StatusConflict)
		_ = overlapTemplate.Execute(response, overlapView{
			Action: "/history/runs/start", Script: request.FormValue("script"), Arguments: request.FormValue("arguments"), Timeout: request.FormValue("timeout_seconds"), CSRFToken: current.csrfToken, Locale: resolveWebLocale(request),
		})
		return
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 24*60*60 {
			http.Error(response, "超时必须是 0 到 86400 秒", http.StatusBadRequest)
			return
		}
		timeoutSeconds = parsed
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	script, err := a.hostPrepareScript(request.Context(), request.FormValue("script"))
	if err != nil {
		a.recordAuditForRequest(request, "start_run", request.FormValue("script"), "rejected")
		http.Error(response, "脚本不可执行："+err.Error(), http.StatusBadRequest)
		return
	}
	workingDirectory, err := a.hostPrepareDirectory(request.Context(), script.Directory)
	if err != nil {
		a.recordAuditResourceForRequest(request, "start_run", script.Path, "rejected", "", script.Digest)
		http.Error(response, "脚本工作目录不可用："+err.Error(), http.StatusBadRequest)
		return
	}
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath:        script.Path,
		ExpectedDigest:    script.Digest,
		ArgumentsTemplate: request.FormValue("arguments"),
		SourceType:        "admin/manual",
		SourceName:        "manual",
		TimeoutSeconds:    timeoutSeconds,
		Variables:         variables,
		InitiatorUserID:   current.userID,
		InitiatorUsername: current.username,
		PreparedScript:    &script,
		PreparedDirectory: &workingDirectory,
	})
	if err != nil {
		a.recordAuditResourceForRequest(request, "start_run", script.Path, "rejected", "", script.Digest)
		http.Error(response, "无法启动脚本："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditResourceForRequest(request, "start_run", id, "accepted", "", script.Digest)
	response.Header().Set(assistantResourceIDHeader, id)
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) loadVariables() (map[string]string, error) {
	variables := make(map[string]string)
	rows, err := a.db.Query("SELECT name, value FROM variables")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			_ = rows.Close()
			return nil, err
		}
		variables[name] = value
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return variables, rows.Close()
}

func (a *App) runDetails(response http.ResponseWriter, request *http.Request) {
	run, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(response, "运行不存在", http.StatusNotFound)
			return
		}
		http.Error(response, "无法读取运行："+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(run.Events) > 1000 {
		run.Events = run.Events[len(run.Events)-1000:]
	}
	current := request.Context().Value(sessionContextKey).(session)
	canManageExecution := identity.Allows(current.role, identity.PermissionManageExecution)
	canStop := current.role == identity.RoleAdministrator || current.role == identity.RoleMaintainer ||
		current.role == identity.RoleOperator && run.InitiatorUserID == current.userID
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runTemplate.Execute(response, struct {
		Run                         runmanager.Run
		CSRFToken                   string
		Locale                      webLocale
		CanStop, CanManageExecution bool
	}{
		Run: run, CSRFToken: current.csrfToken, Locale: resolveWebLocale(request),
		CanStop: canStop, CanManageExecution: canManageExecution,
	})
}

func (a *App) downloadRun(response http.ResponseWriter, request *http.Request) {
	run, err := a.runs.GetMetadata(request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(response, "Run not found", http.StatusNotFound)
			return
		}
		http.Error(response, "Unable to read Run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var header bytes.Buffer
	writeRunDownloadMetadata(&header, run)
	wroteHeader := false
	eventCount := 0
	lastEventHadNewline := true
	writeHeader := func() error {
		if wroteHeader {
			return nil
		}
		wroteHeader = true
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scriptboard-run-%s.txt"`, sanitizeDownloadName(run.ID)))
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		_, err := response.Write(header.Bytes())
		return err
	}
	err = a.runs.StreamEvents(run.ID, func(event runmanager.Event) error {
		if err := writeHeader(); err != nil {
			return err
		}
		eventCount++
		lastEventHadNewline = strings.HasSuffix(event.Data, "\n")
		_, err := io.WriteString(response, event.Data)
		return err
	})
	if err != nil {
		if !wroteHeader {
			http.Error(response, "Unable to read Run log: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if !lastEventHadNewline {
			_, _ = io.WriteString(response, "\n")
		}
		_, _ = io.WriteString(response, "[Run log could not be read completely]\n")
		return
	}
	if err := writeHeader(); err != nil {
		return
	}
	if eventCount == 0 {
		_, _ = io.WriteString(response, "(no output)\n")
	} else if !lastEventHadNewline {
		_, _ = io.WriteString(response, "\n")
	}
}

func formatRunDownload(run runmanager.Run) []byte {
	var result bytes.Buffer
	writeRunDownloadMetadata(&result, run)
	if len(run.Events) == 0 {
		result.WriteString("(no output)\n")
		return result.Bytes()
	}
	for _, event := range run.Events {
		result.WriteString(event.Data)
	}
	if last := run.Events[len(run.Events)-1].Data; !strings.HasSuffix(last, "\n") {
		result.WriteByte('\n')
	}
	return result.Bytes()
}

func writeRunDownloadMetadata(result *bytes.Buffer, run runmanager.Run) {
	result.WriteString("ScriptBoard Run Record\n")
	result.WriteString("======================\n")
	fmt.Fprintf(result, "Run ID: %s\n", run.ID)
	fmt.Fprintf(result, "Script: %s\n", run.ScriptPath)
	fmt.Fprintf(result, "Status: %s\n", run.Status)
	fmt.Fprintf(result, "Created: %s\n", run.CreatedAt.UTC().Format(time.RFC3339Nano))
	writeRunDownloadTime(result, "Started", run.StartedAt)
	writeRunDownloadTime(result, "Finished", run.FinishedAt)
	fmt.Fprintf(result, "Source: %s", run.SourceType)
	if run.SourceName != "" && run.SourceName != "manual" {
		fmt.Fprintf(result, " / %s", run.SourceName)
	}
	result.WriteByte('\n')
	if run.InitiatorUsername != "" {
		fmt.Fprintf(result, "Initiated by: %s\n", run.InitiatorUsername)
	}
	fmt.Fprintf(result, "Runtime identity: %s\n", run.RuntimeIdentity)
	fmt.Fprintf(result, "Executor: %s\n", run.Executor)
	fmt.Fprintf(result, "Timeout: %ds\n", run.TimeoutSeconds)
	if run.ExitCode != nil {
		fmt.Fprintf(result, "Exit code: %d\n", *run.ExitCode)
	}
	if run.ArgumentsTemplate != "" {
		fmt.Fprintf(result, "Argument template: %s\n", run.ArgumentsTemplate)
	}
	if run.WorkingDirectory != "" {
		fmt.Fprintf(result, "Working directory: %s\n", run.WorkingDirectory)
	}
	if run.Error != "" {
		fmt.Fprintf(result, "Error: %s\n", run.Error)
	}
	fmt.Fprintf(result, "SHA-256: %s\n", run.ScriptDigest)
	fmt.Fprintf(result, "Log expired: %t\n", run.LogExpired)
	fmt.Fprintf(result, "Log incomplete: %t\n", run.LogIncomplete)
	fmt.Fprintf(result, "Log truncated: %t\n", run.LogTruncated)
	if run.LogTruncated {
		fmt.Fprintf(result, "Dropped bytes: %d\n", run.DroppedBytes)
	}
	result.WriteString("\nOutput\n======\n")
}

func writeRunDownloadTime(result *bytes.Buffer, label string, value *time.Time) {
	if value != nil {
		fmt.Fprintf(result, "%s: %s\n", label, value.UTC().Format(time.RFC3339Nano))
	}
}

type runFilters struct {
	Query               string
	FromDate            string
	ToDate              string
	FromUnixNano        int64
	ToExclusiveUnixNano int64
	HasFromDate         bool
	HasToDate           bool
	HasActiveSelection  bool
	FocusSearch         bool
	ScheduleID          string
}

func exactUnixNano(value time.Time) (int64, bool) {
	unixNano := value.UnixNano()
	return unixNano, time.Unix(0, unixNano).UTC().Equal(value.UTC())
}

func parseRunFilters(values url.Values) (runFilters, error) {
	dateRange, err := parseLocalDateRange(values)
	if err != nil {
		return runFilters{}, err
	}
	filters := runFilters{
		Query:       strings.TrimSpace(values.Get("q")),
		FromDate:    dateRange.FromDate,
		ToDate:      dateRange.ToDate,
		HasFromDate: dateRange.HasFromDate,
		HasToDate:   dateRange.HasToDate,
		FocusSearch: values.Get("focus") == "search",
		ScheduleID:  strings.TrimSpace(values.Get("schedule_id")),
	}
	if filters.HasFromDate {
		var ok bool
		filters.FromUnixNano, ok = exactUnixNano(dateRange.From)
		if !ok {
			return runFilters{}, errInvalidDateRange
		}
	}
	if filters.HasToDate {
		var ok bool
		filters.ToExclusiveUnixNano, ok = exactUnixNano(dateRange.ToExclusive)
		if !ok {
			return runFilters{}, errInvalidDateRange
		}
	}
	filters.HasActiveSelection = filters.Query != "" || filters.ScheduleID != "" || filters.HasFromDate || filters.HasToDate
	return filters, nil
}

func (a *App) runsPage(response http.ResponseWriter, request *http.Request) {
	filters, err := parseRunFilters(request.URL.Query())
	if err != nil {
		key := "common.invalid_date_range"
		if errors.Is(err, errDateRangeOrder) {
			key = "common.invalid_date_order"
		}
		http.Error(response, webText(resolveWebLocale(request), key), http.StatusBadRequest)
		return
	}
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = runsTemplate.Execute(response, struct {
			Runs         []runmanager.Run
			Pagination   paginationView
			Filters      runFilters
			Locale       webLocale
			DeferredData bool
		}{Filters: filters, Locale: locale, DeferredData: true})
		return
	}
	managerQuery := filters.Query
	if filters.ScheduleID != "" {
		managerQuery = ""
	}
	managerFilters := runmanager.Filter{
		Query:                    managerQuery,
		ScheduleID:               filters.ScheduleID,
		CreatedFromUnixNano:      filters.FromUnixNano,
		CreatedBeforeUnixNano:    filters.ToExclusiveUnixNano,
		HasCreatedFromBoundary:   filters.HasFromDate,
		HasCreatedBeforeBoundary: filters.HasToDate,
	}
	total, err := a.runs.CountFiltered(managerFilters)
	if err != nil {
		http.Error(response, "无法读取运行记录："+err.Error(), http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	runs, err := a.runs.ListPageFiltered(managerFilters, listPageSize, pagination.Start)
	if err != nil {
		http.Error(response, "无法读取运行记录："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runsTemplate.Execute(response, struct {
		Runs         []runListItemView
		Pagination   paginationView
		Filters      runFilters
		Locale       webLocale
		DeferredData bool
	}{Runs: newRunListItemViews(runs), Pagination: pagination, Filters: filters, Locale: locale})
}

func (a *App) moveFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	source := request.FormValue("source")
	destination := request.FormValue("destination")
	var err error
	if destination == "" {
		destination, err = a.hostDestination(request.Context(), request.FormValue("working_directory"), request.FormValue("name"))
		if err != nil {
			writeHostFileError(response, "移动目标无效", err)
			return
		}
	}
	source, err = a.hostCanonicalExisting(request.Context(), source)
	if err != nil {
		writeHostFileError(response, "移动源无效", err)
		return
	}
	destination, err = a.hostCanonicalDestination(request.Context(), destination)
	if err != nil {
		writeHostFileError(response, "移动目标无效", err)
		return
	}
	sourceParent, _ := parentAndName(source)
	action := request.FormValue("conflict_action")
	if !validConflictAction(action) {
		http.Error(response, "同名文件处理方式无效", http.StatusBadRequest)
		return
	}
	if action == conflictActionSkip || hostfiles.ComparisonKey(source) == hostfiles.ComparisonKey(destination) {
		http.Redirect(response, request, filesURL(sourceParent), http.StatusSeeOther)
		return
	}
	if a.runs.ConflictsPath(source) {
		http.Error(response, "活动运行持有该脚本或其后代的运行租约", http.StatusConflict)
		return
	}
	destinationParent, destinationName := parentAndName(destination)
	if action == conflictActionRename {
		newName := strings.TrimSpace(request.FormValue("new_name"))
		if newName == "" {
			http.Error(response, "请输入新的文件名", http.StatusBadRequest)
			return
		}
		if err := hostfiles.ValidateName(newName); err != nil {
			http.Error(response, "新文件名无效："+err.Error(), http.StatusBadRequest)
			return
		}
		destination, err = a.hostDestination(request.Context(), destinationParent, newName)
		if err != nil {
			writeHostFileError(response, "移动目标无效", err)
			return
		}
		destinationName = newName
	}
	_, _, targetErr := a.hostInfo(request.Context(), destination)
	targetExists := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		http.Error(response, "无法检查移动目标："+targetErr.Error(), http.StatusBadRequest)
		return
	}
	if targetExists && action != conflictActionOverwrite {
		suggested, err := a.hostAvailableName(request.Context(), destinationParent, destinationName)
		if err != nil {
			http.Error(response, "无法生成可用名称："+err.Error(), http.StatusBadRequest)
			return
		}
		relatedPath := hostfiles.Contains(source, destination) || hostfiles.Contains(destination, source)
		a.renderFileConflict(response, request, fileConflictView{
			Action: "/resources/files/move", BackURL: filesURL(sourceParent),
			Source: source, Destination: destination, ItemPath: destination, SuggestedName: suggested,
			CanOverwrite: !relatedPath && !a.runs.ConflictsPath(destination),
		})
		return
	}
	if targetExists && action == conflictActionOverwrite && a.runs.ConflictsPath(destination) {
		http.Error(response, "活动运行正在使用同名目标，不能覆盖", http.StatusConflict)
		return
	}
	sameFilesystem, err := a.hostSameFilesystem(request.Context(), source, destination)
	if err != nil {
		writeHostFileError(response, "无法确定移动边界", err)
		return
	}
	operationID := ""
	leaseID := ""
	if sameFilesystem {
		token, tokenErr := randomToken(18)
		if tokenErr != nil {
			http.Error(response, "无法创建移动租约", http.StatusInternalServerError)
			return
		}
		leaseID = "file-mutation:" + token
	} else {
		operationID, err = randomToken(18)
		if err != nil {
			http.Error(response, "无法创建跨文件系统操作", http.StatusInternalServerError)
			return
		}
		leaseID = "file-operation:" + operationID
	}
	brokerCrossMove := !sameFilesystem && a.hostFilesBackend != nil
	if !brokerCrossMove {
		if err := a.files.AcquireLease(leaseID, source, destination); err != nil {
			http.Error(response, "移动路径正在使用中："+err.Error(), http.StatusConflict)
			return
		}
	}
	leaseOwned := !brokerCrossMove
	defer func() {
		if leaseOwned {
			a.files.ReleaseLease(leaseID)
		}
	}()
	_, _, latestTargetErr := a.hostInfo(request.Context(), destination)
	latestTargetExists := latestTargetErr == nil
	if latestTargetErr != nil && !os.IsNotExist(latestTargetErr) {
		writeHostFileError(response, "无法重新检查移动目标", latestTargetErr)
		return
	}
	if latestTargetExists != targetExists {
		http.Error(response, "移动目标在确认期间发生变化，请重试", http.StatusConflict)
		return
	}
	var displacedID string
	var displaced *hostfiles.Trashed
	if targetExists && action == conflictActionOverwrite {
		displacedID, err = randomToken(18)
		if err != nil {
			http.Error(response, "无法创建覆盖事务", http.StatusInternalServerError)
			return
		}
		moved, err := a.hostMoveToTrash(request.Context(), destination, displacedID)
		if err != nil {
			http.Error(response, "无法暂存同名目标："+err.Error(), http.StatusConflict)
			return
		}
		displaced = &moved
		if _, err := a.db.Exec(
			`INSERT INTO trash_entries
				(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			displacedID, moved.OriginalPath, hostfiles.ComparisonKey(moved.OriginalPath), moved.StoredPath,
			hostfiles.ComparisonKey(moved.StoredPath), time.Now().UTC().Unix(), moved.Size, moved.Directory,
		); err != nil {
			_ = a.hostRestoreFromTrash(request.Context(), moved.StoredPath, moved.OriginalPath)
			http.Error(response, "无法记录被覆盖的条目", http.StatusInternalServerError)
			return
		}
	}
	if !sameFilesystem {
		if brokerCrossMove {
			displacedStoredPath := ""
			if displaced != nil {
				displacedStoredPath = displaced.StoredPath
			}
			if _, moveErr := a.hostStartCrossFilesystemMove(request.Context(), operationID, source, destination, displacedStoredPath, displacedID); moveErr != nil {
				http.Error(response, "unable to start cross-filesystem move: "+moveErr.Error(), http.StatusBadRequest)
				return
			}
			a.recordAuditForRequest(request, "cross_filesystem_move", source+" -> "+destination, "accepted")
			http.Redirect(response, request, "/resources/files/operations/"+url.PathEscape(operationID), http.StatusSeeOther)
			return
		}
		started := make(chan struct{})
		finished := make(chan error, 1)
		current := request.Context().Value(sessionContextKey).(session)
		sourceAddress := request.RemoteAddr
		a.fileOperationWG.Add(1)
		leaseOwned = false
		go func() {
			defer a.fileOperationWG.Done()
			_, moveErr := a.fileMoves.ExecuteWithStart(a.fileOperationCtx, operationID, source, destination, func(hostfiles.FileOperation) { close(started) })
			if moveErr != nil && displaced != nil {
				// A post-commit failure remains recoverable and still owns the
				// destination. Restore an overwritten entry only when the move
				// engine actually rolled the destination back.
				if _, destinationErr := a.files.Info(destination); os.IsNotExist(destinationErr) {
					_ = a.restoreTrackedTrash(request.Context(), displacedID, *displaced)
				}
			}
			result := "succeeded"
			if moveErr != nil {
				result = "failed"
			}
			a.recordAuditWithRequestActor(request, "cross_filesystem_move", source+" -> "+destination, result, sourceAddress, current.userID, current.username, current.role)
			finished <- moveErr
		}()
		select {
		case <-started:
			http.Redirect(response, request, "/resources/files/operations/"+url.PathEscape(operationID), http.StatusSeeOther)
		case moveErr := <-finished:
			if moveErr == nil {
				http.Redirect(response, request, "/resources/files/operations/"+url.PathEscape(operationID), http.StatusSeeOther)
			} else {
				http.Error(response, "无法启动跨文件系统移动："+moveErr.Error(), http.StatusBadRequest)
			}
		}
		return
	}
	if err := a.hostMove(request.Context(), source, destination); err != nil {
		if displaced != nil {
			if restoreErr := a.restoreTrackedTrash(request.Context(), displacedID, *displaced); restoreErr != nil {
				http.Error(response, "无法移动条目："+err.Error()+"；恢复被覆盖条目失败："+restoreErr.Error(), http.StatusInternalServerError)
				return
			}
		}
		http.Error(response, "无法移动条目："+err.Error(), http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err == nil {
		err = updateMovedScriptReferences(transaction, source, destination)
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		if transaction != nil {
			_ = transaction.Rollback()
		}
		rollbackErr := a.hostMove(request.Context(), destination, source)
		if rollbackErr == nil && displaced != nil {
			rollbackErr = a.restoreTrackedTrash(request.Context(), displacedID, *displaced)
		}
		if rollbackErr != nil {
			http.Error(response, "无法同步更新引用："+err.Error()+"；文件回滚失败："+rollbackErr.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(response, "无法同步更新引用："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "move_entry", source+" -> "+destination, "succeeded")
	http.Redirect(response, request, filesURL(destinationParent), http.StatusSeeOther)
}

func (a *App) toggleExecutable(response http.ResponseWriter, request *http.Request) {
	if runtime.GOOS != "linux" {
		http.NotFound(response, request)
		return
	}
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	path, err := a.hostCanonicalExisting(request.Context(), request.FormValue("path"))
	if err != nil {
		writeHostFileError(response, "执行权限目标无效", err)
		return
	}
	release, err := a.acquireFileMutationLease(path)
	if err != nil {
		http.Error(response, "条目正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	if _, err := a.hostToggleOwnerExecute(request.Context(), path); err != nil {
		writeHostFileError(response, "无法切换所有者执行权限", err)
		return
	}
	a.recordAuditForRequest(request, "toggle_owner_execute", path, "succeeded")
	parent, _ := hostPathParent(path)
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) editTextPage(response http.ResponseWriter, request *http.Request) {
	relative, err := a.hostCanonicalExisting(request.Context(), request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "无法编辑文件", err)
		return
	}
	document, err := a.hostReadText(request.Context(), relative, 1<<20)
	if err != nil {
		writeHostFileError(response, "无法编辑文件", err)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	parent, _ := hostPathParent(relative)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = textEditorTemplate.Execute(response, struct {
		Path, Content, Digest, CSRFToken, BackURL, ViewURL, DownloadURL, Action string
		Locale                                                                  webLocale
	}{
		Path: relative, Content: document.Content, Digest: document.Digest, CSRFToken: current.csrfToken,
		BackURL: filesURL(parent), ViewURL: routeFileURL("/resources/files/view", relative), DownloadURL: routeFileURL("/resources/files/download", relative), Action: routeFileURL("/resources/files/edit", relative),
		Locale: resolveWebLocale(request),
	})
}

func (a *App) previewTextPage(response http.ResponseWriter, request *http.Request) {
	relative, err := a.hostCanonicalExisting(request.Context(), request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "无法预览文件", err)
		return
	}
	document, err := a.hostReadText(request.Context(), relative, 1<<20)
	if err != nil {
		writeHostFileError(response, "无法预览文件", err)
		return
	}
	parent, _ := hostPathParent(relative)
	markdown := strings.EqualFold(hostfiles.Extension(relative), ".md")
	highlightLanguage := highlightLanguageForPath(relative)
	title := webText(resolveWebLocale(request), "editor.preview_title")
	if markdown {
		title = webText(resolveWebLocale(request), "editor.markdown_preview_title")
	} else if highlightLanguage != "" {
		title = webText(resolveWebLocale(request), "editor.script_preview_title")
	}
	markdownBaseURL := parent
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = textPreviewTemplate.Execute(response, struct {
		Path, Content, BackURL, EditURL, DownloadURL string
		LogURL                                       string
		Title, MarkdownBaseURL, HighlightLanguage    string
		Markdown                                     bool
		Locale                                       webLocale
	}{
		Path: relative, Content: document.Content, BackURL: filesURL(parent),
		EditURL: routeFileURL("/resources/files/edit", relative), DownloadURL: routeFileURL("/resources/files/download", relative),
		LogURL: "/resources/files/log?" + url.Values{"path": {relative}}.Encode(),
		Title:  title, Markdown: markdown, MarkdownBaseURL: markdownBaseURL, HighlightLanguage: highlightLanguage, Locale: resolveWebLocale(request),
	})
}

func (a *App) saveText(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	relative, err := a.hostCanonicalExisting(request.Context(), request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "无法保存文件", err)
		return
	}
	release, err := a.acquireFileMutationLease(relative)
	if err != nil {
		http.Error(response, "文件正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建回收条目", http.StatusInternalServerError)
		return
	}
	trashed, err := a.hostSaveText(request.Context(), relative, request.FormValue("digest"), request.FormValue("content"), id, 1<<20)
	if errors.Is(err, hostfiles.ErrConflict) {
		http.Error(response, "文件已被外部修改，请重新打开后再保存", http.StatusConflict)
		return
	}
	if err != nil {
		writeHostFileError(response, "无法保存文件", err)
		return
	}
	_, err = a.db.Exec(
		`INSERT INTO trash_entries
			(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		id, trashed.OriginalPath, hostfiles.ComparisonKey(trashed.OriginalPath), trashed.StoredPath,
		hostfiles.ComparisonKey(trashed.StoredPath), time.Now().UTC().Unix(), trashed.Size,
	)
	if err != nil {
		_ = a.hostRollbackTextSave(request.Context(), relative, trashed.StoredPath)
		http.Error(response, "无法记录文件旧版本", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "edit_text", relative, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
	parent, _ := hostPathParent(relative)
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) downloadFile(response http.ResponseWriter, request *http.Request) {
	relative, err := a.hostCanonicalExisting(request.Context(), request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "Unable to download file", err)
		return
	}
	file, info, err := a.hostOpenRegular(request.Context(), relative)
	if err != nil {
		writeHostFileError(response, "无法下载文件", err)
		return
	}
	defer file.Close()
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()})
	response.Header().Set("Content-Disposition", disposition)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(response, request, info.Name(), time.Time{}, file)
}

func (a *App) previewImage(response http.ResponseWriter, request *http.Request) {
	relative, err := a.hostCanonicalExisting(request.Context(), request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "Unable to preview image", err)
		return
	}
	extension := strings.ToLower(hostfiles.Extension(relative))
	contentTypes := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}
	contentType, allowed := contentTypes[extension]
	if !allowed {
		http.Error(response, "该格式只能下载，不能内嵌预览", http.StatusUnsupportedMediaType)
		return
	}
	file, info, err := a.hostOpenRegular(request.Context(), relative)
	if err != nil {
		writeHostFileError(response, "无法预览图片", err)
		return
	}
	defer file.Close()
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Disposition", "inline")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(response, request, info.Name(), info.ModTime(), file)
}

func (a *App) deleteFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	path, err := a.hostCanonicalExisting(request.Context(), request.FormValue("path"))
	if err != nil {
		writeHostFileError(response, "无法删除条目", err)
		return
	}
	if a.runs.ConflictsPath(path) {
		http.Error(response, "活动运行持有该脚本或其后代的运行租约", http.StatusConflict)
		return
	}
	externalFileReferences, err := a.countExternalFileReferences(path)
	if err != nil {
		http.Error(response, "Unable to check External Interface file references", http.StatusInternalServerError)
		return
	}
	if externalFileReferences != 0 {
		http.Error(response, "Path is still referenced by an External Interface file action", http.StatusConflict)
		return
	}
	quickCount, scheduleCount, err := a.countScriptReferences(path)
	if err != nil {
		http.Error(response, "无法检查条目引用", http.StatusInternalServerError)
		return
	}
	if (quickCount > 0 || scheduleCount > 0) && request.FormValue("confirm_references") != "yes" {
		current := request.Context().Value(sessionContextKey).(session)
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusConflict)
		_ = deleteImpactTemplate.Execute(response, struct {
			Path                 string
			QuickRuns, Schedules int
			CSRFToken            string
			Locale               webLocale
		}{Path: path, QuickRuns: quickCount, Schedules: scheduleCount, CSRFToken: current.csrfToken, Locale: resolveWebLocale(request)})
		return
	}
	release, err := a.acquireFileMutationLease(path)
	if err != nil {
		http.Error(response, "条目正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建回收条目", http.StatusInternalServerError)
		return
	}
	trashed, err := a.hostMoveToTrash(request.Context(), path, id)
	if err != nil {
		http.Error(response, "无法删除条目："+err.Error(), http.StatusBadRequest)
		return
	}
	_, err = a.db.Exec(
		`INSERT INTO trash_entries
			(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, trashed.OriginalPath, hostfiles.ComparisonKey(trashed.OriginalPath), trashed.StoredPath,
		hostfiles.ComparisonKey(trashed.StoredPath), time.Now().UTC().Unix(), trashed.Size, trashed.Directory,
	)
	if err != nil {
		_ = a.hostRestoreFromTrash(request.Context(), trashed.StoredPath, trashed.OriginalPath)
		http.Error(response, "无法记录回收条目", http.StatusInternalServerError)
		return
	}
	path = trashed.OriginalPath
	transaction, err := a.db.Begin()
	if err == nil {
		err = disableScheduleReferences(transaction, path, time.Now().UTC().UnixNano())
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		if transaction != nil {
			_ = transaction.Rollback()
		}
		if restoreErr := a.restoreTrackedTrash(request.Context(), id, trashed); restoreErr != nil {
			http.Error(response, "无法停用引用该条目的计划："+err.Error()+"；文件回滚失败："+restoreErr.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(response, "无法停用引用该条目的计划", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "trash_entry", trashed.OriginalPath, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
	http.Redirect(response, request, "/resources/trash", http.StatusSeeOther)
}

type trashView struct {
	ID           string
	OriginalPath string
	DeletedAt    time.Time
	Size         int64
	Directory    bool
}

func (a *App) trashPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = trashTemplate.Execute(response, struct {
			Entries      []trashView
			CSRFToken    string
			Pagination   paginationView
			Locale       webLocale
			DeferredData bool
		}{CSRFToken: current.csrfToken, Locale: locale, DeferredData: true})
		return
	}
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM trash_entries").Scan(&total); err != nil {
		http.Error(response, "无法读取回收站", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	rows, err := a.db.Query("SELECT id, original_path, deleted_at, size, is_directory FROM trash_entries ORDER BY deleted_at DESC LIMIT ? OFFSET ?", listPageSize, pagination.Start)
	if err != nil {
		http.Error(response, "无法读取回收站", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var entries []trashView
	for rows.Next() {
		var entry trashView
		var deletedAt int64
		if err := rows.Scan(&entry.ID, &entry.OriginalPath, &deletedAt, &entry.Size, &entry.Directory); err != nil {
			http.Error(response, "无法读取回收条目", http.StatusInternalServerError)
			return
		}
		entry.DeletedAt = time.Unix(deletedAt, 0).UTC()
		entries = append(entries, entry)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = trashTemplate.Execute(response, struct {
		Entries      []trashView
		CSRFToken    string
		Pagination   paginationView
		Locale       webLocale
		DeferredData bool
	}{Entries: entries, CSRFToken: current.csrfToken, Pagination: pagination, Locale: locale})
}

func (a *App) restoreTrash(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	action := request.FormValue("conflict_action")
	if !validConflictAction(action) {
		http.Error(response, "同名文件处理方式无效", http.StatusBadRequest)
		return
	}
	if action == conflictActionSkip {
		http.Redirect(response, request, "/resources/trash", http.StatusSeeOther)
		return
	}
	id := request.FormValue("id")
	var original, stored string
	if err := a.db.QueryRow("SELECT original_path, stored_path FROM trash_entries WHERE id = ?", id).Scan(&original, &stored); err != nil {
		http.Error(response, "回收条目不存在", http.StatusNotFound)
		return
	}
	parent, originalName := parentAndName(original)
	destination := original
	if action == conflictActionRename {
		newName := strings.TrimSpace(request.FormValue("new_name"))
		if newName == "" {
			http.Error(response, "请输入新的文件名", http.StatusBadRequest)
			return
		}
		if err := hostfiles.ValidateName(newName); err != nil {
			http.Error(response, "新文件名无效："+err.Error(), http.StatusBadRequest)
			return
		}
		renamedDestination, err := a.hostDestination(request.Context(), parent, newName)
		if err != nil {
			writeHostFileError(response, "恢复目标无效", err)
			return
		}
		destination = renamedDestination
	}
	_, _, targetErr := a.hostInfo(request.Context(), destination)
	targetExists := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		http.Error(response, "无法检查恢复目标："+targetErr.Error(), http.StatusConflict)
		return
	}
	if targetExists && action != conflictActionOverwrite {
		suggested, err := a.hostAvailableName(request.Context(), parent, originalName)
		if action == conflictActionRename {
			_, requestedName := parentAndName(destination)
			suggested, err = a.hostAvailableName(request.Context(), parent, requestedName)
		}
		if err != nil {
			http.Error(response, "无法生成可用名称："+err.Error(), http.StatusConflict)
			return
		}
		a.renderFileConflict(response, request, fileConflictView{
			Action: "/resources/trash/restore", BackURL: "/resources/trash", ID: id,
			ItemPath: destination, SuggestedName: suggested,
			CanOverwrite: !a.runs.ConflictsPath(destination),
		})
		return
	}
	if action == conflictActionOverwrite && targetExists && a.runs.ConflictsPath(destination) {
		http.Error(response, "活动运行正在使用同名目标，不能覆盖", http.StatusConflict)
		return
	}
	release, err := a.acquireFileMutationLease(destination)
	if err != nil {
		http.Error(response, "恢复目标正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	if err := a.commitTrashRestore(request.Context(), id, stored, destination, action == conflictActionOverwrite && targetExists); err != nil {
		http.Error(response, "无法恢复条目："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "restore_trash", destination, "succeeded")
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) purgeTrash(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "永久清理需要 CSRF 和明确确认", http.StatusForbidden)
		return
	}
	id := request.FormValue("id")
	var original, stored string
	if err := a.db.QueryRow("SELECT original_path, stored_path FROM trash_entries WHERE id = ?", id).Scan(&original, &stored); err != nil {
		http.Error(response, "回收条目不存在", http.StatusNotFound)
		return
	}
	if err := a.hostPurgeTrash(request.Context(), stored); err != nil {
		http.Error(response, "无法永久清理条目："+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := a.db.Exec("DELETE FROM trash_entries WHERE id = ?", id); err != nil {
		http.Error(response, "回收条目已清理，但无法更新记录", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "purge_trash", original, "succeeded")
	http.Redirect(response, request, "/resources/trash", http.StatusSeeOther)
}

func (a *App) uploadFiles(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	request.Body = http.MaxBytesReader(response, request.Body, 2<<30)
	reader, err := request.MultipartReader()
	if err != nil {
		http.Error(response, "上传请求必须使用 multipart/form-data", http.StatusBadRequest)
		return
	}
	var csrfToken, relative string
	conflictAction := ""
	fileCount := 0
	type uploadResult struct {
		Name, Result, Detail string
		Succeeded            bool
	}
	var results []uploadResult
	succeeded := 0
	directoryChecked := false
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			http.Error(response, "读取上传请求失败："+nextErr.Error(), http.StatusBadRequest)
			return
		}
		if part.FileName() == "" {
			value, readErr := io.ReadAll(io.LimitReader(part, 64<<10))
			_ = part.Close()
			if readErr != nil {
				http.Error(response, "读取上传字段失败", http.StatusBadRequest)
				return
			}
			switch part.FormName() {
			case "csrf_token":
				csrfToken = string(value)
			case "path":
				relative = string(value)
			case "replace":
				if string(value) == "yes" {
					conflictAction = conflictActionOverwrite
				}
			case "conflict_action":
				conflictAction = string(value)
			}
			continue
		}
		current := request.Context().Value(sessionContextKey).(session)
		if subtle.ConstantTimeCompare([]byte(current.csrfToken), []byte(csrfToken)) != 1 {
			_ = part.Close()
			http.Error(response, "CSRF Token 无效", http.StatusForbidden)
			return
		}
		fileCount++
		if fileCount > 100 {
			_ = part.Close()
			http.Error(response, "单批最多上传 100 个文件", http.StatusRequestEntityTooLarge)
			return
		}
		filename := part.FileName()
		if !directoryChecked {
			if relative == "" {
				_ = part.Close()
				http.Error(response, "上传目录不能为空", http.StatusBadRequest)
				return
			}
			if _, listErr := a.hostList(request.Context(), relative); listErr != nil {
				_ = part.Close()
				writeHostFileError(response, "上传目录无效", listErr)
				return
			}
			directoryChecked = true
		}
		if nameErr := hostfiles.ValidateName(filename); nameErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: secretredaction.String(nameErr.Error())})
			continue
		}
		targetPath, destinationErr := a.hostDestination(request.Context(), relative, filename)
		if destinationErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: secretredaction.String(destinationErr.Error())})
			continue
		}
		if !validConflictAction(conflictAction) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: webText(locale, "upload_results.invalid_conflict_action")})
			continue
		}
		targetInfo, _, targetErr := a.hostInfo(request.Context(), targetPath)
		targetExists := targetErr == nil
		if targetErr != nil && !os.IsNotExist(targetErr) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: secretredaction.String("无法检查同名文件：" + targetErr.Error())})
			continue
		}
		uploadName := filename
		replace := targetExists && conflictAction == conflictActionOverwrite
		if targetExists {
			switch conflictAction {
			case "", conflictActionSkip:
				_ = part.Close()
				results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.skipped"), Detail: webText(locale, "upload_results.kept_current")})
				a.recordAuditForRequest(request, "upload_file", filename, "skipped")
				continue
			case conflictActionRename:
				uploadName, err = a.hostAvailableName(request.Context(), relative, filename)
				if err != nil {
					_ = part.Close()
					results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "无法生成可用名称：" + err.Error()})
					continue
				}
				targetPath, err = a.hostDestination(request.Context(), relative, uploadName)
				if err != nil {
					_ = part.Close()
					results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: err.Error()})
					continue
				}
			}
		}
		if replace && (!targetInfo.Mode().IsRegular() || a.runs.ConflictsPath(targetPath)) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: webText(locale, "upload_results.cannot_overwrite")})
			a.recordAuditForRequest(request, "upload_file", filename, "rejected")
			continue
		}
		if a.executableUploadRequiresPublication(uploadName) {
			if replace {
				_ = part.Close()
				results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: webText(locale, "upload_results.executable_overwrite")})
				a.recordAuditForRequest(request, "stage_executable_upload", uploadName, "rejected")
				continue
			}
			pending, stageErr := a.uploadInbox.Receive(uploadinbox.Input{
				EntryID: "host-files", OriginalName: uploadName, TargetDirectory: relative, ConflictPolicy: "reject",
			}, part, 1<<30)
			_ = part.Close()
			if stageErr != nil {
				results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: secretredaction.String(stageErr.Error())})
				a.recordAuditForRequest(request, "stage_executable_upload", uploadName, "rejected")
				continue
			}
			a.recordAuditForRequest(request, "stage_executable_upload", pending.ID+" "+pending.SHA256+" "+relative+"/"+uploadName, "succeeded")
			results = append(results, uploadResult{
				Name: uploadName, Result: webText(locale, "upload_results.staged"),
				Detail: webText(locale, "upload_results.staged_detail"), Succeeded: true,
			})
			succeeded++
			continue
		}
		release, leaseErr := a.acquireFileMutationLease(targetPath)
		if leaseErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: secretredaction.String(leaseErr.Error())})
			a.recordAuditForRequest(request, "upload_file", filename, "rejected")
			continue
		}
		storedID, idErr := randomToken(18)
		if idErr != nil {
			release()
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "无法创建上传事务"})
			continue
		}
		trashed, uploadErr := a.hostUpload(request.Context(), relative, uploadName, part, 1<<30, replace, storedID)
		if uploadErr != nil {
			release()
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: secretredaction.String(uploadErr.Error())})
			a.recordAuditForRequest(request, "upload_file", filename, "rejected")
			continue
		}
		_ = part.Close()
		if trashed != nil {
			_, err = a.db.Exec(`INSERT INTO trash_entries
				(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
				VALUES (?, ?, ?, ?, ?, ?, ?, 0)`, storedID, trashed.OriginalPath, hostfiles.ComparisonKey(trashed.OriginalPath),
				trashed.StoredPath, hostfiles.ComparisonKey(trashed.StoredPath), time.Now().UTC().Unix(), trashed.Size)
			if err != nil {
				_ = a.hostRollbackTextSave(request.Context(), targetPath, trashed.StoredPath)
				release()
				results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "替换已回滚：无法记录旧文件"})
				a.recordAuditForRequest(request, "upload_file", filename, "failed")
				continue
			}
		}
		release()
		a.recordAuditForRequest(request, "upload_file", uploadName, "succeeded")
		detail := webText(locale, "upload_results.saved")
		if uploadName != filename {
			detail = fmt.Sprintf(webText(locale, "upload_results.renamed"), uploadName)
		}
		results = append(results, uploadResult{Name: uploadName, Result: webText(locale, "upload_results.succeeded"), Detail: detail, Succeeded: true})
		succeeded++
	}
	if fileCount == 0 {
		http.Error(response, "未选择上传文件", http.StatusBadRequest)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if succeeded < fileCount || len(results) > fileCount {
		response.WriteHeader(http.StatusMultiStatus)
	}
	if err := uploadResultsTemplate.Execute(response, struct {
		Link    string
		Results []uploadResult
		Locale  webLocale
	}{Link: filesURL(relative), Results: results, Locale: locale}); err != nil {
		http.Error(response, "文件已上传，但无法呈现结果："+err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) filesPage(response http.ResponseWriter, request *http.Request) {
	relative := request.URL.Query().Get("path")
	if strings.TrimSpace(relative) == "" {
		relative = a.files.InitialBrowsePath()
	}
	if relative != "" {
		var err error
		relative, err = a.hostCanonicalDirectory(request.Context(), relative)
		if err != nil {
			writeHostFileError(response, "无法读取主机目录", err)
			return
		}
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	showHidden := request.URL.Query().Get("show_hidden") == "1"
	sortField, direction := normalizeFileSort(request.URL.Query().Get("sort"), request.URL.Query().Get("direction"))
	parentURL := ""
	if parent, ok := hostPathParent(relative); ok {
		parentURL = filesStateURL(parent, "", sortField, direction, showHidden, 0)
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = filesTemplate.Execute(response, struct {
			CSRFToken, CurrentPath, Query, SortField, Direction        string
			SortSummary, RootURL, SearchURL, ParentURL                 string
			Locale                                                     webLocale
			Breadcrumbs                                                []fileBreadcrumbView
			DeferredData, ShowHidden                                   bool
			CanWrite, CanMutateCurrent, CanExecute, CanManageExecution bool
		}{
			CSRFToken: current.csrfToken, CurrentPath: relative,
			Query: query, SortField: sortField, Direction: direction, SortSummary: fileSortSummary(locale, sortField, direction),
			RootURL: filesStateURL("", "", sortField, direction, showHidden, 0), SearchURL: "/resources/files", ParentURL: parentURL,
			Locale: locale, Breadcrumbs: buildHostBreadcrumbs(relative, sortField, direction, showHidden), DeferredData: true, ShowHidden: showHidden,
			CanWrite: identity.Allows(current.role, identity.PermissionWriteFiles), CanMutateCurrent: identity.Allows(current.role, identity.PermissionWriteFiles) && relative != "", CanExecute: identity.Allows(current.role, identity.PermissionExecute),
			CanManageExecution: identity.Allows(current.role, identity.PermissionManageExecution),
		})
		return
	}
	entries, err := a.hostList(request.Context(), relative)
	if err != nil {
		writeHostFileError(response, "无法读取主机目录", err)
		return
	}
	listing := prepareFileListingWithContent(entries, query, sortField, direction, showHidden, func(listed listedFile) (fileCategory, bool) {
		return a.classifyFileContent(listed)
	})
	pagination := newPagination(request, len(listing))
	if pagination.HasPrevious {
		pagination.PreviousURL = filesStateURL(relative, query, sortField, direction, showHidden, pagination.Page-1)
	}
	if pagination.HasNext {
		pagination.NextURL = filesStateURL(relative, query, sortField, direction, showHidden, pagination.Page+1)
	}
	type fileView struct {
		hostfiles.Entry
		Path, BrowseURL, PinURL, DownloadURL, EditURL, PreviewURL, ViewURL, RunURL, QuickRunURL, MoveURL string
		LogURL                                                                                           string
		Protection, IconClass                                                                            string
		Runnable, IsHidden, CanMutate                                                                    bool
		NameParts                                                                                        []fileNamePart
		CategoryLabel                                                                                    string
	}
	pageEntries := listing[pagination.Start:pagination.End]
	views := make([]fileView, 0, pagination.End-pagination.Start)
	for _, listed := range pageEntries {
		entry, path := listed.Entry, listed.Path
		category := listed.Category
		displayCategory, previewableText := listed.DisplayCategory, listed.PreviewableText
		if !listed.ContentClassified {
			displayCategory, previewableText = a.classifyFileContent(listed)
		}
		_, canMutate, _ := a.hostInfo(request.Context(), path)
		view := fileView{
			Entry: entry, Path: path, IconClass: fileCategoryIcon(displayCategory),
			NameParts: splitFileNameMatches(entry.Name, query), CategoryLabel: fileCategoryLabel(locale, displayCategory),
			IsHidden: entry.Hidden, CanMutate: canMutate,
		}
		if view.CanMutate {
			view.MoveURL = routeFileURL("/resources/files/move", path)
		}
		if entry.VolumeType != "" {
			view.CategoryLabel = webText(locale, "files.volume."+entry.VolumeType)
			switch entry.VolumeType {
			case "removable":
				view.IconClass = "usb"
			case "network":
				view.IconClass = "network"
			default:
				view.IconClass = "hard-drive"
			}
		}
		if entry.Kind == hostfiles.Directory {
			view.BrowseURL = filesStateURL(path, "", sortField, direction, showHidden, 0)
			view.PinURL = filesURL(path)
		} else if entry.Kind == hostfiles.Regular {
			view.DownloadURL = routeFileURL("/resources/files/download", path)
			switch category {
			case fileCategoryImage:
				view.PreviewURL = routeFileURL("/resources/files/preview", path)
			case fileCategoryOther:
				if previewableText {
					view.ViewURL = routeFileURL("/resources/files/view", path)
				}
			case fileCategoryText, fileCategoryScript:
				if previewableText {
					view.ViewURL = routeFileURL("/resources/files/view", path)
					view.EditURL = routeFileURL("/resources/files/edit", path)
					view.LogURL = "/resources/files/log?" + url.Values{"path": {path}}.Encode()
				}
				if category == fileCategoryScript {
					view.Runnable = true
					view.RunURL = routeFileURL("/resources/files/run", path)
					view.QuickRunURL = routeFileURL("/resources/files/quick-run", path) + "&return_to=" + url.QueryEscape(request.URL.RequestURI())
				}
			}
		}
		views = append(views, view)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	breadcrumbs := buildHostBreadcrumbs(relative, sortField, direction, showHidden)
	_ = filesTemplate.Execute(response, struct {
		Entries             []fileView
		CSRFToken           string
		CurrentPath         string
		Query               string
		SortField           string
		Direction           string
		SortSummary         string
		RootURL             string
		ClearURL            string
		SearchURL           string
		Pagination          paginationView
		CanToggleExecutable bool
		CanWrite            bool
		CanMutateCurrent    bool
		CanExecute          bool
		CanManageExecution  bool
		ParentURL           string
		Breadcrumbs         []fileBreadcrumbView
		Locale              webLocale
		DeferredData        bool
		ShowHidden          bool
	}{
		Entries: views, CSRFToken: current.csrfToken, CurrentPath: relative,
		Query: query, SortField: sortField, Direction: direction, SortSummary: fileSortSummary(locale, sortField, direction),
		RootURL: filesStateURL("", "", sortField, direction, showHidden, 0), ClearURL: filesStateURL(relative, "", sortField, direction, showHidden, 0),
		SearchURL:  "/resources/files",
		Pagination: pagination, CanToggleExecutable: runtime.GOOS == "linux" && identity.Allows(current.role, identity.PermissionWriteFiles), ParentURL: parentURL,
		CanWrite: identity.Allows(current.role, identity.PermissionWriteFiles), CanMutateCurrent: identity.Allows(current.role, identity.PermissionWriteFiles) && relative != "", CanExecute: identity.Allows(current.role, identity.PermissionExecute),
		CanManageExecution: identity.Allows(current.role, identity.PermissionManageExecution),
		Breadcrumbs:        breadcrumbs, Locale: locale, ShowHidden: showHidden,
	})
}

func (a *App) classifyFileContent(listed listedFile) (fileCategory, bool) {
	category := listed.Category
	if listed.Kind != hostfiles.Regular || category != fileCategoryOther && category != fileCategoryText && category != fileCategoryScript {
		return category, false
	}
	likelyText, err := a.files.IsLikelyText(listed.Path, 64<<10)
	previewableText := err == nil && likelyText
	displayCategory := category
	if category == fileCategoryOther && previewableText {
		displayCategory = fileCategoryText
	} else if category == fileCategoryText && !previewableText {
		displayCategory = fileCategoryOther
	}
	return displayCategory, previewableText
}

type fileBreadcrumbView struct {
	Label string
	URL   string
}

func buildHostBreadcrumbs(path, sortField, direction string, showHidden bool) []fileBreadcrumbView {
	crumbs := hostfiles.Breadcrumbs(path)
	items := make([]fileBreadcrumbView, 0, len(crumbs))
	for _, crumb := range crumbs {
		items = append(items, fileBreadcrumbView{Label: crumb.Label, URL: filesStateURL(crumb.Path, "", sortField, direction, showHidden, 0)})
	}
	if len(items) > 0 {
		items[len(items)-1].URL = ""
	}
	return items
}

func (a *App) validateFileQuickAccess(response http.ResponseWriter, request *http.Request) {
	accessible := false
	if path := request.URL.Query().Get("path"); path != "" {
		if info, _, err := a.hostInfo(request.Context(), path); err == nil && info.IsDir() {
			accessible = true
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(map[string]bool{"accessible": accessible})
}

func isTextPreviewExtension(path string) bool {
	switch strings.ToLower(hostfiles.Extension(path)) {
	case ".txt", ".md", ".json", ".yaml", ".yml", ".toml", ".ini", ".conf", ".cfg", ".log", ".csv", ".tsv", ".xml", ".html", ".css", ".js", ".ts", ".go", ".py", ".ps1", ".cmd", ".bat", ".sh", ".sql":
		return true
	default:
		return false
	}
}

func isScriptExtension(path string) bool {
	switch strings.ToLower(hostfiles.Extension(path)) {
	case ".ps1", ".cmd", ".bat", ".sh", ".py":
		return true
	default:
		return false
	}
}

func highlightLanguageForPath(path string) string {
	switch strings.ToLower(hostfiles.Extension(path)) {
	case ".ps1":
		return "powershell"
	case ".cmd", ".bat":
		return "dos"
	case ".sh":
		return "bash"
	case ".py":
		return "python"
	default:
		return ""
	}
}

func routeFileURL(endpoint, path string) string {
	values := url.Values{}
	if path != "" {
		values.Set("path", path)
	}
	if len(values) == 0 {
		return endpoint
	}
	return endpoint + "?" + values.Encode()
}

func filesURL(path string) string {
	return routeFileURL("/resources/files", path)
}

func hostPathParent(path string) (string, bool) {
	return hostfiles.Parent(path)
}

func writeHostFileError(response http.ResponseWriter, action string, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, privilegebroker.ErrHostFilesUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, hostfiles.ErrProtected), os.IsPermission(err):
		status = http.StatusForbidden
	case os.IsNotExist(err):
		status = http.StatusNotFound
	}
	http.Error(response, action+"："+err.Error(), status)
}

func (a *App) acquireFileMutationLease(paths ...string) (func(), error) {
	token, err := randomToken(18)
	if err != nil {
		return nil, err
	}
	id := "file-mutation:" + token
	if err := a.files.AcquireLease(id, paths...); err != nil {
		return nil, err
	}
	return func() { a.files.ReleaseLease(id) }, nil
}

func (a *App) createDirectory(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	directory := request.FormValue("path")
	target, err := a.hostDestination(request.Context(), directory, request.FormValue("name"))
	if err != nil {
		writeHostFileError(response, "无法创建目录", err)
		return
	}
	release, err := a.acquireFileMutationLease(target)
	if err != nil {
		http.Error(response, "目录位置正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	if err := a.hostCreateDirectory(request.Context(), directory, hostfiles.Base(target)); err != nil {
		writeHostFileError(response, "无法创建目录", err)
		return
	}
	a.recordAuditForRequest(request, "create_directory", target, "succeeded")
	http.Redirect(response, request, filesURL(directory), http.StatusSeeOther)
}

func (a *App) accountUsernameTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if current.role != identity.RoleAdministrator {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "account-username",
		Title:       webText(resolveWebLocale(request), "account.change_username"),
		Description: webText(resolveWebLocale(request), "account.change_username_description"),
		BackURL:     "/settings/account",
		Action:      "/settings/account/username",
		Name:        current.username,
	})
}

func (a *App) accountPasswordTask(response http.ResponseWriter, request *http.Request) {
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "account-password",
		Title:       webText(resolveWebLocale(request), "account.change_password"),
		Description: webText(resolveWebLocale(request), "account.change_password_description"),
		BackURL:     "/settings/account",
		Action:      "/settings/account/password",
	})
}

func (a *App) changeUsername(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if current.role != identity.RoleAdministrator {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}

	var username, passwordHash string
	if err := a.db.QueryRow("SELECT username, password_hash FROM users WHERE id = ?", current.userID).Scan(&username, &passwordHash); err != nil {
		http.Error(response, "无法读取用户账号", http.StatusInternalServerError)
		return
	}
	if !verifyPasswordContext(request.Context(), request.FormValue("current_password"), passwordHash) {
		http.Error(response, "当前密码错误", http.StatusUnauthorized)
		return
	}
	newUsername := strings.TrimSpace(request.FormValue("username"))
	if !validUsername(newUsername) {
		http.Error(response, "用户名必须为 1 至 64 个有效 Unicode 字符", http.StatusBadRequest)
		return
	}
	if newUsername == username {
		http.Redirect(response, request, "/settings/account", http.StatusSeeOther)
		return
	}

	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法保存用户名", http.StatusInternalServerError)
		return
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.Exec("UPDATE users SET username = ?, auth_version = auth_version + 1, updated_at = ? WHERE id = ?",
		newUsername, time.Now().UTC().Unix(), current.userID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(response, "用户名已存在", http.StatusConflict)
			return
		}
		http.Error(response, "无法保存用户名", http.StatusInternalServerError)
		return
	}
	if _, err := transaction.Exec("DELETE FROM sessions WHERE user_id = ?", current.userID); err != nil {
		http.Error(response, "无法撤销会话", http.StatusInternalServerError)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "无法保存用户名", http.StatusInternalServerError)
		return
	}
	a.cancelAuthenticatedRequests(current.userID)
	a.recordAuditForRequest(request, "rename_self", username+" -> "+newUsername, "succeeded")
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteLaxMode})
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) changePassword(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}

	var username, passwordHash string
	if err := a.db.QueryRow("SELECT username, password_hash FROM users WHERE id = ?", current.userID).Scan(&username, &passwordHash); err != nil {
		http.Error(response, "无法读取用户账号", http.StatusInternalServerError)
		return
	}
	if !verifyPassword(request.FormValue("current_password"), passwordHash) {
		http.Error(response, "当前密码错误", http.StatusUnauthorized)
		return
	}
	newPassword := request.FormValue("new_password")
	if newPassword != request.FormValue("confirm_password") {
		http.Error(response, "两次输入的新密码不一致", http.StatusBadRequest)
		return
	}
	if validatePasswordPolicy(newPassword, username) != nil || verifyPassword(newPassword, passwordHash) {
		http.Error(response, webText(resolveWebLocale(request), "account.password_policy_error"), http.StatusBadRequest)
		return
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}

	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.Exec("UPDATE users SET password_hash = ?, auth_version = auth_version + 1, updated_at = ? WHERE id = ?", newHash, time.Now().UTC().Unix(), current.userID); err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}
	if _, err := transaction.Exec("DELETE FROM sessions WHERE user_id = ?", current.userID); err != nil {
		http.Error(response, "无法撤销会话", http.StatusInternalServerError)
		return
	}
	if current.role == identity.RoleAdministrator {
		passwordPath := filepath.Join(a.stateRoot, "secrets", initialPasswordFilename)
		if err := os.Remove(passwordPath); err != nil && !os.IsNotExist(err) {
			http.Error(response, "无法删除一次性密码文件", http.StatusInternalServerError)
			return
		}
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}
	a.cancelAuthenticatedRequests(current.userID)
	a.recordAuditForRequest(request, "change_password", username, "succeeded")
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteLaxMode})
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) login(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	resetReadDeadline := setRequestReadDeadline(response, unauthenticatedFormReadTimeout)
	defer resetReadDeadline()
	request.Body = http.MaxBytesReader(response, request.Body, maxLoginRequestBytes)
	defer removeMultipartForm(request)
	if err := parseRequestForm(request, maxLoginRequestBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(response, "登录请求过大", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(response, "登录表单无效", http.StatusBadRequest)
		}
		return
	}
	csrfCookie, err := request.Cookie(loginCSRFCookieName)
	if err != nil || subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(request.FormValue("csrf_token"))) != 1 {
		renderLoginFailure(response, request, http.StatusForbidden, request.FormValue("username"), "登录页面已过期，请重试")
		return
	}
	remoteHost, _, splitErr := net.SplitHostPort(request.RemoteAddr)
	if splitErr != nil {
		remoteHost = request.RemoteAddr
	}
	requestedUsername := strings.TrimSpace(request.FormValue("username"))
	validLoginIdentity := validUsername(requestedUsername)
	loginIdentity := requestedUsername
	if !validLoginIdentity {
		loginIdentity = "<invalid>"
	}
	loginKeys := []string{a.loginRateKey("ip", remoteHost), a.loginRateKey("account", loginIdentity)}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-request.Context().Done():
		return
	}
	if retryAfter := a.loginRetryAfter(loginKeys...); retryAfter > 0 {
		response.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		a.recordAuditForRequest(request, "login", loginIdentity, "rate_limited")
		renderLoginFailure(response, request, http.StatusTooManyRequests, request.FormValue("username"), "登录尝试过于频繁，请稍后重试")
		return
	}

	var userID, username, passwordHash string
	var role identity.Role
	var enabled bool
	var authVersion int64
	if validLoginIdentity {
		err = a.db.QueryRow("SELECT id, username, password_hash, role, enabled, auth_version FROM users WHERE username = ?", requestedUsername).Scan(
			&userID,
			&username,
			&passwordHash,
			&role,
			&enabled,
			&authVersion,
		)
	} else {
		err = sql.ErrNoRows
	}
	candidateHash := passwordHash
	if err != nil || !enabled {
		candidateHash = dummyPasswordHash
	}
	passwordMatches := verifyPasswordContext(request.Context(), request.FormValue("password"), candidateHash)
	if err != nil || !enabled || !passwordMatches {
		a.recordLoginFailure(loginKeys...)
		a.recordAuditForRequest(request, "login", loginIdentity, "failed")
		renderLoginFailure(response, request, http.StatusUnauthorized, request.FormValue("username"), webText(resolveWebLocale(request), "login.invalid_credentials"))
		return
	}
	mfaStatus, err := a.mfa.Status(userID)
	if err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), webText(resolveWebLocale(request), "mfa.unavailable"))
		return
	}
	passkeyUser, err := a.passkeys.User(userID, username)
	if err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), webText(resolveWebLocale(request), "mfa.unavailable"))
		return
	}
	if mfaStatus.Enabled || len(passkeyUser.Credentials) > 0 {
		challengeID, challengeErr := a.loginChallenges.put(loginChallenge{
			UserID: userID, Username: username, Role: role, AuthVersion: authVersion, RemoteHost: remoteHost,
			MFAEnabled: mfaStatus.Enabled, PasskeyEnabled: len(passkeyUser.Credentials) > 0,
		}, time.Now().UTC())
		if challengeErr != nil {
			renderLoginFailure(response, request, http.StatusServiceUnavailable, requestedUsername, webText(resolveWebLocale(request), "mfa.unavailable"))
			return
		}
		http.SetCookie(response, &http.Cookie{
			Name: loginChallengeCookieName, Value: challengeID, Path: "/", MaxAge: int(loginChallengeLifetime.Seconds()),
			HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteStrictMode,
		})
		completeLogin(response, request, "/login/verify")
		return
	}
	a.clearLoginFailures(loginKeys...)
	a.finishLogin(response, request, userID, username, role, authVersion, 1)
}

func (a *App) loginVerificationPage(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	challengeID, challenge, ok := a.pendingLoginChallenge(request)
	if !ok || !a.loginChallengeCurrent(challenge) {
		if ok {
			a.loginChallenges.delete(challengeID)
		}
		expireLoginChallengeCookie(response, request)
		http.Redirect(response, request, "/login", http.StatusSeeOther)
		return
	}
	renderLoginVerificationPage(response, request, http.StatusOK, challenge, "")
}

func (a *App) verifyLoginFactor(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	resetReadDeadline := setRequestReadDeadline(response, unauthenticatedFormReadTimeout)
	defer resetReadDeadline()
	request.Body = http.MaxBytesReader(response, request.Body, maxLoginRequestBytes)
	defer removeMultipartForm(request)
	if err := parseRequestForm(request, maxLoginRequestBytes); err != nil {
		http.Error(response, "invalid login verification form", http.StatusBadRequest)
		return
	}
	csrfCookie, err := request.Cookie(loginCSRFCookieName)
	if err != nil || subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(request.FormValue("csrf_token"))) != 1 {
		a.renderLoginVerificationFailure(response, request, http.StatusForbidden, webText(resolveWebLocale(request), "error.forbidden"))
		return
	}
	challengeID, challenge, ok := a.pendingLoginChallenge(request)
	if !ok {
		expireLoginChallengeCookie(response, request)
		a.renderLoginVerificationFailure(response, request, http.StatusUnauthorized, webText(resolveWebLocale(request), "login.verification_expired"))
		return
	}
	if !a.loginChallengeCurrent(challenge) {
		a.loginChallenges.delete(challengeID)
		expireLoginChallengeCookie(response, request)
		a.renderLoginVerificationFailure(response, request, http.StatusUnauthorized, webText(resolveWebLocale(request), "login.verification_expired"))
		return
	}
	remoteHost := loginRemoteHost(request)
	loginKeys := []string{a.loginRateKey("ip", remoteHost), a.loginRateKey("account", challenge.Username)}
	if retryAfter := a.loginRetryAfter(loginKeys...); retryAfter > 0 {
		response.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		a.recordAuditForRequest(request, "login", challenge.Username, "rate_limited")
		a.renderLoginVerificationFailure(response, request, http.StatusTooManyRequests, webText(resolveWebLocale(request), "login.too_many_attempts"))
		return
	}

	verified := false
	var verifyErr error
	if assertion := request.FormValue("passkey_response"); assertion != "" && challenge.PasskeyEnabled {
		verified, verifyErr = a.verifyPasskeyAssertion(request, challenge.UserID, challenge.Username, "login", challengeID, request.FormValue("passkey_ceremony"), assertion)
	} else if challenge.MFAEnabled {
		verified, verifyErr = a.mfa.Verify(challenge.UserID, request.FormValue("mfa_code"))
	}
	if verifyErr != nil {
		a.renderLoginVerificationFailure(response, request, http.StatusInternalServerError, webText(resolveWebLocale(request), "mfa.unavailable"))
		return
	}
	if !verified {
		a.recordLoginFailure(loginKeys...)
		a.recordAuditForRequest(request, "login", challenge.Username, "failed")
		a.renderLoginVerificationFailure(response, request, http.StatusUnauthorized, webText(resolveWebLocale(request), "login.invalid_verification"))
		return
	}
	if _, ok := a.loginChallenges.take(challengeID, remoteHost, time.Now().UTC()); !ok {
		a.renderLoginVerificationFailure(response, request, http.StatusUnauthorized, webText(resolveWebLocale(request), "login.verification_expired"))
		return
	}
	a.clearLoginFailures(loginKeys...)
	expireLoginChallengeCookie(response, request)
	a.finishLogin(response, request, challenge.UserID, challenge.Username, challenge.Role, challenge.AuthVersion, 2)
}

func (a *App) finishLogin(response http.ResponseWriter, request *http.Request, userID, username string, role identity.Role, authVersion int64, authenticationAssurance int) {

	token, err := randomToken(32)
	if err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	sessionCSRF, err := randomToken(32)
	if err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	now := time.Now().UTC()
	if _, err := a.db.Exec(
		"INSERT INTO sessions (token_hash, user_id, auth_version, authentication_assurance, reauthenticated_at, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		hashToken(token), userID, authVersion, authenticationAssurance, now.Unix(), sessionCSRF, now.Unix(), now.Unix(), now.Add(7*24*time.Hour).Unix(),
	); err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(request),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60,
	})
	http.SetCookie(response, &http.Cookie{Name: loginCSRFCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteStrictMode})
	auditSession := session{userID: userID, username: username, role: role, authVersion: authVersion, authenticationAssurance: authenticationAssurance, reauthenticatedAt: now.Unix()}
	auditRequest := request.WithContext(context.WithValue(request.Context(), sessionContextKey, auditSession))
	a.recordAuditWithRequestActor(auditRequest, "login", username, "succeeded", request.RemoteAddr, userID, username, role)
	completeLogin(response, request, "/monitor")
}

func (a *App) pendingLoginChallenge(request *http.Request) (string, loginChallenge, bool) {
	cookie, err := request.Cookie(loginChallengeCookieName)
	if err != nil || cookie.Value == "" {
		return "", loginChallenge{}, false
	}
	challenge, ok := a.loginChallenges.get(cookie.Value, loginRemoteHost(request), time.Now().UTC())
	return cookie.Value, challenge, ok
}

func (a *App) loginChallengeCurrent(challenge loginChallenge) bool {
	var username string
	var role identity.Role
	var enabled bool
	var authVersion int64
	err := a.db.QueryRow(`SELECT username, role, enabled, auth_version FROM users WHERE id = ?`, challenge.UserID).Scan(&username, &role, &enabled, &authVersion)
	return err == nil && enabled && username == challenge.Username && role == challenge.Role && authVersion == challenge.AuthVersion
}

func loginRemoteHost(request *http.Request) string {
	remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return remoteHost
}

func expireLoginChallengeCookie(response http.ResponseWriter, request *http.Request) {
	http.SetCookie(response, &http.Cookie{Name: loginChallengeCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteStrictMode})
}

func setRequestReadDeadline(response http.ResponseWriter, timeout time.Duration) func() {
	controller := http.NewResponseController(response)
	if err := controller.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return func() {}
	}
	return func() {
		_ = controller.SetReadDeadline(time.Time{})
	}
}

func parseRequestForm(request *http.Request, maxMemory int64) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	if mediaType == "multipart/form-data" {
		return request.ParseMultipartForm(maxMemory)
	}
	return request.ParseForm()
}

func removeMultipartForm(request *http.Request) {
	if request.MultipartForm != nil {
		_ = request.MultipartForm.RemoveAll()
	}
}

func (a *App) logout(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if cookie, err := request.Cookie(sessionCookieName); err == nil {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(cookie.Value))
	}
	current := request.Context().Value(sessionContextKey).(session)
	a.cancelAuthenticatedRequests(current.userID)
	a.recordAuditForRequest(request, "logout", current.username, "succeeded")
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteLaxMode})
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) loginRetryAfter(keys ...string) time.Duration {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	now := time.Now()
	a.pruneLoginFailuresLocked(now)
	var longest time.Duration
	for _, key := range keys {
		if remaining := a.loginFailures[key].blockedUntil.Sub(now); remaining > longest {
			longest = remaining
		}
	}
	return longest
}

func (a *App) loginRateKey(scope, value string) string {
	hash := sha256.New()
	_, _ = hash.Write(a.loginRateSalt[:])
	_, _ = hash.Write([]byte(scope))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(value))
	digest := hash.Sum(nil)
	bucket := ((int(digest[0]) << 8) | int(digest[1])) & (loginRateBucketCount - 1)
	return scope + "\x00" + strconv.Itoa(bucket)
}

func (a *App) recordLoginFailure(keys ...string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	now := time.Now()
	a.pruneLoginFailuresLocked(now)
	for _, key := range keys {
		failure, exists := a.loginFailures[key]
		if !exists && len(a.loginFailures) >= maxLoginFailureEntries {
			continue
		}
		failure.count++
		if failure.count >= 5 {
			exponent := failure.count - 5
			delay := 2 * time.Second
			if exponent >= 8 {
				delay = 5 * time.Minute
			} else {
				delay *= time.Duration(1 << exponent)
			}
			failure.blockedUntil = now.Add(delay)
		}
		failure.updatedAt = now
		a.loginFailures[key] = failure
	}
}

func (a *App) pruneLoginFailuresLocked(now time.Time) {
	if !a.loginLastPrune.IsZero() && now.Sub(a.loginLastPrune) < time.Minute {
		return
	}
	for key, failure := range a.loginFailures {
		if now.Sub(failure.updatedAt) >= time.Hour && !failure.blockedUntil.After(now) {
			delete(a.loginFailures, key)
		}
	}
	a.loginLastPrune = now
}

func (a *App) clearLoginFailures(keys ...string) {
	a.loginMu.Lock()
	for _, key := range keys {
		delete(a.loginFailures, key)
	}
	a.loginMu.Unlock()
}

type session struct {
	userID                  string
	username                string
	role                    identity.Role
	authVersion             int64
	authenticationAssurance int
	reauthenticatedAt       int64
	tokenHash               string
	rawToken                string
	csrfToken               string
	mfaRequiredAt           int64
}

func (a *App) loadSession(request *http.Request) (session, string, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return session{}, "", false
	}
	var current session
	var lastSeen, expiresAt int64
	current.tokenHash = hashToken(cookie.Value)
	current.rawToken = cookie.Value
	err = a.db.QueryRow(`
		SELECT sessions.csrf_token, sessions.last_seen_at, sessions.expires_at,
			sessions.authentication_assurance, sessions.reauthenticated_at,
			users.id, users.username, users.role, users.auth_version, users.mfa_required_at
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.auth_version = users.auth_version AND users.enabled = 1`, current.tokenHash,
	).Scan(&current.csrfToken, &lastSeen, &expiresAt, &current.authenticationAssurance, &current.reauthenticatedAt,
		&current.userID, &current.username, &current.role, &current.authVersion, &current.mfaRequiredAt)
	now := time.Now().UTC()
	if err != nil || now.Unix() >= expiresAt || now.Sub(time.Unix(lastSeen, 0)) >= 12*time.Hour {
		if err == nil {
			_, _ = a.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(cookie.Value))
		}
		return session{}, "", false
	}
	return current, current.username, true
}

func validSessionCSRF(request *http.Request) bool {
	return validSessionCSRFValue(request, request.FormValue("csrf_token"))
}

func validSessionCSRFValue(request *http.Request, value string) bool {
	current, ok := request.Context().Value(sessionContextKey).(session)
	return ok && subtle.ConstantTimeCompare([]byte(current.csrfToken), []byte(value)) == 1
}

func (a *App) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, _, ok := a.loadSession(request)
		if !ok {
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		if privilegedMFAEnrollmentDue(current, time.Now().UTC()) {
			status, statusErr := a.mfa.Status(current.userID)
			if statusErr != nil {
				http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
				return
			}
			passkeyUser, passkeyErr := a.passkeys.User(current.userID, current.username)
			if passkeyErr != nil {
				http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
				return
			}
			if !status.Enabled && len(passkeyUser.Credentials) == 0 && !mfaEnrollmentRouteAllowed(request.URL.Path) {
				response.Header().Set("Cache-Control", "no-store")
				auditRequest := request.WithContext(context.WithValue(request.Context(), sessionContextKey, current))
				a.recordAuditWithRequestActor(auditRequest, "mfa_enrollment_required", current.username, "blocked", request.RemoteAddr, current.userID, current.username, current.role)
				if request.Method == http.MethodGet || request.Method == http.MethodHead {
					http.Redirect(response, request, "/settings/account/mfa", http.StatusSeeOther)
				} else {
					http.Error(response, webText(resolveWebLocale(request), "mfa.enrollment_required"), http.StatusForbidden)
				}
				return
			}
		}
		cookie, _ := request.Cookie(sessionCookieName)
		now := time.Now().UTC()
		if !a.validation.Load() {
			_, _ = a.db.Exec("UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", now.Unix(), hashToken(cookie.Value))
		}
		authenticatedContext := context.WithValue(request.Context(), sessionContextKey, current)
		correlationID, _ := request.Context().Value(requestIDContextKey).(string)
		authenticatedContext = privilegebroker.WithAuthorization(authenticatedContext, privilegebroker.Authorization{
			SessionToken: current.rawToken, RequestID: correlationID,
		})
		authenticatedContext, cancel := context.WithCancel(authenticatedContext)
		requestID := a.registerAuthenticatedRequest(current.userID, cancel)
		defer a.unregisterAuthenticatedRequest(current.userID, requestID)
		defer cancel()
		next.ServeHTTP(response, request.WithContext(authenticatedContext))
	})
}

func privilegedMFAEnrollmentDue(current session, now time.Time) bool {
	return (current.role == identity.RoleAdministrator || current.role == identity.RoleMaintainer) &&
		current.mfaRequiredAt > 0 && !now.Before(time.Unix(current.mfaRequiredAt, 0))
}

func mfaEnrollmentRouteAllowed(path string) bool {
	return path == "/settings/account" || strings.HasPrefix(path, "/settings/account/mfa") ||
		strings.HasPrefix(path, "/settings/account/passkeys") ||
		path == "/auth/step-up" || path == "/logout" || path == "/settings/locale"
}

func (a *App) registerAuthenticatedRequest(userID string, cancel context.CancelFunc) uint64 {
	a.activeRequestsMu.Lock()
	defer a.activeRequestsMu.Unlock()
	if a.activeRequests == nil {
		a.activeRequests = make(map[string]map[uint64]context.CancelFunc)
	}
	a.activeRequestID++
	if a.activeRequests[userID] == nil {
		a.activeRequests[userID] = make(map[uint64]context.CancelFunc)
	}
	a.activeRequests[userID][a.activeRequestID] = cancel
	return a.activeRequestID
}

func (a *App) unregisterAuthenticatedRequest(userID string, requestID uint64) {
	a.activeRequestsMu.Lock()
	defer a.activeRequestsMu.Unlock()
	requests := a.activeRequests[userID]
	delete(requests, requestID)
	if len(requests) == 0 {
		delete(a.activeRequests, userID)
	}
}

func (a *App) cancelAuthenticatedRequests(userID string) {
	a.activeRequestsMu.Lock()
	requests := a.activeRequests[userID]
	delete(a.activeRequests, userID)
	a.activeRequestsMu.Unlock()
	for _, cancel := range requests {
		cancel()
	}
}

func (a *App) cancelAllAuthenticatedRequests() {
	a.activeRequestsMu.Lock()
	active := a.activeRequests
	a.activeRequests = make(map[string]map[uint64]context.CancelFunc)
	a.activeRequestsMu.Unlock()
	for _, requests := range active {
		for _, cancel := range requests {
			cancel()
		}
	}
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (a *App) recordAudit(action, target, result, source string) {
	a.recordAuditWithActor(action, target, result, source, "", "", "")
}

func (a *App) recordAuditForRequest(request *http.Request, action, target, result string) {
	current, _ := request.Context().Value(sessionContextKey).(session)
	a.recordAuditWithRequestActor(request, action, target, result, request.RemoteAddr, current.userID, current.username, current.role)
}

func (a *App) recordAuditWithRequestActor(request *http.Request, action, target, result, source, actorUserID, actorUsername string, actorRole identity.Role) {
	a.recordAuditResourceWithRequestActor(request, action, target, result, source, actorUserID, actorUsername, actorRole, "", "")
}

func (a *App) recordAuditResourceForRequest(request *http.Request, action, target, result, revision, digest string) {
	current, _ := request.Context().Value(sessionContextKey).(session)
	a.recordAuditResourceWithRequestActor(request, action, target, result, request.RemoteAddr, current.userID, current.username, current.role, revision, digest)
}

func (a *App) recordAuditResourceWithRequestActor(request *http.Request, action, target, result, source, actorUserID, actorUsername string, actorRole identity.Role, revision, digest string) {
	requestID, _ := request.Context().Value(requestIDContextKey).(string)
	assurance := ""
	if current, ok := request.Context().Value(sessionContextKey).(session); ok {
		assurance = "aal" + strconv.Itoa(current.authenticationAssurance)
		if identity.RecentAuthenticationValid(current.reauthenticatedAt, time.Now().UTC()) {
			assurance += "+step-up"
		}
	} else if actorRole == identity.Role("external") {
		assurance = "external-capability"
	}
	_, _ = a.auditLog.Append(context.Background(), auditlog.Event{
		OccurredAt: strconv.FormatInt(time.Now().UTC().Unix(), 10),
		Action:     action, Target: target, Result: result, SourceAddress: source,
		ActorUserID: actorUserID, ActorUsername: actorUsername, ActorRole: string(actorRole),
		RequestID: requestID, AuthenticationAssurance: assurance,
		ResourceRevision: revision, ResourceDigestSHA256: digest,
	})
}

func (a *App) recordAuditWithActor(action, target, result, source, actorUserID, actorUsername string, actorRole identity.Role) {
	_, _ = a.auditLog.Append(context.Background(), auditlog.Event{
		OccurredAt: strconv.FormatInt(time.Now().UTC().Unix(), 10),
		Action:     action, Target: target, Result: result, SourceAddress: source,
		ActorUserID: actorUserID, ActorUsername: actorUsername, ActorRole: string(actorRole),
	})
}

type loginPageData struct {
	CSRFToken      string
	Username       string
	Error          string
	Locale         webLocale
	SecondFactor   bool
	MFAEnabled     bool
	PasskeyEnabled bool
}

func renderLoginPage(response http.ResponseWriter, request *http.Request, status int, username, errorMessage string) {
	token := ""
	if cookie, err := request.Cookie(loginCSRFCookieName); err == nil {
		token = cookie.Value
	}
	if token == "" {
		var err error
		token, err = randomToken(32)
		if err != nil {
			http.Error(response, "无法创建登录表单", http.StatusInternalServerError)
			return
		}
		http.SetCookie(response, &http.Cookie{
			Name:     loginCSRFCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecureRequest(request),
			SameSite: http.SameSiteStrictMode,
		})
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	locale := resolveWebLocale(request)
	response.Header().Set("Content-Language", string(locale))
	_ = loginTemplate.Execute(response, loginPageData{CSRFToken: token, Username: username, Error: errorMessage, Locale: locale})
}

func renderLoginVerificationPage(response http.ResponseWriter, request *http.Request, status int, challenge loginChallenge, errorMessage string) {
	token := ""
	if cookie, err := request.Cookie(loginCSRFCookieName); err == nil {
		token = cookie.Value
	}
	if token == "" {
		var err error
		token, err = randomToken(32)
		if err != nil {
			http.Error(response, "unable to create login verification form", http.StatusInternalServerError)
			return
		}
		http.SetCookie(response, &http.Cookie{Name: loginCSRFCookieName, Value: token, Path: "/", HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteStrictMode})
	}
	locale := resolveWebLocale(request)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Language", string(locale))
	response.WriteHeader(status)
	_ = loginTemplate.Execute(response, loginPageData{
		CSRFToken: token, Username: challenge.Username, Error: errorMessage, Locale: locale, SecondFactor: true,
		MFAEnabled: challenge.MFAEnabled, PasskeyEnabled: challenge.PasskeyEnabled,
	})
}

func (a *App) renderPendingLoginVerification(response http.ResponseWriter, request *http.Request, status int, errorMessage string) {
	_, challenge, ok := a.pendingLoginChallenge(request)
	if !ok {
		renderLoginPage(response, request, status, "", errorMessage)
		return
	}
	renderLoginVerificationPage(response, request, status, challenge, errorMessage)
}

func (a *App) renderLoginVerificationFailure(response http.ResponseWriter, request *http.Request, status int, errorMessage string) {
	if !acceptsJSON(request) {
		a.renderPendingLoginVerification(response, request, status, errorMessage)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": errorMessage})
}

func renderLoginFailure(response http.ResponseWriter, request *http.Request, status int, username, errorMessage string) {
	if !acceptsJSON(request) {
		renderLoginPage(response, request, status, username, errorMessage)
		return
	}
	token, err := randomToken(32)
	if err != nil {
		http.Error(response, "无法创建登录表单", http.StatusInternalServerError)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     loginCSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(request),
		SameSite: http.SameSiteStrictMode,
	})
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": errorMessage, "csrf_token": token})
}

func completeLogin(response http.ResponseWriter, request *http.Request, destination string) {
	if !acceptsJSON(request) {
		http.Redirect(response, request, destination, http.StatusSeeOther)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(map[string]string{"redirect": destination})
}

func acceptsJSON(request *http.Request) bool {
	return strings.Contains(request.Header.Get("Accept"), "application/json")
}
