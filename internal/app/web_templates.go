package app

type settingsNavigationData struct {
	Locale          webLocale
	Current         string
	CanManageUsers  bool
	CanManageSystem bool
}

func newSettingsNavigation(current session, locale webLocale, active string) settingsNavigationData {
	return settingsNavigationData{
		Locale: locale, Current: active,
		CanManageUsers:  roleAllows(current.role, permissionManageUsers),
		CanManageSystem: roleAllows(current.role, permissionManageSystem),
	}
}

// Inline-free template declarations keep page markup in web/templates while
// preserving the single-binary deployment provided by go:embed.
var (
	accountTemplate               = mustWebTemplate("account")
	assistantTemplate             = mustWebTemplate("assistant")
	assistantSettingsTemplate     = mustWebTemplate("assistant-settings")
	applicationErrorTemplate      = mustWebTemplate("application-error")
	applicationShellTemplate      = mustWebTemplate("application-shell")
	applicationsTemplate          = mustWebTemplate("applications")
	customDashboardTemplate       = mustWebTemplate("custom-dashboard")
	auditTemplate                 = mustWebTemplate("audit")
	deleteImpactTemplate          = mustWebTemplate("delete-impact")
	displaySettingsTemplate       = mustWebTemplate("display-settings")
	instanceNameSettingsTemplate  = mustWebTemplate("instance-name-settings")
	externalInterfacesTemplate    = mustWebTemplate("external-interfaces")
	externalInterfaceFormTemplate = mustWebTemplate("external-interface-form")
	fileConflictTemplate          = mustWebTemplate("file-conflict")
	fileOperationTemplate         = mustWebTemplate("file-operation")
	filesTemplate                 = mustWebTemplate("files")
	loginTemplate                 = mustWebTemplate("login")
	mysqlDatabasesTemplate        = mustWebTemplate("mysql-databases")
	liveLogTemplate               = mustWebTemplate("live-log")
	overlapTemplate               = mustWebTemplate("overlap")
	overviewTemplate              = mustWebTemplate("overview")
	quickRunsTemplate             = mustWebTemplate("quick-runs")
	runTemplate                   = mustWebTemplate("run")
	runsTemplate                  = mustWebTemplate("runs")
	schedulesTemplate             = mustWebTemplate("schedules")
	securityTemplate              = mustWebTemplate("security")
	taskPageTemplate              = mustWebTemplate("task-page")
	textEditorTemplate            = mustWebTemplate("text-editor")
	textPreviewTemplate           = mustWebTemplate("text-preview")
	trashTemplate                 = mustWebTemplate("trash")
	uploadResultsTemplate         = mustWebTemplate("upload-results")
	variablesTemplate             = mustWebTemplate("variables")
	updatesTemplate               = mustWebTemplate("updates")
	usersTemplate                 = mustWebTemplate("users")
	websiteMonitorDetailTemplate  = mustWebTemplate("website-monitor-detail")
	websiteMonitorFormTemplate    = mustWebTemplate("website-monitor-form")
	websiteMonitorListTemplate    = mustWebTemplate("website-monitor-list")
	websiteMonitorNginxTemplate   = mustWebTemplate("website-monitor-nginx")
	websiteTransferTemplate       = mustWebTemplate("website-monitor-transfer")
)
