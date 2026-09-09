package web

import "scriptboard/internal/identity"

type settingsNavigationData struct {
	Locale             webLocale
	Current            string
	CanManageUsers     bool
	CanManageSystem    bool
	CanManageExecution bool
}

func newSettingsNavigation(current session, locale webLocale, active string) settingsNavigationData {
	// Group settings by intent while retaining each destination's permission boundary.
	switch active {
	case "name", "memory":
		active = "instance"
	case "nodes", "embedding", "notifications", "mcp":
		active = "integrations"
	case "state-backups", "updates":
		active = "maintenance"
	case "display":
		active = "account"
	}
	return settingsNavigationData{
		Locale: locale, Current: active,
		CanManageExecution: identity.Allows(current.role, identity.PermissionManageExecution),
		CanManageUsers:     identity.Allows(current.role, identity.PermissionManageUsers),
		CanManageSystem:    identity.Allows(current.role, identity.PermissionManageSystem),
	}
}

// Inline-free template declarations keep page markup in ui/templates while
// preserving the single-binary deployment provided by go:embed.
var (
	registriesTemplate                = mustWebTemplate("registries")
	settingsHubTemplate               = mustWebTemplate("settings-hub")
	accountTemplate                   = mustWebTemplate("account")
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
	documentsTemplate                 = mustWebTemplate("documents")
	memorySettingsTemplate            = mustWebTemplate("memory-settings")
	embeddingSettingsTemplate         = mustWebTemplate("embedding-settings")
	instanceNameSettingsTemplate      = mustWebTemplate("instance-name-settings")
	fleetNodeSettingsTemplate         = mustWebTemplate("fleet-node-settings")
	fleetNodeFormTemplate             = mustWebTemplate("fleet-node-form")
	fleetTokenFormTemplate            = mustWebTemplate("fleet-token-form")
	kubernetesTemplate                = mustWebTemplate("kubernetes")
	kubernetesConnectionTemplate      = mustWebTemplate("kubernetes-connection")
	kubernetesLogsTemplate            = mustWebTemplate("kubernetes-logs")
	externalInterfacesTemplate        = mustWebTemplate("external-interfaces")
	externalInterfaceFormTemplate     = mustWebTemplate("external-interface-form")
	externalApprovalDetailTemplate    = mustWebTemplate("external-approval-detail")
	fileConflictTemplate              = mustWebTemplate("file-conflict")
	fileOperationTemplate             = mustWebTemplate("file-operation")
	filesTemplate                     = mustWebTemplate("files")
	quickAccessTemplate               = mustWebTemplate("quick-access")
	mfaTemplate                       = mustWebTemplate("mfa")
	mcpSettingsTemplate               = mustWebTemplate("mcp-settings")
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
	variablesTemplate                 = mustWebTemplate("variables")
	updatesTemplate                   = mustWebTemplate("updates")
	usersTemplate                     = mustWebTemplate("users")
	websiteMonitorDetailTemplate      = mustWebTemplate("website-monitor-detail")
	websiteMonitorFormTemplate        = mustWebTemplate("website-monitor-form")
	websiteMonitorListTemplate        = mustWebTemplate("website-monitor-list")
	websiteMonitorNginxTemplate       = mustWebTemplate("website-monitor-nginx")
	websiteTransferTemplate           = mustWebTemplate("website-monitor-transfer")
)
