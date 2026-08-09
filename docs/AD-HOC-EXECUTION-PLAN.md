# 即时执行临时脚本实施计划

> **已被取代。** 本文中的临时脚本位置与保留策略不再有效；当前实施方案见
> [ONE-TIME-AND-QUICK-EXECUTION-PLAN.md](ONE-TIME-AND-QUICK-EXECUTION-PLAN.md)。
> 本文中的受管根目录、相对路径和 `managedfiles` 设计也已被
> [ADR-0122](adr/0122-browse-the-host-filesystem-with-protected-paths.md) 的主机绝对路径模型取代。

状态：Draft

## 1. 目标

为 ScriptBoard 增加“即时执行”页面。管理员输入一次性脚本、选择工作目录并点击执行后，系统在私有 Run 目录创建临时源码，通过现有 Run 执行链启动任务，并跳转到 Run 详情。

临时源码与日志使用同一个私有目录：

```text
state-root/
  runs/
    {run-id}/
      source.ps1       # Windows
      source.sh        # Linux
      events.jsonl
```

脚本进程的工作目录独立设置为管理员选择的目录：

```text
managed-root/{working-directory}
```

该功能是一次性、非交互的高权限脚本执行能力，不是沙箱，也不提供 SSH、PTY、stdin 或持续 Shell 会话。

## 2. 产品范围

### 2.1 MVP 包含

- 管理员输入一次性脚本源码。
- 管理员选择受管根目录或其下任意可进入的普通目录作为 workdir。
- Windows 固定生成 `.ps1`，Linux 固定生成 `.sh`。
- 强制设置非零超时。
- 复用现有 executor、stdout/stderr 日志、SSE、停止、超时和进程树清理能力。
- 执行成功受理后跳转到 Run 详情。
- Run 详情显示执行时的解释器、源码摘要和 workdir 快照。
- 临时源码与 Run 日志采用相同的保留和容量策略。

### 2.2 MVP 不包含

- SSH、交互式终端、PTY 或 stdin。
- 用户自定义解释器或可执行文件路径。
- CMD、Python、Bash、PowerShell 等多语言自由切换。
- 无限超时。
- 即时脚本的参数模板或变量替换。
- 将即时脚本直接保存为 Quick Run 或 Schedule。
- 容器、虚拟机、系统账户切换或权限降级。
- 自动识别脚本语言。

后续如需复用即时脚本，应增加独立的“另存为受管脚本”流程，要求显式命名、选择保存目录并再次确认。

## 3. 安全模型

即时执行继承 ScriptBoard 当前“单管理员、完全信任脚本、继承服务进程身份”的安全模型。它减少了交互式 SSH 会话的攻击面，但仍允许管理员以 root、LocalSystem 或服务账户权限运行任意代码。

上线时必须明确：

- 功能不是安全沙箱。
- ScriptBoard 无法阻止脚本删除文件、修改系统配置、启动子进程或访问服务账户可访问的凭据。
- workdir 限制只控制进程初始工作目录，不限制脚本访问 workdir 以外的路径。
- 页面确认用于降低误操作，不能代替身份认证和授权。

## 4. 核心设计

### 4.1 源码准备 seam

当前 Run 启动流程直接依赖受管脚本路径。新增即时脚本后，受管脚本和临时源码成为两个 Adapter，应建立真实的源码准备 seam，避免 Web handler 和 Run Manager 分别实现执行细节。

Run Manager 对调用方提供两个清晰入口：

```go
StartManaged(request ManagedStartRequest) (string, error)
StartAdHoc(request AdHocStartRequest) (string, error)
```

两者在模块内部转换为统一结构：

```go
type PreparedScript struct {
    OSPath                  string
    DisplayName             string
    Digest                  string
    Kind                    string // managed / adhoc
    WorkingDirectoryPath    string
    WorkingDirectoryDisplay string
    ScriptInfo              os.FileInfo
    WorkingDirectoryInfo    os.FileInfo
}
```

统一的私有启动流程负责：

- 解析 executor。
- 创建数据库 Run 记录。
- 设置环境变量和 `exec.Cmd.Dir`。
- 创建 stdout/stderr 管道。
- 连接进程树监督。
- 设置超时。
- 启动日志采集。
- 更新 Run 状态。

受管脚本 Adapter 继续使用现有 `managedfiles.Store.PrepareScript`。即时脚本 Adapter 在 `state-root/runs/{run-id}` 中创建源码，但不能把 state 路径暴露为受管脚本路径。

### 4.2 workdir 准备 seam

给 `managedfiles.Store` 增加用于执行场景的目录准备接口，例如：

```go
type PreparedDirectory struct {
    RelativePath string
    OSPath       string
    Info         os.FileInfo
}

func (s *Store) PrepareDirectory(relative string) (PreparedDirectory, error)
```

该模块隐藏全部路径安全实现，调用方不能自行使用 `filepath.Join(managedRoot, input)`。

接口必须保证：

- 输入只能是受管根目录的相对路径；空字符串表示受管根目录。
- 拒绝绝对路径、`..` 逃逸、NUL 和非法路径分段。
- 拒绝 `.git`、`.scriptboard-trash`、上传临时文件等保留路径。
- 每一级都必须是可进入的真实目录。
- 拒绝符号链接、junction/reparse point 和受限制挂载点。
- 返回规范化的 `/` 分隔相对路径。
- 返回目录身份信息，供启动前再次校验。

即时执行必须在进程启动前重新解析 workdir，并使用 `os.SameFile` 或平台等价能力确认目录没有被替换。校验失败时 Run 进入启动失败状态，不执行脚本。

脚本运行期间，Web 文件操作应将 workdir 纳入活动路径租约：

- 禁止删除或移动当前 workdir。
- 禁止删除或移动 workdir 的任何祖先目录。
- 不阻止脚本或管理员修改 workdir 内的普通文件。
- 外部程序直接修改磁盘不在 ScriptBoard 可阻止范围内。

### 4.3 workdir 语义

- 浏览器只提交规范化前的受管相对路径，不提交绝对路径。
- 服务端在 POST 时重新解析，不能信任 GET 页面加载时的选择结果。
- `exec.Cmd.Dir` 使用解析后的绝对路径。
- 数据库保存规范化的受管相对路径；空字符串表示受管根目录。
- Run 页面显示 `/` 或相对路径，不显示宿主机绝对路径。
- workdir 在历史查看时已经不存在，不影响 Run 记录；页面只取消“打开工作目录”链接。

需要明确记录一个脚本语义差异：

- 普通相对路径从选定 workdir 开始解析。
- PowerShell 的 `$PSScriptRoot`、Shell 中脚本自身路径仍指向私有 `state-root/runs/{run-id}`。

系统不得通过向用户源码前置 `cd`、`Set-Location` 等文本来伪造 workdir，因为这会改变源码摘要、错误行号和脚本语义。

## 5. Run 创建流程

建议将启动顺序调整为：

1. 验证会话、CSRF 和即时执行功能开关。
2. 检查维护状态和 State Root 剩余空间。
3. 校验源码、超时和 workdir 输入。
4. 通过 `PrepareDirectory` 解析 workdir。
5. 生成不可预测的 Run ID。
6. 以私有权限创建 `state-root/runs/{run-id}`。
7. 以独占方式创建 `source.ps1` 或 `source.sh`。
8. 写入源码、同步、关闭文件并计算实际字节的 SHA-256。
9. 根据固定扩展名解析 executor。
10. 执行版本保护的 pre-run checkpoint。
11. 启动前重新校验源码文件身份、摘要和 workdir 身份。
12. 创建 Run 数据库记录。
13. 设置 `exec.Cmd.Dir`、环境变量和 stdout/stderr 管道。
14. 启动进程并连接现有监督、停止和超时逻辑。
15. 返回 Run ID，Web 层使用 `303 See Other` 跳转 Run 详情。

临时源码必须作为真实文件交给 executor，禁止将用户输入拼接到：

- `PowerShell -Command`
- `sh -c`
- `cmd /c`
- 任何由字符串拼装的 Shell 命令

## 6. 数据模型

建议给 `runs` 表增加：

| 字段 | 说明 |
|---|---|
| `script_kind` | `managed` 或 `adhoc` |
| `working_directory` | 规范化的受管相对目录；空字符串表示根目录 |
| `source_filename` | `source.ps1`、`source.sh`；普通 Run 为空 |
| `source_expired` | 临时源码是否已过期 |

即时 Run 使用：

```text
source_type = admin/ad-hoc
source_name = 即时执行
script_kind = adhoc
script_path = ""
```

约束：

- 不在数据库中保存即时脚本文本。
- 不在 `script_path` 中保存 State Root 绝对路径或伪造的受管路径。
- 继续使用 `script_sha256` 保存实际执行源码摘要。
- 对现有记录回填 `script_kind=managed`。
- 新创建的普通 Run 也应写入 `working_directory` 快照；旧记录可以保留为空并在 UI 显示未知。

## 7. 私有 Run Artifact

为 Run Manager 增加内部 Artifact Store，集中处理：

- 创建私有 Run 目录。
- 独占创建源码和日志。
- 路径包含关系验证。
- 源码限量读取。
- 目录容量统计。
- 到期清理。

安全要求：

- Run ID 由服务端使用密码学安全随机数生成。
- Run 目录不能由用户输入决定。
- Unix 目录权限使用 `0700`，文件使用 `0600`。
- Windows 继承私有 State Root ACL，并按平台能力进一步收紧。
- 创建源码使用 `O_CREATE | O_EXCL` 等价语义。
- 写入完成后执行 `Sync`、`Close` 和摘要计算。
- 启动前重新验证源码仍是同一个普通文件且摘要一致。
- 所有读取和删除操作都必须再次验证目标位于对应 Run 目录内。

## 8. Web 页面与路由

建议增加：

```text
GET  /execute
POST /execute
GET  /history/runs/{id}/source
```

执行页面包含：

- 脚本源码编辑区域。
- workdir 目录选择器。
- 当前解释器，只读。
- 实际运行身份，只读。
- 超时时间。
- 高权限执行警告。
- 执行按钮。

### 8.1 workdir 选择体验

- 默认选择受管根目录。
- 目录选择器只列出 `managedfiles.Store.List` 返回的普通目录。
- 支持逐级浏览，不一次递归加载整个目录树。
- 文件页面可增加“在此目录即时执行”入口，将当前受管相对路径预填到执行页。
- 浏览器提交隐藏字段 `working_directory`，但服务端仍必须完整校验。
- 页面在提交前显示最终相对路径、解释器、运行身份和超时。

### 8.2 Run 列表和详情

即时 Run：

- 标题显示“即时执行 · {短 Run ID}”。
- 不显示“打开脚本目录”。
- 不显示“保存为快捷执行”。
- 显示“打开工作目录”；仅当该目录仍可安全解析时生成链接。
- 显示 workdir 快照、解释器、运行身份和 SHA-256。
- 可通过认证路由查看当时的源码。

源码查看路由只能通过 Run ID 定位文件，不能接受任意路径参数。读取时需要确认：

- Run 存在。
- `script_kind=adhoc`。
- `source_expired=false`。
- 文件名与数据库允许值一致。
- 最终文件位于对应 Run 目录中。

源码始终作为经过 HTML 转义的纯文本显示，不能以内联 HTML、Markdown 或可执行脚本响应。

## 9. 输入限制

MVP 建议：

| 输入 | 限制 |
|---|---|
| 源码 | 必填，最大 64 KiB |
| 编码 | 合法 UTF-8 |
| NUL | 拒绝 |
| workdir | 已存在的受管普通目录 |
| 超时 | 默认 300 秒，范围 1～3600 秒 |
| 并发 | 同时最多一个即时 Run |

即时执行不加载 ScriptBoard 参数模板和变量。脚本仍继承普通 Run 当前继承的服务进程环境，因此页面必须提醒管理员不要输出密码、Token、私钥或其他敏感值。

## 10. 环境变量

现有环境变量需要区分逻辑来源和物理源码路径：

```text
SCRIPTBOARD_RUN_ID={run-id}
SCRIPTBOARD_SOURCE_KIND=adhoc
SCRIPTBOARD_SCRIPT_PATH=
SCRIPTBOARD_WORKING_DIRECTORY={managed-relative-path}
```

不得通过环境变量暴露 `state-root/runs/{run-id}/source.*` 的绝对路径。executor 自身仍会接收到该路径作为文件参数，这是启动脚本所必需的。

## 11. 审计

审计记录：

- 操作类型，例如 `start_ad_hoc_run`。
- Run ID。
- 源码 SHA-256。
- workdir 相对路径。
- 请求来源地址。
- 接受或拒绝结果。
- 时间。

禁止记录：

- 脚本文本。
- HTTP 请求体。
- Cookie、CSRF Token 或密码。
- stdout/stderr 内容。
- State Root 的绝对路径。

校验失败且尚未创建 Run 时，只记录通用拒绝原因分类，不能把原始输入写入审计或错误日志。

## 12. 保留与清理

临时源码与日志共用当前 Run Artifact 保留策略，默认按 90 天和总容量上限清理。

将现有 `CleanupLogs` 的实现扩展为 Run Artifact 清理：

- 容量统计包括 `events.jsonl`、tail 临时文件和 `source.*`。
- 只清理已经结束的 Run。
- 删除源码和日志后删除空 Run 目录。
- 更新 `log_expired` 和 `source_expired`。
- 永久保留 Run 元数据、摘要、workdir 快照和执行结果。
- 活动、停止中或超时终止中的 Run 不参与清理。
- 服务异常重启后，活动 Run 继续按现有规则标记为 `disconnected`，源码随后进入正常保留周期。
- 删除前必须验证目标目录严格位于 `state-root/runs` 内。

## 13. 配置与发布策略

建议增加显式配置：

```yaml
ad_hoc_execution:
  enabled: false
  max_source_bytes: 65536
  default_timeout: 300s
  max_timeout: 1h
  max_concurrent: 1
```

- 升级和新安装默认关闭，由管理员明确启用。
- 若实例允许非 loopback 访问，建议要求执行前完成近期密码验证。
- 页面始终显示当前服务运行身份。
- 配置解析失败时拒绝启动，不静默使用宽松限制。

## 14. 测试计划

### 14.1 workdir 安全测试

- 空路径正确解析为受管根目录。
- 合法嵌套目录可以作为 workdir。
- 绝对路径被拒绝。
- `..`、混合分隔符逃逸和 NUL 被拒绝。
- 保留目录被拒绝。
- 符号链接、junction/reparse point 被拒绝。
- 跨文件系统的受限制挂载被拒绝。
- 提交后、启动前替换 workdir 会导致启动失败。
- Web 删除或移动活动 workdir及其祖先目录会被拒绝。
- 修改 workdir 内普通文件不被活动路径租约错误阻止。
- Run 历史仅显示相对 workdir，不泄露宿主机绝对路径。

### 14.2 即时执行测试

- Windows/Linux 使用正确扩展名和默认 executor。
- 实际进程工作目录与所选工作目录一致。
- 源码摘要与 executor 实际读取的文件一致。
- 源码中的相对文件访问从所选 workdir 开始。
- `$PSScriptRoot` 或脚本自身路径仍指向私有 Run 目录。
- 空源码、超大源码、非法 UTF-8 和 NUL 被拒绝。
- `timeout=0`、负数和超过上限被拒绝。
- CSRF 失败不会创建 Run 目录或数据库记录。
- 日志、审计和错误信息不包含源码。
- 源码中的 HTML 和脚本标签只按纯文本显示。
- 即时 Run 不出现脚本目录和 Quick Run 入口。
- 停止和超时能够终止完整进程树。
- 同时启动第二个即时 Run 被拒绝。

### 14.3 Artifact 清理测试

- 活动 Run 不被清理。
- 到期后源码和日志同时删除。
- 容量计算包含源码。
- 清理后 Run 元数据仍可查看。
- `source_expired` 和 `log_expired` 状态正确。
- 清理逻辑拒绝 State Root 外部路径。
- 服务重启后的遗留源码仍归属于原 Run。

## 15. 实施任务与依赖

### 任务 1：记录架构决策

- 新增 ADR，修改当前“无 Shell 模式”的产品决策。
- 更新 PRD、DATA-MODEL 和 ACCEPTANCE。
- 固化非交互、固定解释器、私有源码、可选 workdir 和非沙箱语义。

### 任务 2：受管 workdir 模块

- 实现 `PrepareDirectory`。
- 增加路径安全和目录身份测试。
- 扩展活动路径租约，使其保护 workdir 及祖先目录。

依赖：任务 1。

### 任务 3：Run Artifact Store

- 实现私有 Run 目录和源码创建。
- 实现摘要、身份复核、读取和安全删除。
- 增加平台权限和路径包含关系测试。

依赖：任务 1。

### 任务 4：Run 数据模型迁移

- 增加 `script_kind`、`working_directory`、`source_filename`、`source_expired`。
- 回填现有 Run。
- 更新查询、筛选和 Run 结构。

依赖：任务 1。

### 任务 5：重构 Run 启动 seam

- 拆分 `StartManaged` 和 `StartAdHoc`。
- 引入统一 `PreparedScript`。
- 复用 executor、日志、监督、停止和超时实现。
- 保存 workdir 快照并设置 `exec.Cmd.Dir`。

依赖：任务 2、任务 3、任务 4。

### 任务 6：即时执行 Web 流程

- 增加 GET/POST 路由。
- 实现源码编辑器、逐级目录选择和执行确认。
- 实现校验、审计和 `303` 跳转。
- 从文件页面增加“在此目录即时执行”入口。

依赖：任务 5。

### 任务 7：Run 历史适配

- 区分受管 Run 和即时 Run。
- 增加 workdir 展示、打开工作目录和源码查看。
- 隐藏不适用的脚本目录、Quick Run 和 Schedule 操作。

依赖：任务 4、任务 5。

### 任务 8：Artifact 保留与清理

- 将日志清理扩展为 Run Artifact 清理。
- 将源码纳入时间和容量上限。
- 删除空 Run 目录并更新过期状态。

依赖：任务 3、任务 4。

### 任务 9：端到端安全验收

- 完成第 14 节测试。
- 验证审计和服务日志不包含源码。
- 验证 Windows 和 Linux executor 行为。
- 验证现有受管脚本、Quick Run 和 Schedule 行为没有回归。

依赖：任务 2 至任务 8。

## 16. 完成标准

满足以下条件后才可发布：

- 临时源码始终位于对应的私有 Run 目录。
- 用户输入从不被拼接为 Shell 命令字符串。
- workdir 只能是启动时仍存在、未被替换的受管普通目录。
- 所有即时 Run 强制使用非零超时。
- Run 日志、审计和应用错误不记录源码或请求体。
- Run 历史准确区分即时脚本和受管脚本。
- 源码与日志由同一保留策略安全清理。
- 现有受管脚本、Quick Run、Schedule、停止和日志行为全部通过回归测试。
