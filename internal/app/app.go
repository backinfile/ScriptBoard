package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/binary"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/assistant"
	"scriptboard/internal/assistant/raster"
	"scriptboard/internal/assistant/runtimeinstall"
	"scriptboard/internal/assistant/toolbroker"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/customdashboard"
	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/hostsecurity"
	"scriptboard/internal/hoststatus"
	"scriptboard/internal/instancelock"
	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/privatepath"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/scheduler"
	updatepkg "scriptboard/internal/update"
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

//go:embed web/assets/* web/templates/*
var webFiles embed.FS

func mustWebAsset(path string) string {
	content, err := webFiles.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(content)
}

func mustWebTemplate(name string) *template.Template {
	path := "web/templates/" + name + ".html"
	return template.Must(template.New(name).Funcs(webTemplateFunctions()).Parse(
		mustWebAsset("web/templates/deferred-region.html") +
			mustWebAsset("web/templates/settings-navigation.html") +
			mustWebAsset(path),
	))
}

func webTemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"assetVersion": func() string { return webAssetVersion },
		"join":         strings.Join,
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
	sessionCookieName   = "scriptboard_session"
	loginCSRFCookieName = "scriptboard_login_csrf"
)

type contextKey string

const (
	sessionContextKey contextKey = "session"
	secureContextKey  contextKey = "secure-request"
)

type Config struct {
	StateRoot              string
	ConfigPath             string
	InstallRoot            string
	TLSKey                 string
	FileTopology           hostfiles.Topology
	RunTimeoutGrace        time.Duration
	SchedulerNow           func() time.Time
	SchedulerTick          time.Duration
	ExecutorChains         map[string][]string
	AdminUsername          string
	AdminPassword          string
	AdminPasswordFile      string
	TrustedProxies         []string
	WebsiteMonitorOptions  websitemonitor.Options
	UpdateCheck            bool
	UpdateInterval         time.Duration
	UpdateSource           updatepkg.ReleaseSource
	RequestShutdown        func()
	RequestRestart         func() error
	ApplicationProbe       appstatus.Probe
	AssistantRuntimeSource runtimeinstall.Source
	HostSecurity           hostsecurity.Service
}

type App struct {
	db                 *sql.DB
	stateRoot          string
	assistant          *assistant.Service
	assistantRuntime   *assistantRuntimeCoordinator
	assistantRuntimes  *runtimeinstall.Manager
	assistantTools     *assistantToolExecutor
	assistantRaster    *raster.Processor
	assistantBroker    *toolbroker.Broker
	files              *hostfiles.Manager
	fileOperations     *sqliteFileOperationStore
	fileMoves          *hostfiles.MoveEngine
	fileOperationCtx   context.Context
	fileOperationStop  context.CancelFunc
	fileOperationWG    sync.WaitGroup
	runs               *runmanager.Manager
	scheduler          *scheduler.Manager
	hostStatus         *hoststatus.Monitor
	hostSecurity       hostsecurity.Service
	securityDraftMu    sync.Mutex
	securityDrafts     map[string]securityFirewallDraft
	applicationStatus  *appstatus.Monitor
	logStreamSlots     chan struct{}
	logHistorySlots    chan struct{}
	shellStatusCache   *shellStatusCache
	websiteMonitor     *websitemonitor.Manager
	customDashboards   *customdashboard.Manager
	externalTriggers   *externaltrigger.Manager
	externalLimit      *externaltrigger.Limiter
	mysql              *mysqlmanager.Manager
	mysqlContext       context.Context
	mysqlCancel        context.CancelFunc
	mysqlWG            sync.WaitGroup
	instanceLock       *instancelock.Lock
	handler            http.Handler
	loginMu            sync.Mutex
	loginSlots         chan struct{}
	loginFailures      map[string]loginFailure
	loginLastPrune     time.Time
	loginRateSalt      [32]byte
	activeRequestsMu   sync.Mutex
	activeRequests     map[string]map[uint64]context.CancelFunc
	activeRequestID    uint64
	credentialOverride bool
	trustedProxies     []*net.IPNet
	updates            *updatepkg.Manager
	requestRestart     func() error
	instanceID         string
	restartRequested   atomic.Bool
	updateCancel       context.CancelFunc
	updateContext      context.Context
	updateResultsWake  chan struct{}
	validation         atomic.Bool
	validationID       string
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
	installRoot := strings.TrimSpace(config.InstallRoot)
	if installRoot == "" {
		if executable, executableErr := os.Executable(); executableErr == nil {
			installRoot = filepath.Dir(executable)
		}
	}
	instanceDigest := sha256.Sum256([]byte(stateRoot))
	files, err := hostfiles.Open(hostfiles.Options{
		ProtectedPaths: []string{stateRoot, installRoot, config.ConfigPath, config.AdminPasswordFile, config.TLSKey},
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
	var loginRateSalt [32]byte
	if _, err := rand.Read(loginRateSalt[:]); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("生成登录限流密钥: %w", err)
	}
	hostSecurityService := config.HostSecurity
	if hostSecurityService == nil {
		hostSecurityService = hostsecurity.NewManager(hostsecurity.Options{})
	}
	application := &App{
		db: db, stateRoot: stateRoot, files: files, instanceLock: instanceLock,
		loginSlots: make(chan struct{}, 2), loginFailures: make(map[string]loginFailure), trustedProxies: trustedProxies,
		loginRateSalt:  loginRateSalt,
		logStreamSlots: make(chan struct{}, 8), logHistorySlots: make(chan struct{}, 4),
		updateResultsWake: make(chan struct{}, 1),
		hostSecurity:      hostSecurityService,
		securityDrafts:    make(map[string]securityFirewallDraft),
		requestRestart:    config.RequestRestart,
		instanceID:        fmt.Sprintf("%d-%x", os.Getpid(), time.Now().UnixNano()),
	}
	application.externalTriggers = externaltrigger.New(db, externaltrigger.Options{SecretsDirectory: filepath.Join(stateRoot, "secrets")})
	application.externalLimit = externaltrigger.NewLimiter(externaltrigger.LimiterOptions{RequestsPerMinute: 60, Concurrent: 4})
	application.mysql, err = mysqlmanager.New(mysqlmanager.Options{DB: db, StateRoot: stateRoot, Audit: func(event mysqlmanager.AuditEvent) {
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
	application.assistant, err = assistant.New(db, assistant.Options{StateRoot: stateRoot})
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
	application.assistantRuntime = newAssistantRuntimeCoordinator(stateRoot, application.assistant, assistantSettings.MaxActiveConversations)
	application.assistantRuntime.SetApprovalAudit(func(actor assistant.Actor, conversationID, approvalID, result string) {
		var role userRole
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
	application.fileMoves = hostfiles.NewMoveEngine(files, application.fileOperations)
	application.fileOperationCtx, application.fileOperationStop = context.WithCancel(context.Background())
	if !validating {
		if err := application.initializeAdmin(stateRoot); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := application.applyCredentialOverride(config.AdminUsername, config.AdminPassword, config.AdminPasswordFile); err != nil {
			_ = db.Close()
			return nil, err
		}
		_, _ = cleanupExpiredAuditEvents(db, stateRoot, time.Now().UTC().AddDate(-1, 0, 0))
	}
	timeoutGrace := config.RunTimeoutGrace
	if timeoutGrace <= 0 {
		timeoutGrace = 30 * time.Second
	}
	application.runs = runmanager.New(db, application.files, stateRoot, timeoutGrace, config.ExecutorChains)
	if err := application.fileMoves.Recover(context.Background()); err != nil {
		application.runs.Close()
		_ = db.Close()
		return nil, fmt.Errorf("recover filesystem operations: %w", err)
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
		application.scheduler = scheduler.NewPaused(db, application.runs, application.loadVariables, config.SchedulerNow, config.SchedulerTick)
	} else {
		application.scheduler = scheduler.New(db, application.runs, application.loadVariables, config.SchedulerNow, config.SchedulerTick)
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
	application.websiteMonitor, err = websitemonitor.New(db, config.WebsiteMonitorOptions)
	if err != nil {
		application.applicationStatus.Close()
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, err
	}
	application.customDashboards, err = customdashboard.New(customdashboard.Options{DB: db, Paused: validating})
	if err != nil {
		application.websiteMonitor.Close()
		application.applicationStatus.Close()
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, fmt.Errorf("initialize custom dashboards: %w", err)
	}
	application.assistantTools = newAssistantToolExecutor(application)
	application.assistantRuntime.SetTurnSettled(application.assistantTools.releaseTurn)
	application.assistantBroker, err = toolbroker.New(stateRoot, application.assistantTools)
	if err != nil {
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
	opened = true
	return application, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) ValidationOperationID() string {
	return a.validationID
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

func (a *App) applyTrustedProxy(request *http.Request) *http.Request {
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
		return request
	}
	forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	for index := len(forwarded) - 1; index >= 0; index-- {
		client := net.ParseIP(strings.TrimSpace(forwarded[index]))
		if client == nil {
			continue
		}
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
	forwardedProto := strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")
	if strings.EqualFold(strings.TrimSpace(forwardedProto[len(forwardedProto)-1]), "https") {
		request = request.WithContext(context.WithValue(request.Context(), secureContextKey, true))
	}
	return request
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
	a.cancelAllAuthenticatedRequests()
	a.recordAudit("admin_reset", username, "succeeded", "local-cli")
	return password, nil
}

func (a *App) applyCredentialOverride(username, password, passwordFile string) error {
	if passwordFile != "" {
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
		if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len([]byte(password)) > 256 || password == username {
			return errors.New("管理员密码覆盖不符合长度规则")
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
	_, _ = a.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	dbErr := a.db.Close()
	lockErr := a.instanceLock.Close()
	if dbErr != nil {
		return dbErr
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
	schemaVersion, err := readSQLiteHeaderUserVersion(databasePath)
	if err != nil {
		return fmt.Errorf("inspect existing SQLite schema without modifying it: %w", err)
	}
	if !compatibleDatabaseSchema(schemaVersion) {
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
	info, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect SQLite database: %w", statErr)
	}
	existingDatabase := statErr == nil && info.Size() > 0
	if existingDatabase {
		schemaVersion, err := readSQLiteHeaderUserVersion(path)
		if err != nil {
			return nil, fmt.Errorf("inspect existing SQLite schema without modifying it: %w", err)
		}
		if !compatibleDatabaseSchema(schemaVersion) {
			return nil, fmt.Errorf("database schema version %d is incompatible with schema %d; use a new State Root", schemaVersion, currentSchemaVersion)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure SQLite: %w", err)
		}
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = db.Close()
		return nil, fmt.Errorf("SQLite integrity check failed: result=%q error=%v", integrity, err)
	}
	var schemaVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read SQLite schema version: %w", err)
	}
	migration, err := db.Begin()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("begin SQLite migration: %w", err)
	}
	defer func() { _ = migration.Rollback() }()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('administrator', 'maintainer', 'operator', 'viewer')),
			enabled INTEGER NOT NULL DEFAULT 1,
			auth_version INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			auth_version INTEGER NOT NULL,
			csrf_token TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at INTEGER NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			result TEXT NOT NULL,
			source_address TEXT NOT NULL,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_username TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS trash_entries (
			id TEXT PRIMARY KEY,
			original_path TEXT NOT NULL,
			original_path_key TEXT NOT NULL,
			stored_path TEXT NOT NULL UNIQUE,
			stored_path_key TEXT NOT NULL UNIQUE,
			deleted_at INTEGER NOT NULL,
			size INTEGER NOT NULL,
			is_directory INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			script_path TEXT NOT NULL,
			script_path_key TEXT NOT NULL,
			script_sha256 TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			template_arguments_json TEXT NOT NULL DEFAULT '[]',
			arguments_json TEXT NOT NULL,
			executor TEXT NOT NULL,
			source_type TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			started_at INTEGER,
			finished_at INTEGER,
			exit_code INTEGER,
			error TEXT NOT NULL DEFAULT '',
			timeout_seconds INTEGER NOT NULL DEFAULT 0,
			log_path TEXT NOT NULL
			, source_name TEXT NOT NULL DEFAULT ''
			, source_id TEXT NOT NULL DEFAULT ''
			, runtime_identity TEXT NOT NULL DEFAULT ''
			, log_expired INTEGER NOT NULL DEFAULT 0
			, log_incomplete INTEGER NOT NULL DEFAULT 0
			, log_truncated INTEGER NOT NULL DEFAULT 0
			, dropped_bytes INTEGER NOT NULL DEFAULT 0
			, script_kind TEXT NOT NULL DEFAULT 'host_file'
			, working_directory TEXT NOT NULL DEFAULT ''
			, working_directory_key TEXT NOT NULL DEFAULT ''
			, source_filename TEXT NOT NULL DEFAULT ''
			, source_expired INTEGER NOT NULL DEFAULT 0
			, source_audit_event_id INTEGER REFERENCES audit_events(id)
			, log_bytes INTEGER NOT NULL DEFAULT -1
			, initiated_by_user_id TEXT NOT NULL DEFAULT ''
			, initiated_by_username TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS variables (
			name TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			is_password INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS quick_run_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS quick_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			script_path TEXT NOT NULL,
			script_path_key TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			source_run_id TEXT REFERENCES runs(id),
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL,
			locked INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			group_name TEXT NOT NULL DEFAULT '',
			group_id TEXT REFERENCES schedule_groups(id) ON DELETE SET NULL,
			script_path TEXT NOT NULL,
			script_path_key TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			expression TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			enabled INTEGER NOT NULL,
			allow_overlap INTEGER NOT NULL,
			next_fire_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
			, deleted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_triggers (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL REFERENCES schedules(id),
			scheduled_for INTEGER NOT NULL,
			result TEXT NOT NULL,
			run_id TEXT NOT NULL,
			error TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_trigger_aggregates (
			schedule_id TEXT NOT NULL REFERENCES schedules(id),
			period TEXT NOT NULL,
			result TEXT NOT NULL,
			trigger_count INTEGER NOT NULL,
			PRIMARY KEY (schedule_id, period, result)
		)`,
		`CREATE TABLE IF NOT EXISTS file_operations (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('cross_filesystem_move')),
			source_path TEXT NOT NULL,
			source_path_key TEXT NOT NULL,
			destination_path TEXT NOT NULL,
			destination_path_key TEXT NOT NULL,
			temporary_path TEXT NOT NULL DEFAULT '',
			trash_path TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL,
			bytes_total INTEGER NOT NULL DEFAULT 0,
			bytes_completed INTEGER NOT NULL DEFAULT 0,
			verification_digest TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS file_quick_access_pins (
			path TEXT NOT NULL,
			path_key TEXT PRIMARY KEY,
			label TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS host_metric_minutes (
			bucket_at INTEGER PRIMARY KEY,
			sample_count INTEGER NOT NULL,
			average_json TEXT NOT NULL,
			maximum_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS application_pins (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('host', 'docker')),
			identity TEXT NOT NULL,
			name TEXT NOT NULL,
			technical TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (kind, identity)
		)`,
		`CREATE TABLE IF NOT EXISTS application_metric_minutes (
			application_id TEXT NOT NULL,
			bucket_at INTEGER NOT NULL,
			sample_count INTEGER NOT NULL,
			cpu_average REAL NOT NULL,
			cpu_maximum REAL NOT NULL,
			memory_average INTEGER NOT NULL,
			memory_maximum INTEGER NOT NULL,
			read_average REAL NOT NULL,
			read_maximum REAL NOT NULL,
			write_average REAL NOT NULL,
			write_maximum REAL NOT NULL,
			PRIMARY KEY (application_id, bucket_at)
		)`,
	} {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("初始化 SQLite: %w", err)
		}
	}
	for _, statement := range websitemonitor.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize Website Monitor SQLite schema: %w", err)
		}
	}
	for _, statement := range assistant.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize Assistant SQLite schema: %w", err)
		}
	}
	for _, statement := range externaltrigger.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize External Interface SQLite schema: %w", err)
		}
	}
	for _, statement := range mysqlmanager.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize MySQL management SQLite schema: %w", err)
		}
	}
	for _, statement := range customdashboard.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize custom dashboard SQLite schema: %w", err)
		}
	}
	if schemaVersion == 21 {
		if _, err := migration.Exec(`ALTER TABLE assistant_tool_calls ADD COLUMN body_offset INTEGER NOT NULL DEFAULT 0`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate Assistant tool-call positions: %w", err)
		}
		if _, err := migration.Exec(`UPDATE assistant_tool_calls SET body_offset = COALESCE(
			(SELECT LENGTH(body) FROM assistant_messages WHERE id = assistant_tool_calls.message_id), 0)`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("backfill Assistant tool-call positions: %w", err)
		}
	}
	if schemaVersion == 21 || schemaVersion == 22 {
		for _, statement := range []string{
			`ALTER TABLE assistant_tool_calls ADD COLUMN request_json TEXT NOT NULL DEFAULT '{}'`,
			`ALTER TABLE assistant_tool_calls ADD COLUMN response_json TEXT NOT NULL DEFAULT 'null'`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Assistant tool-call JSON payloads: %w", err)
			}
		}
	}
	if schemaVersion >= 21 && schemaVersion <= 23 {
		exists, err := sqliteColumnExists(migration, "assistant_models", "supports_images")
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("inspect Assistant model capability migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN supports_images INTEGER NOT NULL DEFAULT 0`); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Assistant model image capability: %w", err)
			}
		}
		for _, column := range []struct{ name, definition string }{
			{"capability_profile", `capability_profile TEXT NOT NULL DEFAULT 'general' CHECK (capability_profile IN ('general', 'diagnose-failed-run', 'investigate-website-incident', 'triage-host-pressure', 'review-script-safety', 'design-schedule'))`},
			{"profile_version", `profile_version TEXT NOT NULL DEFAULT ''`},
			{"thinking_level", `thinking_level TEXT NOT NULL DEFAULT 'medium' CHECK (thinking_level IN ('off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'))`},
			{"stats_user_messages", `stats_user_messages INTEGER NOT NULL DEFAULT 0`},
			{"stats_assistant_messages", `stats_assistant_messages INTEGER NOT NULL DEFAULT 0`},
			{"stats_tool_calls", `stats_tool_calls INTEGER NOT NULL DEFAULT 0`},
			{"stats_tool_results", `stats_tool_results INTEGER NOT NULL DEFAULT 0`},
			{"stats_total_messages", `stats_total_messages INTEGER NOT NULL DEFAULT 0`},
			{"stats_input_tokens", `stats_input_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_output_tokens", `stats_output_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_cache_read_tokens", `stats_cache_read_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_cache_write_tokens", `stats_cache_write_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_total_tokens", `stats_total_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_cost", `stats_cost REAL NOT NULL DEFAULT 0`},
			{"stats_context_tokens", `stats_context_tokens INTEGER NOT NULL DEFAULT 0`},
			{"stats_context_window", `stats_context_window INTEGER NOT NULL DEFAULT 0`},
			{"stats_context_percent", `stats_context_percent REAL`},
			{"stats_updated_at", `stats_updated_at INTEGER NOT NULL DEFAULT 0`},
		} {
			exists, err := sqliteColumnExists(migration, "assistant_conversations", column.name)
			if err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("inspect Assistant capability migration: %w", err)
			}
			if exists {
				continue
			}
			if _, err := migration.Exec(`ALTER TABLE assistant_conversations ADD COLUMN ` + column.definition); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Assistant capability profiles and telemetry: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 24 {
		for _, column := range []struct{ name, definition string }{
			{"owner_user_id", `owner_user_id TEXT NOT NULL DEFAULT ''`},
			{"is_shared", `is_shared INTEGER NOT NULL DEFAULT 0`},
		} {
			exists, err := sqliteColumnExists(migration, "assistant_models", column.name)
			if err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("inspect Assistant model visibility migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN ` + column.definition); err != nil {
					_ = db.Close()
					return nil, fmt.Errorf("migrate Assistant model visibility: %w", err)
				}
			}
		}
		if _, err := migration.Exec(`UPDATE assistant_models SET owner_user_id = COALESCE(
			NULLIF(owner_user_id, ''), NULLIF(updated_by_user_id, ''),
			(SELECT id FROM users WHERE role = 'administrator' LIMIT 1), 'legacy-owner')
			WHERE owner_user_id = ''`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("backfill Assistant model owners: %w", err)
		}
		if _, err := migration.Exec(`DROP INDEX IF EXISTS assistant_models_default_idx`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("replace Assistant model default index: %w", err)
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 25 {
		exists, err := sqliteColumnExists(migration, "assistant_models", "connection_ok")
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("inspect Assistant model connection migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN connection_ok INTEGER NOT NULL DEFAULT 0`); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Assistant model connection status: %w", err)
			}
		}
	}
	if schemaVersion == 28 {
		for _, statement := range []string{
			`ALTER TABLE file_quick_access_pins RENAME TO file_quick_access_pins_user_scoped`,
			`CREATE TABLE file_quick_access_pins (
				path TEXT NOT NULL,
				path_key TEXT PRIMARY KEY,
				label TEXT NOT NULL,
				sort_order INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			)`,
			`INSERT INTO file_quick_access_pins (path, path_key, label, sort_order, created_at)
				SELECT path, path_key, label, MIN(sort_order), MIN(created_at)
				FROM file_quick_access_pins_user_scoped GROUP BY path_key`,
			`DROP TABLE file_quick_access_pins_user_scoped`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate file Quick Access pins to instance scope: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 30 {
		exists, err := sqliteColumnExists(migration, "mysql_instances", "connection_state")
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("inspect MySQL connection state migration: %w", err)
		}
		if !exists {
			if _, err := migration.Exec(`ALTER TABLE mysql_instances ADD COLUMN connection_state TEXT NOT NULL DEFAULT 'untried' CHECK (connection_state IN ('untried', 'connected', 'failed'))`); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate MySQL connection state: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 34 {
		for _, column := range []struct{ name, definition string }{
			{"supports_reasoning", `supports_reasoning INTEGER NOT NULL DEFAULT 0`},
			{"default_thinking_level", `default_thinking_level TEXT NOT NULL DEFAULT 'medium' CHECK (default_thinking_level IN ('off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'))`},
		} {
			exists, err := sqliteColumnExists(migration, "assistant_models", column.name)
			if err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("inspect Assistant model reasoning migration: %w", err)
			}
			if !exists {
				if _, err := migration.Exec(`ALTER TABLE assistant_models ADD COLUMN ` + column.definition); err != nil {
					_ = db.Close()
					return nil, fmt.Errorf("migrate Assistant model reasoning defaults: %w", err)
				}
			}
		}
	}
	if schemaVersion >= 27 && schemaVersion <= 31 {
		for _, statement := range []string{
			`CREATE TABLE external_trigger_entries_schema32 (
				id TEXT PRIMARY KEY,
				key_id TEXT NOT NULL REFERENCES external_trigger_keys(id) ON DELETE CASCADE,
				name TEXT NOT NULL,
				label TEXT NOT NULL,
				action_type TEXT NOT NULL CHECK (action_type IN ('log', 'upload', 'quick_run', 'variable', 'website_monitor')),
				target TEXT NOT NULL DEFAULT '',
				config_json TEXT NOT NULL DEFAULT '{}',
				enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL,
				UNIQUE (key_id, name)
			)`,
			`INSERT INTO external_trigger_entries_schema32
				(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
				SELECT id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at
				FROM external_trigger_entries`,
			`DROP TABLE external_trigger_entries`,
			`ALTER TABLE external_trigger_entries_schema32 RENAME TO external_trigger_entries`,
			`CREATE INDEX external_trigger_entries_key_idx ON external_trigger_entries(key_id, created_at)`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate External Interface website monitoring action: %w", err)
			}
		}
	}
	if schemaVersion >= 20 && schemaVersion <= 33 {
		for _, statement := range []string{
			`ALTER TABLE custom_dashboard_cards RENAME TO custom_dashboard_cards_schema33`,
			`CREATE TABLE custom_dashboard_cards (
				id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
				name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','percentage','quota','key_value','website')),
				source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
				value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
				config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
				sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
				last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
			)`,
			`INSERT INTO custom_dashboard_cards
				(id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,snapshot_json,last_error,last_success_at,last_attempt_at,created_at,updated_at)
				SELECT id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,snapshot_json,last_error,last_success_at,last_attempt_at,created_at,updated_at
				FROM custom_dashboard_cards_schema33`,
			`DROP TABLE custom_dashboard_cards_schema33`,
			`CREATE INDEX custom_dashboard_cards_order_idx ON custom_dashboard_cards(dashboard_id, sort_order, created_at)`,
		} {
			if _, err := migration.Exec(statement); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate custom dashboard percentage cards: %w", err)
			}
		}
	}
	for _, statement := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS users_single_administrator_idx ON users(role) WHERE role = 'administrator'",
		"CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id)",
		"CREATE INDEX IF NOT EXISTS quick_run_groups_order_idx ON quick_run_groups(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS quick_runs_group_order_idx ON quick_runs(group_id, sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS quick_runs_script_path_idx ON quick_runs(script_path_key)",
		"CREATE INDEX IF NOT EXISTS schedules_group_idx ON schedules(group_name, created_at)",
		"CREATE INDEX IF NOT EXISTS schedule_groups_order_idx ON schedule_groups(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_group_order_idx ON schedules(group_id, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_script_path_idx ON schedules(script_path_key)",
		"CREATE INDEX IF NOT EXISTS runs_source_idx ON runs(source_type, source_id, created_at DESC)",
		"CREATE INDEX IF NOT EXISTS runs_source_audit_idx ON runs(source_audit_event_id)",
		"CREATE INDEX IF NOT EXISTS runs_created_idx ON runs(created_at DESC)",
		"CREATE INDEX IF NOT EXISTS runs_script_path_idx ON runs(script_path_key)",
		"CREATE INDEX IF NOT EXISTS runs_log_cleanup_idx ON runs(log_expired, created_at)",
		"CREATE INDEX IF NOT EXISTS audit_events_occurred_idx ON audit_events(occurred_at DESC)",
		"CREATE INDEX IF NOT EXISTS trash_entries_deleted_idx ON trash_entries(deleted_at DESC)",
		"CREATE INDEX IF NOT EXISTS trash_entries_original_path_idx ON trash_entries(original_path_key)",
		"CREATE INDEX IF NOT EXISTS file_operations_phase_idx ON file_operations(phase, created_at)",
		"CREATE INDEX IF NOT EXISTS file_quick_access_pins_order_idx ON file_quick_access_pins(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_due_idx ON schedules(next_fire_at) WHERE enabled = 1 AND deleted = 0",
		"CREATE INDEX IF NOT EXISTS schedule_triggers_schedule_time_idx ON schedule_triggers(schedule_id, scheduled_for DESC)",
		"CREATE INDEX IF NOT EXISTS schedule_triggers_unlinked_time_idx ON schedule_triggers(scheduled_for) WHERE run_id = ''",
		"CREATE INDEX IF NOT EXISTS application_pins_order_idx ON application_pins(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS application_metric_minutes_bucket_idx ON application_metric_minutes(bucket_at)",
		"CREATE UNIQUE INDEX IF NOT EXISTS assistant_models_owner_default_idx ON assistant_models(owner_user_id) WHERE is_default = 1",
		"CREATE INDEX IF NOT EXISTS assistant_models_visibility_idx ON assistant_models(owner_user_id, is_shared, is_default DESC, created_at, name)",
	} {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize SQLite indexes: %w", err)
		}
	}
	if _, err := migration.Exec(fmt.Sprintf("PRAGMA user_version=%d", currentSchemaVersion)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("record SQLite schema version: %w", err)
	}
	if _, err := migration.Exec(`UPDATE runs SET status = 'disconnected', finished_at = ?, error = CASE WHEN error = '' THEN 'service supervision was lost' ELSE error END WHERE status IN ('starting', 'running', 'stopping', 'timing_out')`, time.Now().UnixNano()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("recover disconnected runs: %w", err)
	}
	if err := migration.Commit(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("commit SQLite migration: %w", err)
	}
	// Persist page one before returning so future startup can reject an old
	// database by reading its header without opening it in writable mode.
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("checkpoint initialized SQLite schema: %w", err)
	}
	return db, nil
}

func compatibleDatabaseSchema(version int) bool {
	// Schema 20 is the clean host-filesystem baseline. Schema 21 adds the
	// assistant-owned tables, schema 22 adds persisted tool-call text positions,
	// schema 23 adds bounded request/response JSON, schema 24 adds capability
	// profiles plus bounded Pi session telemetry, schema 25 scopes LLM
	// configurations to owners with explicit sharing, schema 26 records the
	// latest observed LLM connection result, schema 27 adds bounded External
	// Interface keys, entries, and invocation records, schema 28 persists
	// per-user file Quick Access pins, and schema 29 merges those pins into one
	// instance-wide Quick Access list, schema 30 adds MySQL instances,
	// logical backups, plans, and recoverable operations, and schema 31 persists
	// MySQL connection state, and schema 32 adds read-only cross-instance website
	// monitoring interfaces and encrypted remote source metadata, and schema 33 adds
	// custom dashboards with independently public card collections, schema 34
	// adds dedicated percentage cards, and schema 35 adds model-level reasoning
	// capability and default thinking strength. Each supported predecessor has an explicit
	// transactional forward path.
	return version == currentSchemaVersion || currentSchemaVersion == 35 && version >= 20 && version <= 34
}

func sqliteColumnExists(transaction *sql.Tx, table, column string) (bool, error) {
	rows, err := transaction.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&position, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func readSQLiteHeaderUserVersion(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	header := make([]byte, 100)
	if _, err := io.ReadFull(file, header); err != nil {
		return 0, fmt.Errorf("read SQLite header: %w", err)
	}
	if !bytes.Equal(header[:16], []byte("SQLite format 3\x00")) {
		return 0, errors.New("file does not contain a SQLite 3 header")
	}
	return int(binary.BigEndian.Uint32(header[60:64])), nil
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
		"INSERT INTO users (id, username, password_hash, role, enabled, auth_version, created_at, updated_at) VALUES ('administrator', 'admin', ?, 'administrator', 1, 1, ?, ?)",
		hash, now, now,
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

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/app-v2.css", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/css; charset=utf-8", appCSS)
	})
	mux.HandleFunc("GET /assets/app.css", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/css; charset=utf-8", appCSS)
	})
	mux.HandleFunc("GET /assets/app-v2.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", appJS)
	})
	mux.HandleFunc("GET /assets/markdown-it.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", markdownItJS)
	})
	mux.HandleFunc("GET /assets/purify.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", domPurifyJS)
	})
	mux.HandleFunc("GET /assets/highlight.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", highlightJS)
	})
	mux.HandleFunc("GET /assets/highlight-powershell.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", highlightPowerShellJS)
	})
	mux.HandleFunc("GET /assets/highlight-dos.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", highlightDOSJS)
	})
	mux.HandleFunc("GET /favicon.ico", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "image/x-icon", scriptboardFaviconICO)
	})
	mux.Handle("GET /{$}", a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "/monitor", http.StatusSeeOther)
	})))
	mux.HandleFunc("GET /login", func(response http.ResponseWriter, request *http.Request) {
		if _, _, ok := a.loadSession(request); ok {
			http.Redirect(response, request, "/monitor", http.StatusSeeOther)
			return
		}
		renderLoginPage(response, request, http.StatusOK, "", "")
	})
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /settings/locale", a.setWebLocale)
	mux.HandleFunc("/trigger", a.externalTrigger)
	mux.Handle("POST /logout", a.requireSession(http.HandlerFunc(a.logout)))
	mux.Handle("GET /monitor", a.requireSession(http.HandlerFunc(a.overviewPage)))
	mux.Handle("GET /monitor/security", a.requireSession(http.HandlerFunc(a.securityPage)))
	mux.Handle("POST /monitor/security/components/{component}/install", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.installSecurityComponent)))
	mux.Handle("POST /monitor/security/fail2ban/unban", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.unbanSecurityIP)))
	mux.Handle("POST /monitor/security/firewall/draft/rules", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.addSecurityFirewallDraftRule)))
	mux.Handle("POST /monitor/security/firewall/draft/defaults", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.updateSecurityFirewallDraftDefaults)))
	mux.Handle("POST /monitor/security/firewall/draft/toggle", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.toggleSecurityFirewallDraftRule)))
	mux.Handle("POST /monitor/security/firewall/draft/delete", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.deleteSecurityFirewallDraftRule)))
	mux.Handle("POST /monitor/security/firewall/draft/discard", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.discardSecurityFirewallDraft)))
	mux.Handle("POST /monitor/security/firewall/draft/apply", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.applySecurityFirewallDraft)))
	mux.Handle("POST /monitor/security/firewall/enable", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.enableSecurityFirewall)))
	mux.Handle("GET /monitor/security/windows-firewall/rules/new", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.newWindowsFirewallRuleTask)))
	mux.Handle("POST /monitor/security/windows-firewall/rules", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.addWindowsFirewallRule)))
	mux.Handle("POST /monitor/security/windows-firewall/toggle", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.toggleWindowsFirewallRule)))
	mux.Handle("POST /monitor/security/windows-firewall/delete", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.deleteWindowsFirewallRule)))
	mux.Handle("GET /ai", a.requireSession(http.HandlerFunc(a.assistantPage)))
	mux.Handle("GET /ai/resources", a.requireSession(http.HandlerFunc(a.assistantResourceSearch)))
	mux.Handle("POST /ai/conversations", a.requireSession(http.HandlerFunc(a.createAssistantConversation)))
	mux.Handle("GET /ai/conversations/{id}", a.requireSession(http.HandlerFunc(a.assistantConversationPage)))
	mux.Handle("POST /ai/conversations/{id}/messages", a.requireSession(http.HandlerFunc(a.postAssistantMessage)))
	mux.Handle("POST /ai/conversations/{id}/abort", a.requireSession(http.HandlerFunc(a.abortAssistantTurn)))
	mux.Handle("GET /ai/conversations/{id}/events", a.requireSession(http.HandlerFunc(a.assistantConversationEvents)))
	mux.Handle("POST /ai/conversations/{id}/model", a.requireSession(http.HandlerFunc(a.setAssistantConversationModel)))
	mux.Handle("POST /ai/conversations/{id}/approval-mode", a.requireSession(http.HandlerFunc(a.setAssistantApprovalMode)))
	mux.Handle("POST /ai/conversations/{id}/profile", a.requireSession(http.HandlerFunc(a.setAssistantCapabilityProfile)))
	mux.Handle("POST /ai/conversations/{id}/thinking", a.requireSession(http.HandlerFunc(a.setAssistantThinkingLevel)))
	mux.Handle("POST /ai/conversations/{id}/compact", a.requireSession(http.HandlerFunc(a.compactAssistantConversation)))
	mux.Handle("POST /ai/conversations/{id}/approvals/{approval_id}", a.requireSession(http.HandlerFunc(a.resolveAssistantApproval)))
	mux.Handle("POST /ai/conversations/{id}/archive", a.requireSession(http.HandlerFunc(a.archiveAssistantConversation)))
	mux.Handle("POST /ai/conversations/{id}/restore", a.requireSession(http.HandlerFunc(a.restoreAssistantConversation)))
	mux.Handle("GET /monitor/data", a.requireSession(http.HandlerFunc(a.overviewData)))
	mux.Handle("GET /monitor/status", a.requireSession(http.HandlerFunc(a.shellStatus)))
	mux.Handle("GET /monitor/applications", a.requireSession(http.HandlerFunc(a.applicationsPage)))
	mux.Handle("GET /monitor/applications/data", a.requireSession(http.HandlerFunc(a.applicationsData)))
	mux.Handle("GET /monitor/applications/{id}/logs", a.requireSession(http.HandlerFunc(a.applicationLogPage)))
	mux.Handle("GET /monitor/applications/{id}/logs/history", a.requireSession(http.HandlerFunc(a.applicationLogHistory)))
	mux.Handle("GET /monitor/applications/{id}/logs/events", a.requireSession(http.HandlerFunc(a.applicationLogEvents)))
	mux.Handle("GET /monitor/applications/{id}/details", a.requireSession(http.HandlerFunc(a.applicationDetails)))
	mux.Handle("POST /monitor/applications/{id}/pin", a.requireSession(http.HandlerFunc(a.pinApplication)))
	mux.Handle("POST /monitor/applications/{id}/unpin", a.requireSession(http.HandlerFunc(a.unpinApplication)))
	mux.Handle("POST /monitor/applications/{id}/move", a.requireSession(http.HandlerFunc(a.movePinnedApplication)))
	mux.Handle("GET /monitor/websites", a.requireSession(http.HandlerFunc(a.websiteMonitorList)))
	mux.Handle("GET /config/dashboards", a.requireSession(http.HandlerFunc(a.customDashboardPage)))
	mux.Handle("POST /config/dashboards", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.createCustomDashboard)))
	mux.Handle("POST /config/dashboards/import", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.importCustomDashboard)))
	mux.Handle("POST /config/dashboards/{id}", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.updateCustomDashboard)))
	mux.Handle("GET /config/dashboards/{id}/export", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.exportCustomDashboard)))
	mux.Handle("POST /config/dashboards/{id}/delete", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.deleteCustomDashboard)))
	mux.Handle("POST /config/dashboards/{id}/cards", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.createCustomDashboardCard)))
	mux.Handle("POST /config/dashboard-cards/{id}/refresh", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.refreshCustomDashboardCard)))
	mux.Handle("POST /config/dashboard-cards/{id}/move", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.moveCustomDashboardCard)))
	mux.Handle("POST /config/dashboard-cards/{id}", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.updateCustomDashboardCard)))
	mux.Handle("POST /config/dashboard-cards/{id}/delete", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.deleteCustomDashboardCard)))
	mux.Handle("GET /monitor/dashboards", a.requireSession(http.HandlerFunc(a.legacyCustomDashboardPage)))
	mux.Handle("GET /monitor/dashboard/{id}", a.requireSession(http.HandlerFunc(a.customDashboardMonitorPage)))
	mux.Handle("POST /monitor/dashboards", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.createCustomDashboard)))
	mux.Handle("POST /monitor/dashboards/{id}", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.updateCustomDashboard)))
	mux.Handle("POST /monitor/dashboards/{id}/delete", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.deleteCustomDashboard)))
	mux.Handle("POST /monitor/dashboards/{id}/cards", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.createCustomDashboardCard)))
	mux.Handle("POST /monitor/dashboard-cards/{id}/refresh", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.refreshCustomDashboardCard)))
	mux.Handle("POST /monitor/dashboard-cards/{id}", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.updateCustomDashboardCard)))
	mux.Handle("POST /monitor/dashboard-cards/{id}/delete", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.deleteCustomDashboardCard)))
	mux.HandleFunc("GET /public/dashboard/{slug}", a.publicCustomDashboard)
	mux.Handle("GET /monitor/websites/data", a.requireSession(http.HandlerFunc(a.websiteMonitorData)))
	mux.Handle("POST /monitor/websites/remotes", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.createWebsiteMonitorRemoteSource)))
	mux.Handle("POST /monitor/websites/remotes/{id}/delete", a.requirePermission(permissionManageOperations, http.HandlerFunc(a.deleteWebsiteMonitorRemoteSource)))
	mux.Handle("GET /monitor/websites/new", a.requireSession(http.HandlerFunc(a.websiteMonitorCreateTask)))
	mux.Handle("POST /monitor/websites", a.requireSession(http.HandlerFunc(a.createWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/reorder", a.requireSession(http.HandlerFunc(a.reorderWebsiteMonitors)))
	mux.Handle("GET /monitor/websites/export", a.requireSession(http.HandlerFunc(a.websiteMonitorExportTask)))
	mux.Handle("POST /monitor/websites/export", a.requireSession(http.HandlerFunc(a.exportWebsiteMonitors)))
	mux.Handle("GET /monitor/websites/import", a.requireSession(http.HandlerFunc(a.websiteMonitorImportTask)))
	mux.Handle("POST /monitor/websites/import/preview", a.requireSession(http.HandlerFunc(a.previewWebsiteMonitorImport)))
	mux.Handle("POST /monitor/websites/import", a.requireSession(http.HandlerFunc(a.importWebsiteMonitors)))
	mux.Handle("GET /monitor/websites/nginx", a.requireSession(http.HandlerFunc(a.websiteMonitorNginxTask)))
	mux.Handle("POST /monitor/websites/nginx/scan", a.requireSession(http.HandlerFunc(a.scanWebsiteMonitorNginx)))
	mux.Handle("POST /monitor/websites/nginx/import", a.requireSession(http.HandlerFunc(a.importWebsiteMonitorNginx)))
	mux.Handle("GET /monitor/websites/{id}/edit", a.requireSession(http.HandlerFunc(a.websiteMonitorEditTask)))
	mux.Handle("POST /monitor/websites/{id}", a.requireSession(http.HandlerFunc(a.updateWebsiteMonitor)))
	mux.Handle("GET /monitor/websites/{id}", a.requireSession(http.HandlerFunc(a.websiteMonitorDetail)))
	mux.Handle("GET /monitor/websites/{id}/data", a.requireSession(http.HandlerFunc(a.websiteMonitorDetailData)))
	mux.Handle("POST /monitor/websites/{id}/check", a.requireSession(http.HandlerFunc(a.checkWebsiteMonitorNow)))
	mux.Handle("POST /monitor/websites/{id}/pause", a.requireSession(http.HandlerFunc(a.pauseWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/{id}/resume", a.requireSession(http.HandlerFunc(a.resumeWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/{id}/move", a.requireSession(http.HandlerFunc(a.moveWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteWebsiteMonitor)))
	mux.Handle("GET /settings/account", a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := request.Context().Value(sessionContextKey).(session)
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = accountTemplate.Execute(response, struct {
			Username, CSRFToken string
			CredentialOverride  bool
			CanRename           bool
			Locale              webLocale
			SettingsNavigation  settingsNavigationData
		}{
			Username: current.username, CSRFToken: current.csrfToken,
			CredentialOverride: a.credentialOverride && current.role == roleAdministrator,
			CanRename:          current.role == roleAdministrator, Locale: resolveWebLocale(request),
			SettingsNavigation: newSettingsNavigation(current, resolveWebLocale(request), "account"),
		})
	})))
	mux.Handle("POST /settings/account", a.requireSession(http.HandlerFunc(a.changePassword)))
	mux.Handle("GET /settings/account/username", a.requireSession(http.HandlerFunc(a.accountUsernameTask)))
	mux.Handle("POST /settings/account/username", a.requireSession(http.HandlerFunc(a.changeUsername)))
	mux.Handle("GET /settings/account/password", a.requireSession(http.HandlerFunc(a.accountPasswordTask)))
	mux.Handle("POST /settings/account/password", a.requireSession(http.HandlerFunc(a.changePassword)))
	mux.Handle("GET /settings/users", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.usersPage)))
	mux.Handle("GET /settings/users/create", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.createUserTask)))
	mux.Handle("POST /settings/users", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.createUser)))
	mux.Handle("GET /settings/users/{id}/edit", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.editUserTask)))
	mux.Handle("POST /settings/users/{id}/disable", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.disableUser)))
	mux.Handle("POST /settings/users/{id}/enable", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.enableUser)))
	mux.Handle("POST /settings/users/{id}/update", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.updateUser)))
	mux.Handle("POST /settings/users/{id}/reset-password", a.requirePermission(permissionManageUsers, http.HandlerFunc(a.resetUserPassword)))
	mux.Handle("GET /settings/display", a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := request.Context().Value(sessionContextKey).(session)
		locale := resolveWebLocale(request)
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = displaySettingsTemplate.Execute(response, struct {
			Locale             webLocale
			SettingsNavigation settingsNavigationData
		}{Locale: locale, SettingsNavigation: newSettingsNavigation(current, locale, "display")})
	})))
	mux.Handle("GET /settings/ai", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.assistantSettingsPage)))
	mux.Handle("POST /settings/ai/llms", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.saveAssistantModel)))
	mux.Handle("POST /settings/ai/llms/{id}/test", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.testAssistantModel)))
	mux.Handle("POST /settings/ai/llms/{id}/default", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.setDefaultAssistantModel)))
	mux.Handle("POST /settings/ai/llms/{id}/delete", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.deleteAssistantModel)))
	mux.Handle("POST /settings/ai/defaults", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.saveAssistantDefaults)))
	mux.Handle("POST /settings/ai/runtime/check", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.checkAssistantRuntime)))
	mux.Handle("POST /settings/ai/runtime/install", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.installAssistantRuntime)))
	mux.Handle("POST /settings/ai/runtime/offline", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.installAssistantRuntimeOffline)))
	mux.Handle("POST /settings/ai/runtime/rollback", a.requirePermission(permissionManageSystem, http.HandlerFunc(a.rollbackAssistantRuntime)))
	mux.Handle("GET /settings/updates", a.requireSession(http.HandlerFunc(a.updatesPage)))
	mux.Handle("GET /settings/updates/status", a.requireSession(http.HandlerFunc(a.updateStatus)))
	mux.Handle("POST /settings/updates/check", a.requireSession(http.HandlerFunc(a.checkUpdate)))
	mux.Handle("POST /settings/updates/prepare", a.requireSession(http.HandlerFunc(a.prepareUpdate)))
	mux.Handle("POST /settings/updates/apply", a.requireSession(http.HandlerFunc(a.applyUpdate)))
	mux.Handle("POST /settings/updates/restart", a.requireSession(http.HandlerFunc(a.restartService)))
	mux.Handle("GET /resources/databases", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.mysqlDatabasesPage)))
	mux.Handle("POST /resources/databases/instances", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.saveMySQLInstance)))
	mux.Handle("POST /resources/databases/settings/backup-root", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.setMySQLBackupRoot)))
	mux.Handle("POST /resources/databases/settings/tools", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.setMySQLTools)))
	mux.Handle("POST /resources/databases/instances/{id}/test", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.testMySQLInstance)))
	mux.Handle("POST /resources/databases/instances/{id}/delete", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.deleteMySQLInstance)))
	mux.Handle("POST /resources/databases/instances/{id}/databases", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.createMySQLDatabase)))
	mux.Handle("POST /resources/databases/instances/{id}/backup", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.startMySQLBackup)))
	mux.Handle("POST /resources/databases/instances/{id}/backup/batch", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.startMySQLBatchBackup)))
	mux.Handle("POST /resources/databases/instances/{id}/drop", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.startDropMySQLDatabase)))
	mux.Handle("POST /resources/databases/instances/{id}/imports", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.importMySQLBackup)))
	mux.Handle("POST /resources/databases/instances/{id}/imports/server", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.importMySQLServerBackup)))
	mux.Handle("POST /resources/databases/instances/{id}/plans", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.saveMySQLPlan)))
	mux.Handle("POST /resources/databases/plans/{plan_id}/delete", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.deleteMySQLPlan)))
	mux.Handle("GET /resources/databases/backups/{backup_id}/download", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.downloadMySQLBackup)))
	mux.Handle("POST /resources/databases/backups/{backup_id}/restore", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.startMySQLRestore)))
	mux.Handle("POST /resources/databases/backups/{backup_id}/delete", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.deleteMySQLBackup)))
	mux.Handle("GET /resources/databases/operations/{operation_id}/status", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.mysqlOperationStatus)))
	mux.Handle("GET /resources/databases/operations/{operation_id}/events", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.mysqlOperationEvents)))
	mux.Handle("POST /resources/databases/operations/{operation_id}/cancel", a.requirePermission(permissionManageDatabases, http.HandlerFunc(a.cancelMySQLOperation)))
	mux.Handle("GET /resources/files/new-directory", a.requireSession(http.HandlerFunc(a.newDirectoryTask)))
	mux.Handle("GET /resources/files/upload", a.requireSession(http.HandlerFunc(a.uploadTask)))
	mux.Handle("GET /resources/files/move", a.requireSession(http.HandlerFunc(a.moveFileTask)))
	mux.Handle("GET /resources/directories", a.requireSession(http.HandlerFunc(a.hostDirectories)))
	mux.Handle("GET /resources/files/log", a.requireSession(http.HandlerFunc(a.fileLogPage)))
	mux.Handle("GET /resources/files/log/history", a.requireSession(http.HandlerFunc(a.fileLogHistory)))
	mux.Handle("GET /resources/files/log/events", a.requireSession(http.HandlerFunc(a.fileLogEvents)))
	mux.Handle("GET /resources/files/run", a.requireSession(http.HandlerFunc(a.runFileTask)))
	mux.Handle("GET /resources/files/quick-run", a.requireSession(http.HandlerFunc(a.quickRunFromFileTask)))
	mux.Handle("GET /resources/files", a.requireSession(http.HandlerFunc(a.filesPage)))
	mux.Handle("GET /resources/files/validate", a.requireSession(http.HandlerFunc(a.validateFileQuickAccess)))
	mux.Handle("GET /resources/files/quick-access", a.requireSession(http.HandlerFunc(a.fileQuickAccessPins)))
	mux.Handle("POST /resources/files/quick-access", a.requireSession(http.HandlerFunc(a.updateFileQuickAccessPin)))
	mux.Handle("POST /resources/files/mkdir", a.requireSession(http.HandlerFunc(a.createDirectory)))
	mux.Handle("POST /resources/files/conflicts", a.requireSession(http.HandlerFunc(a.uploadConflicts)))
	mux.Handle("POST /resources/files/upload", a.requireSession(http.HandlerFunc(a.uploadFiles)))
	mux.Handle("GET /resources/files/download", a.requireSession(http.HandlerFunc(a.downloadFile)))
	mux.Handle("GET /resources/files/preview", a.requireSession(http.HandlerFunc(a.previewImage)))
	mux.Handle("GET /resources/files/view", a.requireSession(http.HandlerFunc(a.previewTextPage)))
	mux.Handle("POST /resources/files/delete", a.requireSession(http.HandlerFunc(a.deleteFile)))
	mux.Handle("POST /resources/files/move", a.requireSession(http.HandlerFunc(a.moveFile)))
	mux.Handle("POST /resources/files/toggle-executable", a.requireSession(http.HandlerFunc(a.toggleExecutable)))
	mux.Handle("GET /resources/files/operations/{id}", a.requireSession(http.HandlerFunc(a.fileOperationPage)))
	mux.Handle("GET /resources/files/operations/{id}/status", a.requireSession(http.HandlerFunc(a.fileOperationStatus)))
	mux.Handle("GET /resources/files/operations/{id}/events", a.requireSession(http.HandlerFunc(a.fileOperationEvents)))
	mux.Handle("POST /resources/files/operations/{id}/cancel", a.requireSession(http.HandlerFunc(a.cancelFileOperation)))
	mux.Handle("GET /resources/trash", a.requireSession(http.HandlerFunc(a.trashPage)))
	mux.Handle("POST /resources/trash/restore", a.requireSession(http.HandlerFunc(a.restoreTrash)))
	mux.Handle("POST /resources/trash/purge", a.requireSession(http.HandlerFunc(a.purgeTrash)))
	mux.Handle("GET /resources/files/edit", a.requireSession(http.HandlerFunc(a.editTextPage)))
	mux.Handle("POST /resources/files/edit", a.requireSession(http.HandlerFunc(a.saveText)))
	mux.Handle("POST /history/runs/start", a.requireSession(http.HandlerFunc(a.startRun)))
	mux.Handle("GET /history/runs", a.requireSession(http.HandlerFunc(a.runsPage)))
	mux.Handle("GET /history/runs/{id}/save-quick-run", a.requireSession(http.HandlerFunc(a.saveQuickRunTask)))
	mux.Handle("GET /history/runs/{id}/source", a.requireSession(http.HandlerFunc(a.runSource)))
	mux.Handle("GET /history/runs/{id}", a.requireSession(http.HandlerFunc(a.runDetails)))
	mux.Handle("POST /history/runs/{id}/stop", a.requireSession(http.HandlerFunc(a.stopRun)))
	mux.Handle("GET /history/runs/{id}/events", a.requireSession(http.HandlerFunc(a.runEvents)))
	mux.Handle("GET /resources/variables", a.requireSession(http.HandlerFunc(a.variablesPage)))
	mux.Handle("GET /resources/variables/new", a.requireSession(http.HandlerFunc(a.newVariableTask)))
	mux.Handle("GET /resources/variables/{name}/edit", a.requireSession(http.HandlerFunc(a.editVariableTask)))
	mux.Handle("POST /resources/variables", a.requireSession(http.HandlerFunc(a.createVariable)))
	mux.Handle("POST /resources/variables/{name}/update", a.requireSession(http.HandlerFunc(a.updateVariable)))
	mux.Handle("POST /resources/variables/{name}/delete", a.requireSession(http.HandlerFunc(a.deleteVariable)))
	mux.Handle("POST /history/runs/{id}/quick-run", a.requireSession(http.HandlerFunc(a.saveQuickRun)))
	mux.Handle("GET /config/quick-runs", a.requireSession(http.HandlerFunc(a.quickRunsPage)))
	mux.Handle("POST /config/quick-runs", a.requireSession(http.HandlerFunc(a.createQuickRunFromFile)))
	mux.Handle("GET /config/quick-runs/one-time/new", a.requireSession(http.HandlerFunc(a.oneTimeRunTask)))
	mux.Handle("POST /config/quick-runs/one-time", a.requireSession(http.HandlerFunc(a.startOneTimeRun)))
	mux.Handle("GET /config/quick-runs/from-source/new", a.requireSession(http.HandlerFunc(a.quickCreateTask)))
	mux.Handle("POST /config/quick-runs/from-source", a.requireSession(http.HandlerFunc(a.createQuickRunFromSource)))
	mux.Handle("GET /config/quick-runs/groups/new", a.requireSession(http.HandlerFunc(a.newQuickRunGroupTask)))
	mux.Handle("POST /config/quick-runs/groups", a.requireSession(http.HandlerFunc(a.createQuickRunGroup)))
	mux.Handle("GET /config/quick-runs/groups/{id}/edit", a.requireSession(http.HandlerFunc(a.editQuickRunGroupTask)))
	mux.Handle("POST /config/quick-runs/groups/{id}/update", a.requireSession(http.HandlerFunc(a.updateQuickRunGroup)))
	mux.Handle("POST /config/quick-runs/groups/{id}/move", a.requireSession(http.HandlerFunc(a.moveQuickRunGroup)))
	mux.Handle("POST /config/quick-runs/groups/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteQuickRunGroup)))
	mux.Handle("GET /config/quick-runs/{id}/move-group", a.requireSession(http.HandlerFunc(a.moveQuickRunToGroupTask)))
	mux.Handle("POST /config/quick-runs/{id}/move-group", a.requireSession(http.HandlerFunc(a.moveQuickRunToGroup)))
	mux.Handle("GET /config/quick-runs/{id}/edit", a.requireSession(http.HandlerFunc(a.editQuickRunTask)))
	mux.Handle("POST /config/quick-runs/{id}/update", a.requireSession(http.HandlerFunc(a.updateQuickRun)))
	mux.Handle("GET /config/quick-runs/{id}/copy", a.requireSession(http.HandlerFunc(a.copyQuickRunTask)))
	mux.Handle("POST /config/quick-runs/{id}/copy", a.requireSession(http.HandlerFunc(a.copyQuickRun)))
	mux.Handle("POST /config/quick-runs/{id}/lock", a.requireSession(http.HandlerFunc(a.setQuickRunLocked)))
	mux.Handle("POST /config/quick-runs/{id}/start", a.requireSession(http.HandlerFunc(a.startQuickRun)))
	mux.Handle("POST /config/quick-runs/{id}/move", a.requireSession(http.HandlerFunc(a.moveQuickRun)))
	mux.Handle("POST /config/quick-runs/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteQuickRun)))
	mux.Handle("GET /config/schedules", a.requireSession(http.HandlerFunc(a.schedulesPage)))
	mux.Handle("GET /config/schedules/groups/new", a.requireSession(http.HandlerFunc(a.newScheduleGroupTask)))
	mux.Handle("POST /config/schedules/groups", a.requireSession(http.HandlerFunc(a.createScheduleGroup)))
	mux.Handle("GET /config/schedules/groups/{id}/edit", a.requireSession(http.HandlerFunc(a.editScheduleGroupTask)))
	mux.Handle("POST /config/schedules/groups/{id}/update", a.requireSession(http.HandlerFunc(a.updateScheduleGroup)))
	mux.Handle("POST /config/schedules/groups/{id}/move", a.requireSession(http.HandlerFunc(a.moveScheduleGroup)))
	mux.Handle("POST /config/schedules/groups/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteScheduleGroup)))
	mux.Handle("GET /config/schedules/new", a.requireSession(http.HandlerFunc(a.newScheduleTask)))
	mux.Handle("GET /config/schedules/{id}/edit", a.requireSession(http.HandlerFunc(a.editScheduleTask)))
	mux.Handle("POST /config/schedules/preview", a.requireSession(http.HandlerFunc(a.previewScheduleCron)))
	mux.Handle("POST /config/schedules/{id}/preview", a.requireSession(http.HandlerFunc(a.previewScheduleCron)))
	mux.Handle("POST /config/schedules", a.requireSession(http.HandlerFunc(a.createSchedule)))
	mux.Handle("POST /config/schedules/{id}/update", a.requireSession(http.HandlerFunc(a.updateSchedule)))
	mux.Handle("POST /config/schedules/{id}/toggle", a.requireSession(http.HandlerFunc(a.toggleSchedule)))
	mux.Handle("POST /config/schedules/{id}/run", a.requireSession(http.HandlerFunc(a.runScheduleNow)))
	mux.Handle("POST /config/schedules/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteSchedule)))
	mux.Handle("GET /config/external-interfaces", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.externalInterfacesPage)))
	mux.Handle("GET /config/external-interfaces/keys/new", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.newExternalKeyTask)))
	mux.Handle("POST /config/external-interfaces/keys", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.createExternalKey)))
	mux.Handle("GET /config/external-interfaces/keys/{id}/edit", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.editExternalKeyTask)))
	mux.Handle("GET /config/external-interfaces/keys/{id}/copy", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.copyExternalKeyTask)))
	mux.Handle("GET /config/external-interfaces/keys/{id}/rotate", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.rotateExternalKeyTask)))
	mux.Handle("POST /config/external-interfaces/keys/{id}", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.updateExternalKey)))
	mux.Handle("POST /config/external-interfaces/keys/{id}/toggle", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.toggleExternalKey)))
	mux.Handle("POST /config/external-interfaces/keys/{id}/rotate", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.rotateExternalKey)))
	mux.Handle("POST /config/external-interfaces/keys/{id}/delete", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.deleteExternalKey)))
	mux.Handle("GET /config/external-interfaces/keys/{id}/entries/new", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.newExternalEntryTask)))
	mux.Handle("POST /config/external-interfaces/keys/{id}/entries", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.createExternalEntry)))
	mux.Handle("GET /config/external-interfaces/entries/{id}", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.externalEntryDetail)))
	mux.Handle("GET /config/external-interfaces/entries/{id}/edit", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.editExternalEntryTask)))
	mux.Handle("POST /config/external-interfaces/entries/{id}", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.updateExternalEntry)))
	mux.Handle("POST /config/external-interfaces/entries/{id}/toggle", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.toggleExternalEntry)))
	mux.Handle("POST /config/external-interfaces/entries/{id}/delete", a.requirePermission(permissionManageExecution, http.HandlerFunc(a.deleteExternalEntry)))
	mux.Handle("GET /history/audit", a.requireSession(http.HandlerFunc(a.auditPage)))
	mux.Handle("GET /history/audit.csv", a.requireSession(http.HandlerFunc(a.auditDownload)))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request = a.applyTrustedProxy(request)
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if isSecureRequest(request) {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if request.Body != nil && request.Method != http.MethodGet && request.Method != http.MethodHead &&
			request.URL.Path != "/resources/files/upload" && request.URL.Path != "/settings/ai/runtime/offline" && request.URL.Path != "/trigger" {
			if request.ContentLength > maxFormRequestBytes {
				http.Error(response, "request body is too large", http.StatusRequestEntityTooLarge)
				return
			}
			resetReadDeadline := setRequestReadDeadline(response, boundedFormReadTimeout)
			defer resetReadDeadline()
			request.Body = http.MaxBytesReader(response, request.Body, maxFormRequestBytes)
		}
		if a.validation.Load() && (request.Method != http.MethodGet || request.URL.Path == "/trigger") {
			response.Header().Set("Retry-After", "2")
			if request.URL.Path == "/trigger" {
				response.Header().Set("Content-Type", "application/json; charset=utf-8")
				writeExternalTriggerError(response, http.StatusServiceUnavailable, "service_unavailable")
			} else {
				http.Error(response, webText(resolveWebLocale(request), "updates.validation_write_blocked"), http.StatusServiceUnavailable)
			}
			return
		}
		pageResponse := &pageResponseWriter{ResponseWriter: response}
		mux.ServeHTTP(pageResponse, request)
		pageResponse.finish(a, request)
	})
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
	w.buffering = (strings.HasPrefix(contentType, "text/html") && (status < 300 || status >= 400)) ||
		(status >= 400 && strings.HasPrefix(contentType, "text/plain"))
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
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
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

var appCSS = mustWebAsset("web/assets/app.css")

var appJS = mustWebAsset("web/assets/app.js")

var markdownItJS = mustWebAsset("web/assets/markdown-it.min.js")

var domPurifyJS = mustWebAsset("web/assets/purify.min.js")

var highlightJS = mustWebAsset("web/assets/highlight.min.js")

var highlightPowerShellJS = mustWebAsset("web/assets/highlight-powershell.min.js")

var highlightDOSJS = mustWebAsset("web/assets/highlight-dos.min.js")

var scriptboardFaviconICO = mustWebAsset("web/assets/favicon.ico")

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
		WHERE (? = '' OR action LIKE ? OR target LIKE ? OR result LIKE ? OR source_address LIKE ? OR actor_username LIKE ? OR actor_role LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)`,
		filters.Query, like, like, like, like, like, like,
		filters.HasFromDate, filters.FromUnix,
		filters.HasToDate, filters.ToExclusiveUnix).Scan(&total); err != nil {
		http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	rows, err := a.db.Query(`SELECT occurred_at, action, target, result, source_address, actor_username, actor_role FROM audit_events
		WHERE (? = '' OR action LIKE ? OR target LIKE ? OR result LIKE ? OR source_address LIKE ? OR actor_username LIKE ? OR actor_role LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)
		ORDER BY occurred_at DESC LIMIT ? OFFSET ?`,
		filters.Query, like, like, like, like, like, like,
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
		if err := rows.Scan(&occurredAt, &event.Action, &event.Target, &event.Result, &event.Source, &event.Actor, &event.ActorRole); err != nil {
			http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
			return
		}
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
	rows, err := a.db.Query("SELECT occurred_at, action, target, result, source_address, actor_user_id, actor_username, actor_role FROM audit_events ORDER BY occurred_at")
	if err != nil {
		http.Error(response, "无法导出审计事件", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	response.Header().Set("Content-Type", "text/csv; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="scriptboard-audit.csv"`)
	writer := csv.NewWriter(response)
	_ = writer.Write([]string{"occurred_at", "action", "target", "result", "source_address", "actor_user_id", "actor_username", "actor_role"})
	for rows.Next() {
		var occurred int64
		var action, target, result, source, actorUserID, actorUsername, actorRole string
		if rows.Scan(&occurred, &action, &target, &result, &source, &actorUserID, &actorUsername, &actorRole) != nil {
			return
		}
		record := []string{time.Unix(occurred, 0).UTC().Format(time.RFC3339), action, target, result, source, actorUserID, actorUsername, actorRole}
		for index := range record {
			record[index] = spreadsheetSafeCSVCell(record[index])
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
			CanExecute: roleAllows(current.role, permissionExecute), CanManage: roleAllows(current.role, permissionManageExecution),
			CanReadFiles: roleAllows(current.role, permissionReadFiles),
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
		CSRFToken: current.csrfToken, Locale: locale, CanManage: roleAllows(current.role, permissionManageExecution),
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
	scriptPath, err := a.files.CanonicalExisting(request.FormValue("script"))
	if err != nil {
		return scheduler.CreateRequest{}, fmt.Errorf("计划脚本无效: %w", err)
	}
	info, err := a.files.Info(scriptPath)
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
	ArgumentsTemplate string
	TimeoutSeconds    int
	GroupID           string
	Valid             bool
	Locked            bool
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

func (a *App) createQuickRun(values quickRunCreateRequest) (string, error) {
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
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, source_run_id, sort_order, created_at, group_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, values.Name, values.ScriptPath, hostfiles.ComparisonKey(values.ScriptPath), values.ArgumentsTemplate, values.TimeoutSeconds,
		values.SourceRunID, sortOrder, now, values.GroupID, now,
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
	id, err := a.createQuickRun(quickRunCreateRequest{
		Name: name, ScriptPath: source.ScriptPath, ArgumentsTemplate: source.ArgumentsTemplate,
		TimeoutSeconds: source.TimeoutSeconds, SourceRunID: &source.ID, GroupID: groupID,
	})
	if err != nil {
		http.Error(response, "无法保存快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "create_quick_run", id, "succeeded")
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
	scriptPath, err := a.files.CanonicalExisting(request.FormValue("script"))
	if err != nil {
		writeHostFileError(response, "脚本不存在或不可运行", err)
		return
	}
	info, err := a.files.Info(scriptPath)
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
	id, err := a.createQuickRun(quickRunCreateRequest{
		Name: name, ScriptPath: scriptPath, ArgumentsTemplate: argumentsTemplate,
		TimeoutSeconds: timeoutSeconds, SourceRunID: nil, GroupID: groupID,
	})
	if err != nil {
		http.Error(response, "无法保存快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "create_quick_run", id, "succeeded")
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
			CanExecute: roleAllows(current.role, permissionExecute), CanManage: roleAllows(current.role, permissionManageExecution),
			CanReadFiles: roleAllows(current.role, permissionReadFiles),
		})
		return
	}
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "无法读取快捷执行分组", http.StatusInternalServerError)
		return
	}
	rows, err := a.db.Query(`SELECT id, name, script_path, arguments_template, timeout_seconds, group_id, locked
		FROM quick_runs ORDER BY sort_order, created_at`)
	if err != nil {
		http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
		return
	}
	var quickRuns []quickRunView
	for rows.Next() {
		var quick quickRunView
		var groupID sql.NullString
		if err := rows.Scan(&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds, &groupID, &quick.Locked); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
			return
		}
		if info, infoErr := a.files.Info(quick.ScriptPath); infoErr == nil && info.Mode().IsRegular() {
			quick.Valid = true
		}
		if groupID.Valid {
			quick.GroupID = groupID.String
		}
		quickRuns = append(quickRuns, quick)
	}
	_ = rows.Close()
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
		CanExecute: roleAllows(current.role, permissionExecute), CanManage: roleAllows(current.role, permissionManageExecution),
		CanReadFiles: roleAllows(current.role, permissionReadFiles),
	}); err != nil {
		http.Error(response, "Unable to render Quick Runs: "+err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) startQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	var quick quickRunView
	if err := a.db.QueryRow("SELECT id, name, script_path, arguments_template, timeout_seconds FROM quick_runs WHERE id = ?", request.PathValue("id")).Scan(
		&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds,
	); err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
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
		ScriptPath: quick.ScriptPath, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds,
		SourceType: "admin/quick-run", SourceName: quick.Name, SourceID: quick.ID, Variables: variables,
		InitiatorUserID: current.userID, InitiatorUsername: current.username,
	})
	if err != nil {
		http.Error(response, "无法启动快捷执行："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "start_quick_run", quick.ID, "accepted")
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
		_, err = transaction.Exec("UPDATE quick_runs SET arguments_template = replace(arguments_template, ?, ?)", oldReference, newReference)
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
	if current.role == roleOperator {
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
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath:        request.FormValue("script"),
		ArgumentsTemplate: request.FormValue("arguments"),
		SourceType:        "admin/manual",
		SourceName:        "manual",
		TimeoutSeconds:    timeoutSeconds,
		Variables:         variables,
		InitiatorUserID:   current.userID,
		InitiatorUsername: current.username,
	})
	if err != nil {
		a.recordAuditForRequest(request, "start_run", request.FormValue("script"), "rejected")
		http.Error(response, "无法启动脚本："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "start_run", id, "accepted")
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
	canManageExecution := roleAllows(current.role, permissionManageExecution)
	canStop := current.role == roleAdministrator || current.role == roleMaintainer ||
		current.role == roleOperator && run.InitiatorUserID == current.userID
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
		destination, err = a.files.Destination(request.FormValue("working_directory"), request.FormValue("name"))
		if err != nil {
			writeHostFileError(response, "移动目标无效", err)
			return
		}
	}
	source, err = a.files.CanonicalExisting(source)
	if err != nil {
		writeHostFileError(response, "移动源无效", err)
		return
	}
	destination, err = a.files.CanonicalDestination(destination)
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
		destination, err = a.files.Destination(destinationParent, newName)
		if err != nil {
			writeHostFileError(response, "移动目标无效", err)
			return
		}
		destinationName = newName
	}
	_, targetErr := a.files.Info(destination)
	targetExists := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		http.Error(response, "无法检查移动目标："+targetErr.Error(), http.StatusBadRequest)
		return
	}
	if targetExists && action != conflictActionOverwrite {
		suggested, err := a.files.AvailableName(destinationParent, destinationName)
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
	sameFilesystem, err := a.files.SameFilesystem(source, destination)
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
	if err := a.files.AcquireLease(leaseID, source, destination); err != nil {
		http.Error(response, "移动路径正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	leaseOwned := true
	defer func() {
		if leaseOwned {
			a.files.ReleaseLease(leaseID)
		}
	}()
	_, latestTargetErr := a.files.Info(destination)
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
		moved, err := a.files.MoveToTrash(destination, displacedID)
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
			_ = a.files.RestoreFromTrash(moved.StoredPath, moved.OriginalPath)
			http.Error(response, "无法记录被覆盖的条目", http.StatusInternalServerError)
			return
		}
	}
	if !sameFilesystem {
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
					_ = a.restoreTrackedTrash(displacedID, *displaced)
				}
			}
			result := "succeeded"
			if moveErr != nil {
				result = "failed"
			}
			a.recordAuditWithActor("cross_filesystem_move", source+" -> "+destination, result, sourceAddress, current.userID, current.username, current.role)
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
	if err := a.files.Move(source, destination); err != nil {
		if displaced != nil {
			if restoreErr := a.restoreTrackedTrash(displacedID, *displaced); restoreErr != nil {
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
		rollbackErr := a.files.Move(destination, source)
		if rollbackErr == nil && displaced != nil {
			rollbackErr = a.restoreTrackedTrash(displacedID, *displaced)
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
	path, err := a.files.CanonicalExisting(request.FormValue("path"))
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
	if _, err := a.files.ToggleOwnerExecute(path); err != nil {
		writeHostFileError(response, "无法切换所有者执行权限", err)
		return
	}
	a.recordAuditForRequest(request, "toggle_owner_execute", path, "succeeded")
	parent, _ := hostPathParent(path)
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) editTextPage(response http.ResponseWriter, request *http.Request) {
	relative, err := a.files.CanonicalExisting(request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "无法编辑文件", err)
		return
	}
	document, err := a.files.ReadText(relative, 1<<20)
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
	relative, err := a.files.CanonicalExisting(request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "无法预览文件", err)
		return
	}
	document, err := a.files.ReadText(relative, 1<<20)
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
	relative, err := a.files.CanonicalExisting(request.URL.Query().Get("path"))
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
	trashed, err := a.files.SaveText(relative, request.FormValue("digest"), request.FormValue("content"), id, 1<<20)
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
		_ = a.files.RollbackTextSave(relative, trashed.StoredPath)
		http.Error(response, "无法记录文件旧版本", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "edit_text", relative, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
	parent, _ := hostPathParent(relative)
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) downloadFile(response http.ResponseWriter, request *http.Request) {
	relative := request.URL.Query().Get("path")
	file, info, err := a.files.OpenRegular(relative)
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
	relative := request.URL.Query().Get("path")
	extension := strings.ToLower(hostfiles.Extension(relative))
	contentTypes := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}
	contentType, allowed := contentTypes[extension]
	if !allowed {
		http.Error(response, "该格式只能下载，不能内嵌预览", http.StatusUnsupportedMediaType)
		return
	}
	file, info, err := a.files.OpenRegular(relative)
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
	path, err := a.files.CanonicalExisting(request.FormValue("path"))
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
	trashed, err := a.files.MoveToTrash(path, id)
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
		_ = a.files.RestoreFromTrash(trashed.StoredPath, trashed.OriginalPath)
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
		if restoreErr := a.restoreTrackedTrash(id, trashed); restoreErr != nil {
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
		renamedDestination, err := a.files.Destination(parent, newName)
		if err != nil {
			writeHostFileError(response, "恢复目标无效", err)
			return
		}
		destination = renamedDestination
	}
	_, targetErr := a.files.Info(destination)
	targetExists := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		http.Error(response, "无法检查恢复目标："+targetErr.Error(), http.StatusConflict)
		return
	}
	if targetExists && action != conflictActionOverwrite {
		suggested, err := a.files.AvailableName(parent, originalName)
		if action == conflictActionRename {
			_, requestedName := parentAndName(destination)
			suggested, err = a.files.AvailableName(parent, requestedName)
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
	if err := a.commitTrashRestore(id, stored, destination, action == conflictActionOverwrite && targetExists); err != nil {
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
	if err := a.files.PurgeTrash(stored); err != nil {
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
			if _, listErr := a.files.List(relative); listErr != nil {
				_ = part.Close()
				writeHostFileError(response, "上传目录无效", listErr)
				return
			}
			directoryChecked = true
		}
		if nameErr := hostfiles.ValidateName(filename); nameErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: nameErr.Error()})
			continue
		}
		targetPath, destinationErr := a.files.Destination(relative, filename)
		if destinationErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: destinationErr.Error()})
			continue
		}
		if !validConflictAction(conflictAction) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: webText(locale, "upload_results.invalid_conflict_action")})
			continue
		}
		targetInfo, targetErr := a.files.Info(targetPath)
		targetExists := targetErr == nil
		if targetErr != nil && !os.IsNotExist(targetErr) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "无法检查同名文件：" + targetErr.Error()})
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
				uploadName, err = a.files.AvailableName(relative, filename)
				if err != nil {
					_ = part.Close()
					results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "无法生成可用名称：" + err.Error()})
					continue
				}
				targetPath, err = a.files.Destination(relative, uploadName)
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
		release, leaseErr := a.acquireFileMutationLease(targetPath)
		if leaseErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: leaseErr.Error()})
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
		trashed, uploadErr := a.files.Upload(relative, uploadName, part, 1<<30, replace, storedID)
		if uploadErr != nil {
			release()
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: uploadErr.Error()})
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
				_ = a.files.RollbackTextSave(targetPath, trashed.StoredPath)
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
		relative, err = a.files.CanonicalDirectory(relative)
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
			CanWrite: roleAllows(current.role, permissionWriteFiles), CanMutateCurrent: roleAllows(current.role, permissionWriteFiles) && relative != "", CanExecute: roleAllows(current.role, permissionExecute),
			CanManageExecution: roleAllows(current.role, permissionManageExecution),
		})
		return
	}
	entries, err := a.files.List(relative)
	if err != nil {
		writeHostFileError(response, "无法读取主机目录", err)
		return
	}
	listing := prepareFileListing(entries, relative, query, sortField, direction, showHidden)
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
		previewableText := false
		if entry.Kind == hostfiles.Regular && (category == fileCategoryOther || category == fileCategoryText || category == fileCategoryScript) {
			likelyText, detectErr := a.files.IsLikelyText(path, 64<<10)
			previewableText = detectErr == nil && likelyText
		}
		view := fileView{
			Entry: entry, Path: path, IconClass: fileCategoryIcon(category),
			NameParts: splitFileNameMatches(entry.Name, query), CategoryLabel: fileCategoryLabel(locale, category),
			IsHidden: entry.Hidden, CanMutate: a.files.CanMutate(path),
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
		Pagination: pagination, CanToggleExecutable: runtime.GOOS == "linux" && roleAllows(current.role, permissionWriteFiles), ParentURL: parentURL,
		CanWrite: roleAllows(current.role, permissionWriteFiles), CanMutateCurrent: roleAllows(current.role, permissionWriteFiles) && relative != "", CanExecute: roleAllows(current.role, permissionExecute),
		CanManageExecution: roleAllows(current.role, permissionManageExecution),
		Breadcrumbs:        breadcrumbs, Locale: locale, ShowHidden: showHidden,
	})
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
		if info, err := a.files.Info(path); err == nil && info.IsDir() {
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
	target, err := a.files.Destination(directory, request.FormValue("name"))
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
	if err := a.files.CreateDirectory(directory, hostfiles.Base(target)); err != nil {
		writeHostFileError(response, "无法创建目录", err)
		return
	}
	a.recordAuditForRequest(request, "create_directory", target, "succeeded")
	http.Redirect(response, request, filesURL(directory), http.StatusSeeOther)
}

func (a *App) accountUsernameTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if current.role != roleAdministrator {
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
	if current.role != roleAdministrator {
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
	if !utf8.ValidString(newPassword) || utf8.RuneCountInString(newPassword) < 12 || len([]byte(newPassword)) > 256 || newPassword == username {
		http.Error(response, "密码必须至少包含 12 个 Unicode 字符、不超过 256 个 UTF-8 字节，且不能与用户名相同", http.StatusBadRequest)
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
	if current.role == roleAdministrator {
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
	var role userRole
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
		renderLoginFailure(response, request, http.StatusUnauthorized, request.FormValue("username"), "用户名或密码错误")
		return
	}
	a.clearLoginFailures(loginKeys...)

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
		"INSERT INTO sessions (token_hash, user_id, auth_version, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		hashToken(token), userID, authVersion, sessionCSRF, now.Unix(), now.Unix(), now.Add(7*24*time.Hour).Unix(),
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
	a.recordAuditWithActor("login", username, "succeeded", request.RemoteAddr, userID, username, role)
	completeLogin(response, request, "/monitor")
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
	userID      string
	username    string
	role        userRole
	authVersion int64
	tokenHash   string
	csrfToken   string
}

func (a *App) loadSession(request *http.Request) (session, string, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return session{}, "", false
	}
	var current session
	var lastSeen, expiresAt int64
	current.tokenHash = hashToken(cookie.Value)
	err = a.db.QueryRow(`
		SELECT sessions.csrf_token, sessions.last_seen_at, sessions.expires_at,
			users.id, users.username, users.role, users.auth_version
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.auth_version = users.auth_version AND users.enabled = 1`, current.tokenHash,
	).Scan(&current.csrfToken, &lastSeen, &expiresAt, &current.userID, &current.username, &current.role, &current.authVersion)
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
		if !roleAllows(current.role, permissionForRequest(request)) {
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		cookie, _ := request.Cookie(sessionCookieName)
		now := time.Now().UTC()
		if !a.validation.Load() {
			_, _ = a.db.Exec("UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", now.Unix(), hashToken(cookie.Value))
		}
		authenticatedContext := context.WithValue(request.Context(), sessionContextKey, current)
		authenticatedContext, cancel := context.WithCancel(authenticatedContext)
		requestID := a.registerAuthenticatedRequest(current.userID, cancel)
		defer a.unregisterAuthenticatedRequest(current.userID, requestID)
		defer cancel()
		next.ServeHTTP(response, request.WithContext(authenticatedContext))
	})
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
	a.recordAuditWithActor(action, target, result, request.RemoteAddr, current.userID, current.username, current.role)
}

func (a *App) recordAuditWithActor(action, target, result, source, actorUserID, actorUsername string, actorRole userRole) {
	_, _ = a.db.Exec(
		`INSERT INTO audit_events
			(occurred_at, action, target, result, source_address, actor_user_id, actor_username, actor_role)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Unix(), action, target, result, source, actorUserID, actorUsername, actorRole,
	)
}

type loginPageData struct {
	CSRFToken string
	Username  string
	Error     string
	Locale    webLocale
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
