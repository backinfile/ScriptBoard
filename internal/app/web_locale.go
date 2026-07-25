package app

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type webLocale string

const (
	localeSimplifiedChinese webLocale = "zh-CN"
	localeEnglishUS         webLocale = "en-US"
	localeCookieName                  = "scriptboard_locale"
)

type localizedMessage struct {
	zh string
	en string
}

var webMessages = map[string]localizedMessage{
	"login.title":                    {"登录", "Sign in"},
	"login.failure":                  {"登录失败", "Sign-in failed"},
	"login.username":                 {"用户名", "Username"},
	"login.password":                 {"密码", "Password"},
	"login.submit":                   {"登录", "Sign in"},
	"login.pending":                  {"正在登录…", "Signing in…"},
	"login.network_error":            {"网络连接失败，请稍后重试。", "Network connection failed. Try again."},
	"login.description":              {"一台主机，一份清晰的运行记录。", "One host. One clear operational record."},
	"login.language":                 {"切换为 English", "切换为中文"},
	"shell.skip":                     {"跳至主要内容", "Skip to main content"},
	"shell.navigation":               {"主导航", "Primary navigation"},
	"shell.open_navigation":          {"打开导航", "Open navigation"},
	"shell.close_navigation":         {"关闭导航", "Close navigation"},
	"shell.local":                    {"本机", "Local"},
	"shell.remote":                   {"远程", "Remote"},
	"shell.active_runs":              {"个活动 Run", "active Runs"},
	"shell.settings":                 {"设置", "Settings"},
	"shell.change_language":          {"切换语言", "Change language"},
	"shell.sign_out":                 {"退出", "Sign out"},
	"status.current":                 {"数据正常", "Data current"},
	"status.attention":               {"需要关注", "Attention needed"},
	"status.stale":                   {"数据已过期", "Data stale"},
	"nav.monitor":                    {"监控", "Monitor"},
	"nav.overview":                   {"概览", "Overview"},
	"nav.runs":                       {"运行", "Runs"},
	"nav.resources":                  {"资源", "Resources"},
	"nav.files":                      {"文件", "Managed Files"},
	"nav.variables":                  {"变量", "Variables"},
	"nav.configuration":              {"配置", "Configuration"},
	"nav.quick_runs":                 {"快捷执行", "Quick Runs"},
	"nav.schedules":                  {"计划", "Schedules"},
	"nav.history":                    {"历史", "History"},
	"nav.audit":                      {"审计", "Audit"},
	"common.actions":                 {"操作", "Actions"},
	"common.apply":                   {"应用", "Apply"},
	"common.arguments":               {"参数", "Arguments"},
	"common.back":                    {"返回", "Back"},
	"common.cancel":                  {"取消", "Cancel"},
	"common.clear":                   {"清除", "Clear"},
	"common.close":                   {"关闭", "Close"},
	"common.create":                  {"创建", "Create"},
	"common.delete":                  {"删除", "Delete"},
	"common.details":                 {"说明", "Details"},
	"common.disable":                 {"停用", "Disable"},
	"common.disabled":                {"已停用", "Disabled"},
	"common.download":                {"下载", "Download"},
	"common.edit":                    {"编辑", "Edit"},
	"common.enable":                  {"启用", "Enable"},
	"common.enabled":                 {"已启用", "Enabled"},
	"common.modified":                {"修改时间", "Modified"},
	"common.more_actions":            {"更多操作", "More actions"},
	"common.name":                    {"名称", "Name"},
	"common.next":                    {"下一页", "Next"},
	"common.not_available":           {"—", "—"},
	"common.open":                    {"打开", "Open"},
	"common.path":                    {"路径", "Path"},
	"common.preview":                 {"预览", "Preview"},
	"common.previous":                {"上一页", "Previous"},
	"common.records":                 {"条记录", "records"},
	"common.result":                  {"结果", "Result"},
	"common.run":                     {"运行", "Run"},
	"common.save":                    {"保存", "Save"},
	"common.script":                  {"脚本", "Script"},
	"common.search":                  {"搜索", "Search"},
	"common.size":                    {"大小", "Size"},
	"common.source":                  {"来源", "Source"},
	"common.status":                  {"状态", "Status"},
	"common.target":                  {"目标", "Target"},
	"common.time":                    {"时间", "Time"},
	"common.timeout":                 {"超时", "Timeout"},
	"common.value":                   {"值", "Value"},
	"overview.title":                 {"主机概览", "Host overview"},
	"overview.eyebrow":               {"观测台账", "Observation ledger"},
	"overview.host_uptime":           {"主机已运行", "Host uptime"},
	"overview.run_script":            {"运行脚本", "Run script"},
	"overview.observation_verdict":   {"当前判断", "Observation verdict"},
	"overview.current_description":   {"最新采集没有发现需要处理的异常。", "The latest collection contains no condition that needs attention."},
	"overview.attention_description": {"采集结果包含需要查看的异常。", "The latest collection contains conditions that need review."},
	"overview.stale_description":     {"采集结果已超过有效时间，请确认服务状态。", "The latest collection is outside its freshness window. Check the service."},
	"overview.collected_at":          {"采集于", "Collected"},
	"overview.issues":                {"观测异常", "Observation issues"},
	"overview.measurements":          {"资源测量", "Resource measurements"},
	"overview.history_range":         {"历史范围", "History range"},
	"overview.collecting":            {"等待采集", "Awaiting collection"},
	"overview.collecting_rate":       {"需要两个样本计算速率", "Two samples are required to calculate a rate"},
	"overview.logical_cores":         {"个逻辑核心", "logical cores"},
	"overview.logical_cores_label":   {"逻辑核心", "Logical cores"},
	"overview.cpu_history":           {"CPU 使用率历史", "CPU usage history"},
	"overview.memory":                {"内存", "Memory"},
	"overview.memory_history":        {"内存使用率历史", "Memory usage history"},
	"overview.storage":               {"存储", "Storage"},
	"overview.storage_history":       {"关键卷使用率历史", "Critical volume usage history"},
	"overview.network":               {"网络", "Network"},
	"overview.network_history":       {"网络接收速率历史", "Network receive-rate history"},
	"overview.available":             {"可用", "Available"},
	"overview.receive":               {"接收", "Receive"},
	"overview.send":                  {"发送", "Send"},
	"overview.active_runs":           {"活动 Run", "Active Runs"},
	"overview.view_all":              {"查看全部", "View all"},
	"overview.no_active_runs":        {"当前没有活动 Run。", "There are no active Runs."},
	"overview.service":               {"ScriptBoard 服务", "ScriptBoard service"},
	"overview.service_uptime":        {"服务已运行", "Service uptime"},
	"overview.resident_memory":       {"常驻内存", "Resident memory"},
	"overview.threads":               {"线程", "Threads"},
	"overview.storage_io":            {"存储与 I/O", "Storage and I/O"},
	"overview.filesystems":           {"个文件系统", "filesystems"},
	"overview.devices":               {"个设备", "devices"},
	"overview.filesystem":            {"文件系统", "Filesystem"},
	"overview.role":                  {"用途", "Role"},
	"overview.used":                  {"已用", "Used"},
	"overview.capacity":              {"容量", "Capacity"},
	"overview.network_interfaces":    {"网络接口", "Network interfaces"},
	"overview.interfaces":            {"个接口", "interfaces"},
	"overview.interface":             {"接口", "Interface"},
	"overview.address":               {"地址", "Address"},
	"overview.errors_drops":          {"错误 / 丢包", "Errors / drops"},
	"overview.host_facts":            {"主机信息", "Host facts"},
	"overview.hostname":              {"主机名", "Hostname"},
	"overview.system":                {"系统", "System"},
	"overview.architecture":          {"架构", "Architecture"},
	"overview.physical_memory":       {"物理内存", "Physical memory"},
	"overview.booted_at":             {"启动时间", "Booted"},
	"files.title":                    {"文件", "Managed files"},
	"files.eyebrow":                  {"资源 / 受管目录", "Resources / Managed root"},
	"files.description":              {"在受管目录中查找、整理并运行脚本。", "Find, organize, and run scripts in the managed root."},
	"files.upload":                   {"上传文件", "Upload files"},
	"files.breadcrumb":               {"文件路径", "File path"},
	"files.root":                     {"根目录", "Root"},
	"files.tools":                    {"目录工具", "Directory tools"},
	"files.search":                   {"搜索当前目录", "Search this directory"},
	"files.search_placeholder":       {"搜索文件名…", "Search filenames…"},
	"files.sort":                     {"排序字段", "Sort field"},
	"files.natural_order":            {"自然顺序", "Natural order"},
	"files.direction":                {"排序方向", "Sort direction"},
	"files.ascending":                {"升序", "Ascending"},
	"files.descending":               {"降序", "Descending"},
	"files.new_directory":            {"新建目录", "New directory"},
	"files.trash":                    {"回收站", "Trash"},
	"files.executable":               {"执行权限", "Executable permission"},
	"files.move_to_trash":            {"移入回收站", "Move to trash"},
	"files.empty":                    {"当前目录为空。", "This directory is empty."},
	"files.pagination":               {"文件分页", "File pagination"},
	"runs.title":                     {"运行记录", "Runs"},
	"runs.eyebrow":                   {"监控 / 执行历史", "Monitor / Execution history"},
	"runs.description":               {"追踪每次脚本执行的状态、来源与输出。", "Track the state, source, and output of every script execution."},
	"runs.executor":                  {"执行器", "Executor"},
	"runs.empty":                     {"还没有运行记录。", "There are no Runs yet."},
	"runs.pagination":                {"运行记录分页", "Run pagination"},
	"quick_runs.title":               {"快捷执行", "Quick Runs"},
	"quick_runs.eyebrow":             {"配置 / 常用任务", "Configuration / Frequent tasks"},
	"quick_runs.description":         {"保存验证过的脚本参数，以便再次启动。", "Keep proven script configurations ready to run again."},
	"quick_runs.invalid_path":        {"脚本路径已失效", "The script path is no longer valid"},
	"quick_runs.invalid":             {"路径失效", "Invalid path"},
	"quick_runs.move_up":             {"上移", "Move up"},
	"quick_runs.move_down":           {"下移", "Move down"},
	"quick_runs.delete_confirm":      {"确认删除这个快捷执行？此操作无法撤销。", "Delete this Quick Run? This cannot be undone."},
	"quick_runs.empty":               {"还没有快捷执行。先运行一个脚本，再从运行详情保存配置。", "There are no Quick Runs yet. Run a script, then save its configuration from the Run detail."},
	"quick_runs.browse_scripts":      {"浏览脚本", "Browse scripts"},
	"quick_runs.pagination":          {"快捷执行分页", "Quick Run pagination"},
	"schedules.title":                {"计划", "Schedules"},
	"schedules.eyebrow":              {"配置 / 自动执行", "Configuration / Automation"},
	"schedules.description":          {"用 Cron 表达式安排脚本，并控制超时与重叠策略。", "Schedule scripts with cron expressions, timeouts, and overlap policy."},
	"schedules.create":               {"创建计划", "Create schedule"},
	"schedules.timing":               {"时间规则", "Timing"},
	"schedules.next_run":             {"下次运行", "Next run"},
	"schedules.overlap_allowed":      {"允许重叠", "Overlap allowed"},
	"schedules.overlap_blocked":      {"禁止重叠", "Overlap blocked"},
	"schedules.view_run":             {"查看 Run", "View Run"},
	"schedules.run_now":              {"立即执行", "Run now"},
	"schedules.delete_confirm":       {"确认删除这个计划？此操作无法撤销。", "Delete this schedule? This cannot be undone."},
	"schedules.empty":                {"还没有计划。", "There are no schedules yet."},
	"schedules.pagination":           {"计划分页", "Schedule pagination"},
	"variables.title":                {"变量", "Variables"},
	"variables.eyebrow":              {"资源 / 运行参数", "Resources / Runtime values"},
	"variables.description":          {"集中维护脚本参数可引用的运行变量。", "Maintain runtime values that script arguments can reference."},
	"variables.create":               {"创建变量", "Create variable"},
	"variables.plaintext_notice":     {"密码类型只会在页面中默认隐藏；变量仍以明文存储，并可能出现在运行参数与历史中。", "Password type only hides the value by default in this page. Variables are still stored as plain text and may appear in Run arguments and history."},
	"variables.password_type":        {"密码类型", "Password type"},
	"variables.hidden":               {"变量值已隐藏", "Variable value hidden"},
	"variables.show_value":           {"显示变量值", "Show variable value"},
	"variables.hide_value":           {"隐藏变量值", "Hide variable value"},
	"variables.copy_value":           {"复制变量值", "Copy variable value"},
	"variables.copied":               {"已复制", "Copied"},
	"variables.copy_failed":          {"复制失败，请手动选择", "Copy failed. Select the value manually."},
	"variables.delete_confirm":       {"确认删除这个变量？引用它的配置可能失效，且此操作无法撤销。", "Delete this variable? Configurations that reference it may break, and this cannot be undone."},
	"variables.empty":                {"还没有变量。", "There are no variables yet."},
	"variables.pagination":           {"变量分页", "Variable pagination"},
	"audit.title":                    {"审计", "Audit"},
	"audit.eyebrow":                  {"历史 / 操作记录", "History / Operation record"},
	"audit.description":              {"查看管理操作、目标、结果与来源地址。", "Review administrative actions, targets, outcomes, and source addresses."},
	"audit.download_csv":             {"下载完整 CSV", "Download full CSV"},
	"audit.search":                   {"搜索审计记录", "Search audit records"},
	"audit.search_placeholder":       {"操作、目标、结果或来源…", "Action, target, result, or source…"},
	"audit.operation":                {"操作", "Operation"},
	"audit.empty":                    {"没有符合条件的审计记录。", "No audit records match this view."},
	"audit.pagination":               {"审计分页", "Audit pagination"},
	"trash.title":                    {"回收站", "Trash"},
	"trash.eyebrow":                  {"资源 / 可恢复删除", "Resources / Recoverable deletion"},
	"trash.description":              {"恢复已删除条目，或明确执行永久清理。", "Restore deleted entries or explicitly purge them forever."},
	"trash.back_to_files":            {"返回文件", "Back to files"},
	"trash.deleted_at":               {"删除时间", "Deleted"},
	"trash.restore":                  {"恢复", "Restore"},
	"trash.purge":                    {"永久清理", "Purge permanently"},
	"trash.purge_confirm":            {"确认永久清理这个条目？此操作无法撤销。", "Permanently purge this entry? This cannot be undone."},
	"trash.empty":                    {"回收站为空。", "Trash is empty."},
	"trash.pagination":               {"回收站分页", "Trash pagination"},
	"settings.title":                 {"设置", "Settings"},
	"settings.eyebrow":               {"系统 / 管理", "System / Administration"},
	"settings.description":           {"管理账户与本地版本保护。", "Manage the account and local version protection."},
	"settings.sections":              {"设置分区", "Settings sections"},
	"settings.account":               {"账户", "Account"},
	"account.title":                  {"账户", "Account"},
	"account.description":            {"管理当前唯一管理员账户的登录凭据。", "Manage credentials for the single administrator account."},
	"account.current_username":       {"当前用户名", "Current username"},
	"account.credential_source":      {"凭据来源", "Credential source"},
	"account.source_override":        {"启动配置覆盖", "Startup configuration override"},
	"account.source_database":        {"ScriptBoard 数据库", "ScriptBoard database"},
	"account.override_notice":        {"当前实例使用启动凭据覆盖。网页修改只在下次重启前有效；要永久保留，请移除启动配置中的覆盖值。", "This instance uses startup credential overrides. Web changes last only until the next restart; remove the override values to keep changes permanently."},
	"account.change":                 {"修改账户凭据", "Change credentials"},
	"account.current_password":       {"当前密码", "Current password"},
	"account.new_password":           {"新密码", "New password"},
	"account.confirm_password":       {"确认新密码", "Confirm new password"},
	"account.save":                   {"保存账户凭据", "Save credentials"},
	"protection.title":               {"版本保护", "Version protection"},
	"protection.description":         {"用本地 Git 检查点记录受管文件变更，并按文件恢复历史版本。", "Record managed-file changes with local Git checkpoints and restore previous file versions."},
	"protection.enabled":             {"保护已启用", "Protection enabled"},
	"protection.disabled":            {"保护未启用", "Protection disabled"},
	"protection.repository_size":     {"仓库占用", "Repository size"},
	"protection.storage_warning":     {"已超过容量上限的 80%", "Above 80% of the storage limit"},
	"protection.last_commit":         {"最近提交", "Latest commit"},
	"protection.start_title":         {"为文件变更建立安全网", "Create a safety net for file changes"},
	"protection.start_description":   {"启用后，受支持的文件操作会创建本地检查点。历史只保存在当前设备，不会自动推送到远程。", "When enabled, supported file operations create local checkpoints. History stays on this device and is never pushed automatically."},
	"protection.benefit_trace":       {"追踪编辑、上传与删除", "Trace edits, uploads, and deletions"},
	"protection.benefit_restore":     {"按单个文件查看与恢复历史", "Review and restore history per file"},
	"protection.benefit_local":       {"所有历史只保存在本机", "Keep all history local"},
	"protection.enable":              {"启用版本保护", "Enable version protection"},
	"protection.existing_repository": {"已有 Git 仓库？", "Existing Git repository?"},
	"protection.adopt_description":   {"仅在受管根目录已经是干净且可信的 Git 仓库时使用。", "Use this only when the managed root is already a clean, trusted Git repository."},
	"protection.adopt_confirm":       {"确认接管已有 Git 仓库？请确保仓库干净且来源可信。", "Adopt the existing Git repository? Make sure it is clean and trusted."},
	"protection.adopt":               {"接管已有仓库", "Adopt repository"},
	"protection.checkpoint":          {"立即创建检查点", "Create checkpoint"},
	"protection.disable":             {"停用保护", "Disable protection"},
	"protection.disable_confirm":     {"确认停用版本保护？已有历史会保留。", "Disable version protection? Existing history will be kept."},
	"protection.file_history":        {"文件历史", "File history"},
	"protection.history_description": {"输入相对于受管目录的文件路径。", "Enter a path relative to the managed root."},
	"protection.view_history":        {"查看历史", "View history"},
	"protection.restore":             {"恢复此版本", "Restore this version"},
	"protection.restore_confirm":     {"确认把文件恢复到这个 Commit？系统会创建新的恢复提交。", "Restore the file to this commit? A new restore commit will be created."},
	"protection.no_history":          {"这个文件还没有可显示的版本历史。", "This file has no version history to display."},
	"protection.enter_path":          {"输入文件路径后即可查看检查点历史。", "Enter a file path to view its checkpoint history."},
	"run.status.starting":            {"启动中", "Starting"},
	"run.status.running":             {"运行中", "Running"},
	"run.status.stopping":            {"停止中", "Stopping"},
	"run.status.timing_out":          {"超时处理中", "Timing out"},
	"run.status.succeeded":           {"成功", "Succeeded"},
	"run.status.failed":              {"失败", "Failed"},
	"run.status.cancelled":           {"已取消", "Cancelled"},
	"result.succeeded":               {"成功", "Succeeded"},
	"result.failed":                  {"失败", "Failed"},
	"result.accepted":                {"已接受", "Accepted"},
	"result.created":                 {"已创建", "Created"},
	"result.rejected":                {"已拒绝", "Rejected"},
	"result.skipped":                 {"已跳过", "Skipped"},
	"result.missed":                  {"已错过", "Missed"},
	"result.skipped_overlap":         {"因重叠跳过", "Skipped due to overlap"},
	"storage.role.managed":           {"受管目录", "Managed root"},
	"storage.role.state":             {"内部状态", "Internal state"},
	"task.new_directory.title":       {"新建目录", "New directory"},
	"task.new_directory.description": {"在当前受管路径下创建一个目录。", "Create a directory under the current managed path."},
	"task.directory_name":            {"目录名", "Directory name"},
	"task.upload.title":              {"上传文件", "Upload files"},
	"task.upload.description":        {"选择一个或多个文件并上传到当前目录。", "Select one or more files to upload into the current directory."},
	"task.select_files":              {"选择文件", "Select files"},
	"task.replace_existing":          {"同名时替换，并将旧文件移入回收站", "Replace matching names and move old files to Trash"},
	"task.start_upload":              {"开始上传", "Start upload"},
	"task.run.title":                 {"运行脚本", "Run script"},
	"task.run.description":           {"确认参数与超时后启动一个新的 Run。", "Review arguments and timeout, then start a new Run."},
	"task.arguments":                 {"启动参数", "Arguments"},
	"task.timeout_seconds":           {"超时秒数", "Timeout seconds"},
	"task.start_run":                 {"启动 Run", "Start Run"},
	"task.quick_save.description":    {"保存当前脚本、参数和超时设置，之后可以一键再次运行。", "Save the current script, arguments, and timeout so it can be run again in one step."},
	"task.variable_new.title":        {"创建变量", "Create variable"},
	"task.variable_edit.title":       {"编辑变量", "Edit variable"},
	"task.variable_description":      {"名称使用大写字母、数字与下划线，最多 64 个字符。", "Use uppercase letters, numbers, and underscores; maximum 64 characters."},
	"task.password_type":             {"密码类型", "Password type"},
	"task.schedule_new.title":        {"创建计划", "Create schedule"},
	"task.schedule_edit.title":       {"编辑计划", "Edit schedule"},
	"task.schedule_description":      {"配置脚本、五段 Cron 表达式和运行限制。", "Configure a script, five-field cron expression, and run limits."},
	"task.cron_expression":           {"五段 Cron 表达式", "Five-field cron expression"},
	"task.disallow_overlap":          {"禁止重叠运行", "Disallow overlapping Runs"},
	"task.save_changes":              {"保存修改", "Save changes"},
	"run_detail.back":                {"返回运行记录", "Back to Runs"},
	"run_detail.title":               {"运行详情", "Run detail"},
	"run_detail.run_id":              {"Run ID", "Run ID"},
	"run_detail.stop":                {"停止 Run", "Stop Run"},
	"run_detail.force_stop":          {"强制停止", "Force stop"},
	"run_detail.save_quick":          {"保存为快捷执行", "Save as Quick Run"},
	"run_detail.quick_name":          {"快捷执行名称", "Quick Run name"},
	"run_detail.created":             {"发起时间", "Created"},
	"run_detail.runtime_identity":    {"执行身份", "Runtime identity"},
	"run_detail.technical":           {"参数与技术信息", "Arguments and technical details"},
	"run_detail.arguments_template":  {"参数模板", "Argument template"},
	"run_detail.output":              {"输出日志", "Output log"},
	"run_detail.connecting":          {"正在连接实时输出…", "Connecting to live output…"},
	"run_detail.pause":               {"暂停显示", "Pause display"},
	"run_detail.resume":              {"继续显示", "Resume display"},
	"run_detail.log_expired":         {"运行日志已按保留策略清理。", "The Run log was removed by the retention policy."},
	"run_detail.log_incomplete":      {"运行日志写入不完整。", "The Run log is incomplete."},
	"run_detail.log_truncated":       {"运行日志已达到上限。", "The Run log reached its size limit."},
	"editor.preview_title":           {"文本预览", "Text preview"},
	"editor.edit_title":              {"编辑文件", "Edit file"},
	"editor.back_directory":          {"返回目录", "Back to directory"},
	"editor.read_only":               {"只读预览", "Read-only preview"},
	"editor.content":                 {"文件内容", "File content"},
	"editor.save_notice":             {"保存时会校验文件是否已被其他程序修改。", "Saving checks whether another program changed the file."},
	"editor.save_file":               {"保存文件", "Save file"},
	"error.title":                    {"操作未完成", "Operation not completed"},
	"error.return":                   {"返回工作区", "Return to workspace"},
	"error.bad_request":              {"提交的内容无法处理，请检查后重试。", "The submitted information could not be processed. Check it and try again."},
	"error.unauthorized":             {"身份验证失败，请确认凭据后重试。", "Authentication failed. Check the credentials and try again."},
	"error.forbidden":                {"当前请求没有通过安全校验。", "This request did not pass the security check."},
	"error.not_found":                {"请求的记录或路径不存在。", "The requested record or path does not exist."},
	"error.conflict":                 {"当前状态与此操作冲突，请刷新后重试。", "The current state conflicts with this action. Refresh and try again."},
	"error.internal":                 {"ScriptBoard 无法完成此操作。", "ScriptBoard could not complete this operation."},
	"error.technical_details":        {"技术详情", "Technical details"},
	"upload_results.title":           {"上传结果", "Upload results"},
	"delete_impact.title":            {"确认引用影响", "Confirm reference impact"},
	"delete_impact.description":      {"删除此路径会使快捷执行失效并停用相关计划；恢复文件不会自动重新启用计划。", "Deleting this path invalidates Quick Runs and disables related schedules. Restoring the file does not re-enable schedules automatically."},
	"delete_impact.confirm":          {"确认移入回收站", "Move to Trash"},
	"overlap.title":                  {"确认并发运行", "Confirm concurrent Run"},
	"overlap.description":            {"这个脚本已有活动 Run。确认后将并发启动另一个 Run。", "This script already has an active Run. Confirm to start another concurrently."},
	"overlap.confirm":                {"确认并发启动", "Start concurrently"},
}

func resolveWebLocale(request *http.Request) webLocale {
	if cookie, err := request.Cookie(localeCookieName); err == nil {
		if locale, ok := parseWebLocale(cookie.Value); ok {
			return locale
		}
	}
	return negotiateWebLocale(request.Header.Get("Accept-Language"))
}

func parseWebLocale(value string) (webLocale, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "zh", "zh-cn", "zh-hans":
		return localeSimplifiedChinese, true
	case "en", "en-us":
		return localeEnglishUS, true
	default:
		return "", false
	}
}

func negotiateWebLocale(header string) webLocale {
	type preference struct {
		locale webLocale
		q      float64
		order  int
	}
	preferences := make([]preference, 0, 2)
	for index, entry := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ";")
		locale, ok := parseWebLocale(parts[0])
		if !ok {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			parameter = strings.TrimSpace(parameter)
			if strings.HasPrefix(parameter, "q=") {
				if parsed, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64); err == nil {
					quality = parsed
				}
			}
		}
		preferences = append(preferences, preference{locale: locale, q: quality, order: index})
	}
	sort.SliceStable(preferences, func(i, j int) bool {
		if preferences[i].q == preferences[j].q {
			return preferences[i].order < preferences[j].order
		}
		return preferences[i].q > preferences[j].q
	})
	if len(preferences) > 0 {
		return preferences[0].locale
	}
	return localeEnglishUS
}

func webText(locale webLocale, key string) string {
	message, ok := webMessages[key]
	if !ok {
		return key
	}
	if locale == localeSimplifiedChinese && message.zh != "" {
		return message.zh
	}
	if message.en != "" {
		return message.en
	}
	return key
}

func webMessageKeys() []string {
	keys := make([]string, 0, len(webMessages))
	for key := range webMessages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (a *App) setWebLocale(response http.ResponseWriter, request *http.Request) {
	if !a.validWebLocaleCSRF(request) {
		http.Error(response, "CSRF token is invalid", http.StatusForbidden)
		return
	}
	locale, ok := parseWebLocale(request.FormValue("locale"))
	if !ok {
		http.Error(response, "Unsupported locale", http.StatusBadRequest)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     localeCookieName,
		Value:    string(locale),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   isSecureRequest(request),
		SameSite: http.SameSiteLaxMode,
	})
	destination := safeLocalReturnPath(request.FormValue("return_to"))
	if destination == "" {
		if _, _, authenticated := a.loadSession(request); authenticated {
			destination = "/monitor"
		} else {
			destination = "/login"
		}
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
}

func (a *App) validWebLocaleCSRF(request *http.Request) bool {
	token := request.FormValue("csrf_token")
	if current, _, ok := a.loadSession(request); ok {
		return subtle.ConstantTimeCompare([]byte(current.csrfToken), []byte(token)) == 1
	}
	cookie, err := request.Cookie(loginCSRFCookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1
}

func safeLocalReturnPath(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return ""
	}
	return parsed.RequestURI()
}
