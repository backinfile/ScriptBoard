# ScriptBoard 数据模型与状态机

本文描述冻结 MVP 的逻辑数据模型。字段类型用于表达约束，具体 SQL 类型由实现选择；所有 ID 建议使用不可预测的 UUID，所有绝对时间以 UTC 保存。

## 宿主分钟指标（Host Metric Minute）

宿主状态以五秒实时样本形成完整自然分钟聚合，固定保留 24 小时：

| 字段 | 约束与语义 |
| --- | --- |
| bucket_at | UTC 自然分钟起点，主键 |
| sample_count | 参与聚合的有效五秒样本数 |
| average_json | 公共资源与设备维度的分钟平均值 |
| maximum_json | 同一组指标的分钟峰值 |

未完成分钟不持久化；服务停止或异常退出会形成明确历史缺口。设备消失后的分钟历史保留至自然过期，当前状态标记为离线。该表属于可自动清理的诊断数据，不是审计记录。

## 应用 Pin（Application Pin）

| 字段 | 约束与语义 |
| --- | --- |
| id | 稳定应用 ID，主键 |
| kind / identity | `host` 或 `docker` 与规范身份；组合唯一 |
| name / technical | 最近一次显示名称与可执行路径或镜像 |
| sort_order | 无上限 Pin 列表中的稳定顺序 |
| created_at / updated_at | UTC |

Pin 是展示状态，不赋予应用控制能力。当前快照存在时由实时事实覆盖保存的名称与技术信息；应用停止或 Docker 数据源不可用时仍保留 Pin 身份。

## 应用分钟指标（Application Metric Minute）

| 字段 | 约束与语义 |
| --- | --- |
| application_id / bucket_at | 应用 ID 与 UTC 自然分钟起点，组合主键 |
| sample_count | 参与聚合的有效五秒样本数 |
| cpu_average / cpu_maximum | 整机归一 CPU 平均值与峰值 |
| memory_average / memory_maximum | 使用字节平均值与峰值 |
| read_average / read_maximum | 读取速率平均值与峰值 |
| write_average / write_maximum | 写入速率平均值与峰值 |

该表只服务已 Pin 应用的 24 小时诊断历史，不是审计记录或长期监控存储。

## 1. 核心原则

- 主机文件系统是文件与目录的事实来源，不建立通用 File 表。
- 数据库只保存应用身份、绝对路径引用、执行、计划、审计、回收条目与持久化文件操作。
- 文件引用同时保存规范绝对路径与平台比较键，并在每次使用时由主机文件模块重新验证保护策略、权限、文件系统和条目类型。
- Run 是不可删除的执行事实；日志是有保留期的外部文件。
- 参数同时保存模板与实际展开值，因为变量全部是普通明文数据。

## 2. 实体

### User

| 字段 | 约束 |
|---|---|
| id | 稳定、不可预测 ID |
| username | 实例内唯一；系统管理员初始默认 `admin` |
| password_hash | 版本化 Argon2id 编码 |
| role | `administrator`、`maintainer`、`operator`、`viewer` 之一 |
| enabled | 系统管理员始终为 true |
| auth_version | 密码、用户名、角色或状态变更时递增，用于撤销 Session |
| created_at / updated_at | UTC |

数据库约束最多一个 `administrator`。普通用户不能提升为系统管理员；账号不永久删除。

### Session

| 字段 | 约束 |
|---|---|
| token_hash | 唯一；不保存浏览器原 Token |
| user_id | 关联 User |
| csrf_token | 随机 CSRF Token |
| auth_version | 必须等于 User 当前版本 |
| created_at / last_seen_at / expires_at | 12 小时空闲、7 天绝对期限 |

### LoginThrottle

| 字段 | 约束 |
|---|---|
| key_type / key_value | 来源 IP 或目标用户名维度 |
| failure_count | 成功登录后清零 |
| blocked_until | 最长 5 分钟 |
| updated_at | UTC |

### Variable

| 字段 | 约束 |
|---|---|
| id | UUID |
| name | 唯一；`[A-Z][A-Z0-9_]{0,63}` |
| value | 普通明文；最大 4 KiB；允许空 |
| created_at / updated_at | UTC |

删除前必须检查 QuickRun 和 Schedule 引用；重命名在同一事务更新活动引用。

### QuickRunGroup

| 字段 | 约束 |
|---|---|
| id | UUID |
| name | 管理员定义；忽略大小写唯一 |
| sort_order | 分组显示顺序 |
| created_at / updated_at | UTC |

“未分组”是 `QuickRun.group_id` 为空时的派生展示区域，不保存为 QuickRunGroup。删除分组时，其中快捷执行项按原组内顺序追加到“未分组”，不会级联删除。

### QuickRun

| 字段 | 约束 |
|---|---|
| id | UUID |
| name | 管理员定义 |
| script_path / script_path_key | 规范绝对路径及平台比较键 |
| argument_text | 原始模板文本 |
| argument_template | 解析后的模板数组 |
| timeout_seconds | 可空代表无超时 |
| always_confirm | 默认 false |
| source_run_id | 可空；从历史创建时关联不可删除 Run，从文件创建时为空 |
| group_id | 可空；引用 QuickRunGroup，删除分组时置空 |
| sort_order | 当前分组内排序；未分组条目共享独立排序域 |
| locked | 默认 false；仅阻止管理员编辑和删除 |
| validity | 派生值，不作为唯一事实来源 |
| created_at / updated_at | UTC |

复制 QuickRun 时保留脚本路径、参数模板、超时与可空来源 Run ID，但生成新 ID 且 `locked=false`。复制到原分组时新项紧随来源项；移动到其他分组或“未分组”时追加到目标排序域。软锁不阻止启动、复制、分组移动、排序或系统维护路径引用。

### ScheduleGroup

| 字段 | 约束 |
|---|---|
| id | UUID |
| name | 管理员定义；忽略大小写唯一 |
| sort_order | 分组显示顺序 |
| created_at / updated_at | UTC |

“未分组”是 `Schedule.group_id` 为空时的派生展示区域，不保存为 ScheduleGroup。删除分组只移除容器，其中计划保留并转为未分组。

### Schedule

| 字段 | 约束 |
|---|---|
| id | UUID |
| name | 管理员定义 |
| group_id | 可空；引用 ScheduleGroup，删除分组时置空 |
| script_path / script_path_key | 规范绝对路径及平台比较键 |
| argument_text / argument_template | 变量引用模板 |
| cron_expression | 标准五段 cron |
| timeout_seconds | 可空代表无超时 |
| overlap_policy | `allow` 或 `skip_if_same_script_running` |
| enabled | 删除脚本时自动 false |
| next_fire_at | UTC 派生缓存，可重算 |
| created_at / updated_at | UTC |

### ScheduleTrigger

| 字段 | 约束 |
|---|---|
| id | UUID |
| schedule_id | 计划删除后可保留历史关系或快照 |
| schedule_name_snapshot | 永久可解释 |
| scheduled_for / observed_at | UTC |
| outcome | `run_created`、`skipped_overlap`、`missed_downtime`、`failed_pre_start` |
| run_id | outcome 为 run_created 时必填 |
| reason | 可空，不含秘密 |

未创建 Run 的明细保留一年，之后进入 ScheduleTriggerAggregate。

### ScheduleTriggerAggregate

| 字段 | 约束 |
|---|---|
| schedule_id / schedule_name_snapshot | 历史身份 |
| local_date | 按实例时区归档日期 |
| outcome | 聚合结果 |
| count | 正整数 |

### Run

| 字段 | 约束 |
|---|---|
| id | UUID |
| script_path / script_path_key | 启动时规范绝对路径及平台比较键 |
| script_sha256 | 启动瞬间摘要 |
| script_kind | `host_file` 或 `one_time` |
| argument_text | 原始参数模板 |
| argument_template | 模板参数数组 |
| resolved_arguments | 启动时实际参数数组 |
| source_type | `manual`、`quick_run`、`schedule` |
| source_id / source_name_snapshot | 可空；历史解释 |
| initiator_user_id / initiator_username_snapshot | 系统计划触发时为空 |
| runtime_identity_name / runtime_identity_id | 用户名/UID 或账号/SID |
| executor | 实际可执行文件与固定前缀参数 |
| executor_fallback_failures | 更早候选无法启动原因 |
| working_directory / working_directory_key | 启动时规范绝对路径及平台比较键 |
| status | 见状态机 |
| pid / process_group_or_job_id | 活动期信息 |
| exit_code | 可空 |
| timeout_seconds | 可空 |
| created_at / started_at / finished_at | UTC；按状态允许为空 |
| stop_requested_at / force_kill_requested_at | 可空 |
| termination_reason | 可空 |
| error_message | 可空；不得包含秘密 |
| log_manifest_path | 状态目录相对路径 |
| log_expired / log_incomplete / log_truncated | 布尔标记 |

Run 不允许删除。

### RunLogManifest

存于每个 Run 私有日志目录，可同时在数据库保存摘要索引：

- format_version
- first_sequence / last_sequence
- persisted_bytes / discarded_bytes
- head_bytes / tail_bytes
- truncated
- incomplete
- segment 列表及摘要

事件包含 sequence、captured_at、stream、raw bytes。浏览器展示是安全解码视图，不是日志事实来源。

### RunLease

主要为内存态，可在数据库保存恢复提示：

- platform_file_id
- normalized_script_path
- protected_ancestor_paths
- active_run_ids

同一文件多个活动 Run 共享租约计数；最后一个结束才释放。

### TrashEntry

| 字段 | 约束 |
|---|---|
| id | UUID |
| original_path / original_path_key | 删除前规范绝对路径及平台比较键 |
| stored_path / stored_path_key | 源文件系统私有回收区中的规范绝对路径及比较键 |
| entry_type | file 或 directory |
| size | 删除时估算 |
| deleted_at | UTC |
| affected_quick_run_ids / schedule_ids | 通过确认页和审计关联 |

### AuditEvent

| 字段 | 约束 |
|---|---|
| id | UUID/有序 ID |
| occurred_at | UTC |
| actor_user_id | Web 用户稳定 ID；系统操作为空 |
| actor_username / actor_role | 操作者当时的用户名和角色快照 |
| action | 稳定英文标识 |
| target_type / target_id / target_snapshot | 最小必要信息 |
| outcome | success / failure |
| source_ip | Web 操作时保存 |
| details | 结构化、已脱敏 |

不保存密码、Cookie、Session、CSRF、变量值、文件内容或请求正文。默认保留一年，不允许逐条修改。

### FileOperation

跨文件系统移动的持久化恢复记录：

| 字段 | 约束 |
|---|---|
| id | UUID |
| kind | 当前固定为 `cross_filesystem_move` |
| source_path / source_path_key | 源规范绝对路径及平台比较键 |
| destination_path / destination_path_key | 目标规范绝对路径及平台比较键 |
| temporary_path | 目标同目录临时项；尚未建立时为空 |
| trash_path | 源文件系统回收项；尚未提交源时为空 |
| phase | 预扫描、复制、校验、提交目标、回收源、更新引用、完成或失败 |
| bytes_total / bytes_completed | 扫描后总量与已复制进度 |
| verification_digest | 内容校验摘要 |
| cancel_requested | 持久化取消请求 |
| error | 终态或待恢复错误，不含文件内容与秘密 |
| created_at / updated_at | UTC |

记录必须足以在进程崩溃后判断源、临时项、已提交目标和源回收项的状态，并幂等继续或回滚。

## 3. Run 状态机

```mermaid
stateDiagram-v2
    [*] --> starting
    starting --> running: process created
    starting --> failed: process creation failed
    running --> succeeded: exit code 0
    running --> failed: non-zero exit
    running --> stopping: admin graceful stop
    stopping --> cancelled: process exits or forced
    running --> timing_out: deadline reached
    timing_out --> timed_out: exits or forced after grace
    running --> disconnected: service supervision lost
    stopping --> disconnected: service supervision lost
    timing_out --> disconnected: service supervision lost
    succeeded --> [*]
    failed --> [*]
    cancelled --> [*]
    timed_out --> [*]
    disconnected --> [*]
```

### 状态不变量

- 启动前路径、保护策略、权限、参数、变量或执行器校验失败时不创建 Run，只写审计。
- Run 创建后初始为 starting；进程创建失败才产生 starting → failed。
- stopping 的最终结果固定为 cancelled；timing_out 的最终结果固定为 timed_out。
- disconnected 是 ScriptBoard 失去监督后的终态，不重新接管 PID。
- 终态 Run 不可删除或回到活动状态。

## 4. 跨文件系统移动状态机

```mermaid
stateDiagram-v2
    [*] --> scanning
    scanning --> copying: preflight and space check succeed
    scanning --> failed: restricted entry, conflict, or insufficient space
    scanning --> cancelled: cancellation succeeds before copying
    copying --> ready_to_commit: bytes and metadata verify
    copying --> failed: copy or verification fails
    copying --> cancelled: cancellation cleans the temporary item
    copying --> cleanup_pending: temporary cleanup must resume after restart
    ready_to_commit --> target_committed: target atomically renamed into place
    target_committed --> source_trashed: verified source enters its filesystem trash
    source_trashed --> completed: trash registration and reference transaction commit
    scanning --> rolled_back: startup recovery
    copying --> rolled_back: startup recovery
    ready_to_commit --> rolled_back: startup recovery before target commit
    cleanup_pending --> rolled_back: startup cleanup succeeds
    completed --> [*]
    rolled_back --> [*]
    cancelled --> [*]
    failed --> [*]
```

### 文件操作状态不变量

- 目标提交前失败或取消必须清理临时项并保持源不变。
- 目标只通过同目录原子重命名变为可见；源只移动到带实例所有权标记的同文件系统回收区。
- 目标提交后不得把操作伪装成未发生；重启恢复必须依据磁盘事实继续源回收和引用事务，或报告可解释失败。
- 目录预扫描发现链接、特殊文件或无法无损保留的平台元数据时，在复制前拒绝。
- 源和目标路径租约覆盖祖先与后代，阻止 Run 和其他文件事务竞争。

## 5. 文件引用不变量

- QuickRun 与 Schedule 引用规范绝对路径和平台比较键，路径解释只能经过主机文件模块。
- 同文件系统移动以原子重命名与数据库事务更新引用；跨文件系统移动由 FileOperation 恢复协议保证可解释状态。
- 外部移动使引用失效，不按名称、摘要或 inode 猜测新路径。
- Run 保存路径快照，不随当前文件移动。
- 变量重命名更新 QuickRun 与 Schedule；Run 历史不改写。

## 6. 建议索引

- Session(token_hash), Session(expires_at)
- Variable(name)
- QuickRun(sort_order), QuickRun(script_path_key)
- Schedule(enabled, next_fire_at), Schedule(script_path_key)
- ScheduleTrigger(schedule_id, scheduled_for), ScheduleTrigger(run_id)
- Run(created_at DESC), Run(status, started_at), Run(script_path_key, status)
- Run(source_type, source_id)
- AuditEvent(occurred_at DESC), AuditEvent(action, occurred_at)
- TrashEntry(original_path_key), TrashEntry(deleted_at)
- FileOperation(phase, updated_at), FileOperation(source_path_key), FileOperation(destination_path_key)
- AssistantModel(is_default), AssistantConversation(owner_user_id, archived_at, updated_at), AssistantMessage(conversation_id, sequence)
- AssistantToolCall(conversation_id, started_at), AssistantApproval(conversation_id, status, expires_at)

## 7. 文件布局

```text
host-filesystems/
  <ordinary host files>       # 文件系统本身是事实来源，不由 ScriptBoard 建表镜像
  .scriptboard-trash/         # 每个发生删除的文件系统各自建立，带实例所有权标记并受保护

state-root/
  app.db
  app.db-wal
  app.db-shm
  instance.lock
  secrets/
  runs/{run-id}/
    events-*.log
    manifest.json
  logs/
    scriptboard.log
    scriptboard.log.1 ...
  runtime.json                # 当前进程写入的精确构建运行标记
  updates/
    cache.json                # 最近一次有效检查、ETag 与错误摘要
    check.json                # 最近一次检查时间与错误（包括首次失败）
    active.json               # 当前 Update Operation
    operations/{id}/
      operation.json          # 持久阶段、目标与恢复信息
      result.json             # 提交、回滚或人工恢复结果
      database-before-update.db # helper 在旧进程退出后创建的一致快照
      release-manifest.json
      release-manifest.json.sig
      scriptboard-v*.{zip,tar.gz}
      extracted/              # 安全解压后的 Release 内容
      helper/                 # Windows 本次事务使用的独立 helper
  tmp/
```

State Root、State Root 同级的外部凭据主密钥目录、Install Root、活动配置、管理员密码文件、TLS 私钥和各文件系统回收区均为受保护路径。文件页面不显示这些路径；其祖先不能执行会影响后代的写入、移动、删除、覆盖或执行。磁盘上已有 `.git/` 不属于应用状态，ScriptBoard 不修改或删除它。

正式系统服务另有独立程序布局，不属于 State Root：

```text
install-root/
  install.json
  current -> versions/<version>    # Linux 原子符号链接；Windows 服务配置指向版本目录
  versions/<version>/
    RELEASE.json
    scriptboard[.exe]
    scriptboard-broker[.exe]       # 固定主机写动作的独立特权进程
    scriptboard-ai-host[.exe]      # 独立身份运行 Pi 的受限 Runtime Host
    scriptboard-runner[.exe]       # 复核摘要并在独立身份执行 Run 的 Worker
    scriptboard-updater[.exe]
    ...                            # 对应平台完整 Release 内容
  scriptboard-updater              # 仅 Linux；切换前原子刷新、供恢复使用的独立 helper
  scriptboard-tray-launcher.exe    # 仅 Windows；稳定托盘入口
```

受管 Linux Web 服务以无登录 `scriptboard-web` 运行，Windows Web 服务以 `LocalService` 加独立
`NT SERVICE\ScriptBoard` SID 运行；它们只获 Install Root 读/执行、配置读取以及 State Root 和
实例外部密钥目录修改权限。特权 Broker 分别保留 root/LocalSystem，并只通过受保护本机 IPC
接受该 Web 服务身份。Run、Assistant 与凭据解封尚未完成独立身份迁移，不能把当前 ACL 视为
最终最小权限模型。

Update Operation 是文件系统持久化事务，不写入 SQLite 作为事实来源，以便数据库本身被恢复时仍能继续判断更新阶段。终态结果由应用在正常启动后幂等导入审计一次。

主机安全写操作使用独立 Broker 的内存 capability，不新增 SQLite capability 表。Broker 收到
authorize 请求后直接用原始会话 token 的 SHA-256 查询 `sessions`/`users`，重新检查认证版本、
角色、期限与近期 step-up；随机 capability 只在 Broker 内存保留 30 秒并在任何 execute 尝试时
先消费。`privileged_broker.<action>` 的 `attempted` 与终态事件由 Broker 自己写入审计链并刷新
外部签名 checkpoint，Web 的同层业务审计不作为 Broker 执行授权或成功事实来源。

## 8. 网站监控

| 实体 | 关键字段与保留 |
| --- | --- |
| WebsiteMonitor | 配置 JSON（包括仅保存变量引用模板的自定义请求头）、服务位置、协议、管理员顺序、状态、失败计数、配置代次、下一检查时间；删除后保留一年 |
| WebsiteCheckResult | 成功、状态码、耗时、错误类别、技术证据、证书快照；保留 24 小时 |
| WebsiteHourlyAggregate | 每小时检查数、成功/失败数、平均/最大耗时与错误类别计数；保留 30 天 |
| WebsiteIncident | 确认故障的开始、结束、首个错误事实与关闭原因；完成后保留一年 |

```mermaid
stateDiagram-v2
    [*] --> pending
    pending --> up: first check succeeds
    pending --> verifying: first check fails
    up --> verifying: check fails
    verifying --> down: confirmation check fails
    verifying --> up: confirmation check succeeds
    down --> up: later check succeeds
    pending --> paused: admin pauses
    up --> paused: admin pauses
    verifying --> paused: admin pauses
    down --> paused: admin pauses
    paused --> pending: admin resumes
```

配置更新递增 generation、清除旧证据并立即重新检查；较旧 generation
的在途结果不得回写。管理员暂停或删除时同样递增 generation。WebSocket
应用消息规则只处理文本帧和二进制帧；Ping/Pong 规则只处理 RFC 6455
控制帧。

网站探测的 TLS 验证例外不是永久配置。管理员每次保存关闭验证的监控时只签发一小时
例外，到期后读取和执行配置都会恢复验证；创建和更新审计目标记录例外到期时间。远程
ScriptBoard 网站状态来源只接受 HTTPS，且仍经过共享出站地址策略并禁止重定向。

网站监控与 Custom Dashboard 配置导入共享“JSON 配置文件”外层安全约束，但继续使用
独立的领域 schema：文件名必须是安全的单一 `.json` 目标，MIME 只接受 JSON、纯文本或
通用 multipart 二进制，内容必须是 UTF-8、无 NUL 且以 JSON 对象开始。之后分别执行
网站监控最多 100 项/128 MiB 与 Dashboard 最多 100 卡片/2 MiB 的未知字段拒绝、版本和
领域字段校验；外层策略不能替代领域解码。

Custom Dashboard 的 HTTP 数据源继续允许保存 `Authorization`、`Cookie` 等业务凭据头，
但 URL 不得内嵌凭据或片段，请求头不得覆盖 Host、代理认证、连接或传输语义。运行时使用
共享出站策略固定经校验的公网目标，不读取环境代理、不跟随重定向，并限制请求头、响应
大小和超时；默认不能访问回环、私网、链路本地或云元数据服务。

## 9. 一次性 Run 源码

`Run.script_kind` 区分 `host_file` 与 `one_time`。一次性 Run 额外保存：

| 字段 | 语义 |
| --- | --- |
| working_directory / working_directory_key | 允许的普通主机目录规范绝对路径及平台比较键 |
| source_filename | 私有 Run 目录内固定的 `source.{ext}` |
| source_expired | 源码已回收，源码入口返回 410 |
| source_audit_event_id | 回收前关联的 `start_one_time_run` 审计主键 |
| script_sha256 | 实际执行源码的内容摘要，源码回收后仍保留 |

源码不是 Host Entry 或 Trash Entry。审计清理先删除源码，再在同一数据库事务中
设置 `source_expired=1`、清空审计引用并删除审计条目。文件删除失败时三个数据库
变更均不得发生。RunLogManifest 的 90 天/容量清理不处理源码文件。

## 10. AI Assistant

schema 21 在 schema 20 主机文件基线上增加 Assistant 自有表；schema 24 在兼容的
schema 21–23 上增加 Conversation Profile、Session Telemetry 与模型图片输入事实。
迁移在同一事务内补列、建索引并更新 `user_version`；早于 20 或高于当前版本的状态库
仍在任何写入前拒绝。

| 实体 | 关键字段与语义 |
| --- | --- |
| AssistantSettings | 单例；功能启用、最大活动对话数、新对话自动审批默认值、更新者和时间 |
| AssistantModel | 名称、Provider、模型 ID、HTTPS/回环 Endpoint、凭据已配置事实、`supports_images` 管理员声明、唯一默认项；不保存 API Key |
| AssistantConversation | 所有者、标题、必选模型、审批模式、Pi session 相对标识、Runtime 版本、Conversation Profile 与版本、思考级别、Session Telemetry、状态、revision、归档时间 |
| AssistantMessage | 对话内稳定 sequence、user/assistant、正文、streaming/complete/interrupted/error 与完成时间 |
| AssistantContextRef | 对话、资源种类、稳定 ID、安全标签和展示顺序；不复制文件或日志正文 |
| AssistantToolCall | 工具名、目标/参数摘要、状态、稳定错误码、结果摘要、时间，以及对话内可检查的有界调用/返回 JSON；调用 JSON 不含 capability 或 Provider 凭据 |
| AssistantApproval | 工具调用、参数摘要、pending/approved/rejected/expired/cancelled、过期时间和决定者 |

AssistantConversation 只能由 `owner_user_id` 对应用户列出、读取、订阅和修改。每个对话
同一时间最多存在一个 streaming assistant message；服务启动时，仍为 running 或
waiting_approval 的对话及消息原位转为 interrupted，不创建重放任务。归档只设置时间，
不会级联删除消息、资源引用或 Pi session。

提交消息时，用户消息、streaming assistant message、conversation running 状态和该次
完整资源引用集合在一个事务内写入；遗漏的旧引用会被移除。每个 Agent Turn 随后重新校验
引用权限并生成有界快照。目录只包含逻辑名称与最多 48 个直接子项元数据；明确引用的普通
UTF-8 文本文件最多附带 16 KiB 正文并记录 SHA-256，不把宿主绝对路径送入 Prompt。
明确引用的 PNG、JPEG 或 WebP 在当前角色重新授权后进入 Safe Raster Processor；输入
最多 10 MiB/40 MP，输出最长边 2048、单图最多 4 MiB、每次最多四图，只在内存中保存
重新编码后的 base64。原图、EXIF、GPS 和处理后图片均不持久化到 Assistant 表、审计或日志。

Provider API Key 按 AssistantModel ID 保存到
`state-root/secrets/assistant-provider.json`，使用私有权限与同目录原子替换；它不进入
SQLite、HTML、审计、SSE 或普通日志。删除仍被对话引用的模型受外键和领域检查共同拒绝。

```text
state-root/
  secrets/
    assistant-provider.json
  assistant/
    runtime/
      active.json
      versions/<version>/
        pi[.exe]
        scriptboard-extension.ts     # 正式签名 Runtime 的唯一固定 Extension
        capabilities.json            # 版本、大小和 SHA-256 固定的能力清单
        playbooks/*.md                # 由清单显式列出的 Operational Playbook
        runtime.json                 # Pi/RPC/Broker 合同和上游 commit
        LICENSE
    pi-home/<user-id>/<conversation-id>/
      models.json                    # 只引用会话 Provider capability，不含上游 Endpoint 或实际 API Key
    sessions/<user-id>/<conversation-id>/
    workspaces/<user-id>/<conversation-id>/
```

所有 Assistant 目录都属于 State Root 受保护范围，不显示为 Host Entry。活动 Runtime
解析只接受 `active.json` 指向自身版本目录内的普通文件，绝不查询 PATH 或用户 Pi 目录。
私有 session 目录已有非空 JSONL 时，下一次受管进程使用 `--continue` 恢复；每个 Turn
开始前仍通过 RPC `set_model` 重选该对话当前模型，避免保温进程沿用过期模型。
非通用 Conversation Profile 必须能在活动 Runtime 的 Capability Bundle 中解析出完全匹配
的版本；缺失、摘要不符或路径越界时拒绝本次 Turn，不回退到用户级 Skill。每个 Turn 还会
重设思考级别和自动压缩/重试策略；settled 后把 Pi session stats 写回 Conversation，手动
压缩只允许空闲的活动 session。

Tool Broker 使用每个受管 Pi 进程独有的 Named Pipe/Unix Socket 与 256-bit capability。
capability 不持久化；AssistantToolCall 记录规范参数、目标、有界结果摘要，以及不含
capability 和 Provider 凭据的有界调用/返回 JSON，
AssistantApproval 绑定用户、角色、授权版本、对话、Tool Call、参数和目标当前状态。
服务重启把尚未完成的工具标记为 interrupted，并取消 pending/approved 的状态修改。

Provider 凭据 JSON 先由统一 credential store 以用途绑定的 AES-GCM 密封，再写入 State
Root；其主密钥在 State Root 同级受保护目录，Windows key blob 使用机器级 DPAPI，Unix
key 文件仅允许服务身份/root。旧明文 `assistant-provider.json` 在启动时原子迁移并删除。

每个受管 Pi 进程同时获得独立的环回 Provider 代理和 256-bit capability。代理在 Web
进程内持有实际 Provider Endpoint 与 API Key，只接受绑定模型对应的固定推理 POST 路径，
禁止重定向并复用共享出站策略；Pi 的参数、环境和 `models.json` 不再包含上游 Endpoint
或真实 API Key。代理随 Pi 进程退出或会话停止而关闭。该进程内边界不替代 P0-08 要求的
独立 OS 身份、秘密目录 ACL 和 Runtime 网络默认拒绝。

Windows 上每个 Pi 进程还进入独立 Job Object：最多一个活动进程，进程与作业内存各限
1 GiB，累计用户态 CPU 限 15 分钟，并禁止桌面、显示设置、退出系统、全局 atom、句柄、
剪贴板和系统参数 UI 能力；Job 句柄关闭时终止剩余进程。该资源边界同样不赋予受限
Token，也不代替秘密目录 ACL。

Evidence Query 仍走相同 Tool Broker 和实时角色授权。日志搜索、日志窗口、Run 对比、计划
历史和审计列表都有结果条数与文本字节上限；继续读取使用带 HMAC、五分钟过期并绑定用户、
对话、工具、目标和查询的不透明游标，不能跨查询或跨对话复用。

## 11. 文件快捷访问

`file_quick_access_pins` 持久化当前实例的全局文件页固定目录，最多 30 项：

| 字段 | 约束 |
|---|---|
| path / path_key | 固定时由 Host Filesystem 规范化的目录绝对路径及平台比较键 |
| label | 从规范路径派生的显示名称 |
| sort_order | 用户范围内的稳定顺序 |
| created_at | UTC |

全局按 `path_key` 唯一，所有有文件页权限的用户读取和修改同一列表。目录暂时离线或权限变化时保留记录，展示时重新验证可访问性；固定新目录时必须通过 Host Filesystem 的目录边界。schema 28 最初增加用户级固定项，schema 29 将其按路径去重合并为全局列表；旧浏览器本地固定项仍作为一次性兼容迁移来源。

## 12. External Interfaces

schema 27 增加 `external_trigger_keys`、`external_trigger_entries` 和 `external_trigger_requests`。Key 在 SQLite 中只保存标签、Token 的不可逆摘要与提示、启用状态、到期时间和最近成功使用时间；完整 Token 仅在创建或轮换后返回一次，不持久化。Entry 保存动作类型、固定目标与经过类型校验的 JSON 约束；schema 37 将其收紧为每个 Key 唯一绑定一个不可变能力。Request 保存不可变的调用结果摘要，不通过外键级联删除，以便 Key 删除后仍保留审计上下文。

schema 38 增加持久化单例 `external_trigger_control`，用于全局紧急暂停所有有效外部调用。schema 39 在 Entry 上增加 `require_signature`，并用 `external_trigger_nonces` 原子消费短期 nonce；nonce 按 Key 唯一并带过期时间。迁移的旧 Entry 默认保持 Bearer 兼容，新 Entry 默认要求 5 分钟时间戳、唯一 nonce 和 HMAC-SHA256 签名。schema 40 在 `sessions` 增加 `authentication_assurance` 和 `reauthenticated_at`；高风险声明式路由要求 10 分钟内的浏览器会话密码认证，Assistant UI 动作不会为这些路由提供替代入口。schema 41 在 `audit_events` 增加 `request_id` 与 `authentication_assurance`；新事件把两者纳入 v2 哈希，历史空字段事件继续按 v1 验证。schema 42 在 `users` 增加 `mfa_required_at`；Administrator/Maintainer 到期且未配置任一第二因素时，只能访问 MFA 注册与带外退出路径。

`audit_events` 按 ID 顺序链接 `previous_hash` 与 `event_hash`，`audit_chain_state` 保存保留锚点和
当前链尾。为防止事件尾部与同库链尾状态一起回退后仍通过本地校验，每个 State Root 另有一份
Ed25519 签名 checkpoint，记录实例路径身份、最后事件 ID/摘要、签名时间和公钥。签名私钥密文
`../secrets/audit-checkpoint-signing-<实例摘要>.enc` 与
`../secrets/audit-checkpoint-<实例摘要>.json` 均位于 State Root 外；私钥由统一外部主密钥以
独立用途密封。启动、`audit verify` 和取证导出同时验证本地链、签名、公钥绑定与 checkpoint
成员关系；只读命令缺失材料时失败而不初始化。正常关闭、保留清理后和每五分钟刷新 checkpoint。
它限制 State Root 单独回退，不等同于远端不可变日志；最近刷新后的尾部窗口与拥有外部 secrets
目录权限的高权限攻击者仍需由后续远端转发和告警覆盖。

账户 TOTP 状态不新增 SQLite 明文秘密列，而是保存在 `state-root/secrets/account-mfa.enc`：整个
用户映射由 State Root 外的统一主密钥以用途绑定 AES-GCM 密封。每个账户记录已确认或待确认的
TOTP secret、最后接受的时间步与恢复码 SHA-256 摘要；恢复码具有独立 128 bit 随机熵、只显示
一次并在使用时原子移除。已配置账户的登录与 step-up 均接受 TOTP 或未使用恢复码，成功会话记录
`aal2`；同一 TOTP 时间步不能重放。本机管理员重置会清除管理员 MFA 并撤销会话。

WebAuthn 凭据保存在 `state-root/secrets/account-passkeys.enc`，使用与 TOTP 不同的用途绑定整体密封。记录包含 credential ID、公钥、attestation 元数据、flags 与 authenticator counter；每次成功断言后原子写回更新后的 counter/flags。注册、登录和 step-up challenge 只存在于进程内存，绑定 ceremony 类型、用户、浏览器会话与精确 Origin，五分钟过期且在 finish 时先消费。注册要求 user verification，优先创建 discoverable credential；登录仍先验证账户密码，passkey 作为第二因素达到 `aal2`。

只读远程网站来源等仍需恢复的 External Interface 相关秘密使用统一 credential store 密封；
旧 `external-interface.master-key` 与逐项密文启动时解密、重封并先删除旧原始 key。一次显示
Trigger Key 本身只保留不可逆 verifier，不进入这套可恢复秘密存储。

变量与快捷执行条目使用 `target` 建立领域引用：目标被引用时禁止删除；变量被引用时也禁止改名或转为密码变量。日志文件与上传目录都在配置和调用时通过 Host Filesystem 边界重新验证；日志动作将规范化后的文件绝对路径保存在 `target` 与 `config_json.file` 中。到期 Key 不需要后台任务修改数据库；鉴权时根据当前时间派生为不可用状态。

私有上传收件箱同时接收 External Interface 文件与 Host Files 页面提交的可执行扩展。两类
payload 都只使用随机目录内固定无扩展名、0600 文件，metadata 保存来源、原始文件名、规范
目标目录、冲突策略、大小、SHA-256 和创建时间。Host Files 普通文档仍可原子直传；内置或
自定义执行器扩展必须经 step-up 发布路由重新读取并校验整个 payload 后才进入主机路径，且
普通上传不得直接覆盖已有可执行文件。

## 13. MySQL 备份恢复管理

schema 30 增加独立的 `mysqlmanager` 领域表；Web 层只调用领域服务，不直接执行管理 SQL 或拼装客户端参数。

| 实体 | 关键字段与语义 |
| --- | --- |
| MySQLInstance | 名称、TCP 地址、用户名、TLS 模式、CA 路径和凭据已配置事实；SQLite 不保存密码 |
| MySQLBackupPlan | 实例、数据库集合、五字段 Cron、保留数量、启用状态和下次触发时间 |
| MySQLBackup | 单库产物路径、SHA-256、字节数、来源类别、计划引用和成功时间 |
| MySQLOperation | 操作类型、目标库、阶段、进度、安全备份引用、错误摘要、取消请求和起止时间 |
| MySQLSetting | 备份根目录以及宿主 `mysqldump`/`mysql` 客户端路径 |

实例密码由 State Root 外的统一主密钥使用用途绑定 AES-GCM 密封后保存到独立凭据文件；Windows 主密钥 blob 再受机器级 DPAPI 保护，Unix 使用 root-only 外部 key。旧 State Root 内 MySQL 原始 key 在启动迁移时先删除。CLI 每次只读取临时、权限受限的 option file，完成后立即删除；参数、错误、审计和 HTML 均不得包含密码。默认备份根目录是 `state-root/database-backups/mysql`，自定义绝对目录同样进入 Host Filesystem Protected Path。

每个成功备份对应一个原子提交的 `.sql.gz` 和 SHA-256。现有库恢复在替换前强制创建 `safety` 备份；导入失败自动从该产物回滚，回滚失败进入 `needs_attention`。服务启动会删除未提交的 `.partial`，并根据持久化阶段恢复破坏性操作。计划只轮换自身成功产物，手动、导入和安全备份不参与轮换。

导入只接受 `.sql` 或 `.sql.gz`。gzip 在接收时及每次恢复前完整解码验证，解压后 SQL
最多 8 GiB，避免小型压缩包造成无界 CPU/磁盘输入。恢复客户端固定使用
`--binary-mode --batch --skip-reconnect`，数据库参数放在 `--` 后；MySQL 与 MariaDB 的
非交互 binary mode 会禁用 `system`、`source`、pager、tee 等本地客户端命令，同时保留
dump 所需的字符集与 delimiter 语义。
