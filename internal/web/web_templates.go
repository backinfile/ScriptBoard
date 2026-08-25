package web

import "scriptboard/internal/identity"

type settingsNavigationData struct {
	Locale          webLocale
	Current         string
	CanManageUsers  bool
	CanManageSystem bool
}

func newSettingsNavigation(current session, locale webLocale, active string) settingsNavigationData {
	return settingsNavigationData{
		Locale: locale, Current: active,
		CanManageUsers:  identity.Allows(current.role, identity.PermissionManageUsers),
		CanManageSystem: identity.Allows(current.role, identity.PermissionManageSystem),
	}
}

// Inline-free template declarations keep page markup in ui/templates while
// preserving the single-binary deployment provided by go:embed.
var (
	accountTemplate                   = mustWebTemplate("account")
	assistantTemplate                 = mustWebTemplate("assistant")
	assistantSettingsTemplate         = mustWebTemplate("assistant-settings")
	applicationErrorTemplate          = mustWebTemplate("application-error")
	applicationShellTemplate          = mustWebTemplate("application-shell")
	applicationsTemplate              = mustWebTemplate("applications")
	containersTemplate                = mustWebTemplate("containers")
	containerOperationConfirmTemplate = mustWebTemplate("container-operation-confirm")
	customDashboardTemplate           = mustWebTemplate("custom-dashboard")
	customTabsTemplate                = mustWebTemplate("custom-tabs")
	customTabFrameTemplate            = mustWebTemplate("custom-tab-frame")
	auditTemplate                     = mustWebTemplate("audit")
	deleteImpactTemplate              = mustWebTemplate("delete-impact")
	displaySettingsTemplate           = mustWebTemplate("display-settings")
	instanceNameSettingsTemplate      = mustWebTemplate("instance-name-settings")
	fleetNodeSettingsTemplate         = mustWebTemplate("fleet-node-settings")
	fleetNodeFormTemplate             = mustWebTemplate("fleet-node-form")
	fleetTokenFormTemplate            = mustWebTemplate("fleet-token-form")
	kubernetesTemplate                = mustWebTemplate("kubernetes")
	kubernetesConnectionTemplate      = mustWebTemplate("kubernetes-connection")
	kubernetesLogsTemplate            = mustWebTemplate("kubernetes-logs")
	externalInterfacesTemplate        = mustWebTemplate("external-interfaces")
	externalInterfaceFormTemplate     = mustWebTemplate("external-interface-form")
	fileConflictTemplate              = mustWebTemplate("file-conflict")
	fileOperationTemplate             = mustWebTemplate("file-operation")
	filesTemplate                     = mustWebTemplate("files")
	mfaTemplate                       = mustWebTemplate("mfa")
	loginTemplate                     = mustWebTemplate("login")
	notificationsTemplate             = mustWebTemplate("notifications")
	stateBackupsTemplate              = mustWebTemplate("state-backups")
	mysqlDatabasesTemplate            = mustWebTemplate("mysql-databases")
	redisDatabasesTemplate            = mustWebTemplate("redis-databases")
	liveLogTemplate                   = mustWebTemplate("live-log")
	overlapTemplate                   = mustWebTemplate("overlap")
	overviewTemplate                  = mustWebTemplate("overview")
	quickRunsTemplate                 = mustWebTemplate("quick-runs")
	runTemplate                       = mustWebTemplate("run")
	runsTemplate                      = mustWebTemplate("runs")
	schedulesTemplate                 = mustWebTemplate("schedules")
	serviceLogsTemplate               = mustWebTemplate("service-logs")
	securityTemplate                  = mustWebTemplate("security")
	taskPageTemplate                  = mustWebTemplate("task-page")
	textEditorTemplate                = mustWebTemplate("text-editor")
	textPreviewTemplate               = mustWebTemplate("text-preview")
	trashTemplate                     = mustWebTemplate("trash")
	uploadResultsTemplate             = mustWebTemplate("upload-results")
	uploadInboxTemplate               = mustWebTemplate("upload-inbox")
	variablesTemplate                 = mustWebTemplate("variables")
	updatesTemplate                   = mustWebTemplate("updates")
	usersTemplate                     = mustWebTemplate("users")
	websiteMonitorDetailTemplate      = mustWebTemplate("website-monitor-detail")
	websiteMonitorFormTemplate        = mustWebTemplate("website-monitor-form")
	websiteMonitorListTemplate        = mustWebTemplate("website-monitor-list")
	websiteMonitorNginxTemplate       = mustWebTemplate("website-monitor-nginx")
	websiteTransferTemplate           = mustWebTemplate("website-monitor-transfer")
)
