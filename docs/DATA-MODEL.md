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

## 1. 核心原则

- 文件系统是受管条目的事实来源，不建立通用 File 表。
- 数据库只保存应用身份、引用、执行、计划、审计与 Git 管理状态。
- 文件引用保存相对受管根目录的规范路径，并在使用时重新验证真实路径、设备/卷与文件类型。
- Run 是不可删除的执行事实；日志是有保留期的外部文件。
- 参数同时保存模板与实际展开值，因为变量全部是普通明文数据。

## 2. 实体

### Admin

| 字段 | 约束 |
|---|---|
| id | 唯一固定管理员 ID |
| username | 唯一；默认 `admin` |
| password_hash | 版本化 Argon2id 编码 |
| must_change_password | 首次登录为 true |
| credential_version | 凭据变更时递增，用于撤销 Session |
| created_at / updated_at | UTC |

### Session

| 字段 | 约束 |
|---|---|
| id | 内部 ID |
| admin_id | 固定关联 Admin |
| token_hash | 唯一；不保存浏览器原 Token |
| csrf_secret | 服务端 CSRF 派生材料 |
| credential_version | 必须等于 Admin 当前版本 |
| created_at / last_seen_at / expires_at | 12 小时空闲、7 天绝对期限 |
| source_ip / user_agent | 审计辅助；按最小必要保存 |
| revoked_at | 可空 |

### LoginThrottle

| 字段 | 约束 |
|---|---|
| key_type / key_value_hash | IP 或 admin 维度；避免保存不必要原值 |
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
| script_path | 规范相对路径 |
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
| script_path | 规范相对路径 |
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
| script_path_snapshot | 启动时规范相对路径 |
| script_file_id_snapshot | 平台文件身份，用于诊断 |
| script_sha256 | 启动瞬间摘要 |
| script_versioned | 启动时 Git 保护状态 |
| argument_text | 原始参数模板 |
| argument_template | 模板参数数组 |
| resolved_arguments | 启动时实际参数数组 |
| source_type | `manual`、`quick_run`、`schedule` |
| source_id / source_name_snapshot | 可空；历史解释 |
| runtime_identity_name / runtime_identity_id | 用户名/UID 或账号/SID |
| executor | 实际可执行文件与固定前缀参数 |
| executor_fallback_failures | 更早候选无法启动原因 |
| working_directory_snapshot | 启动时绝对路径或安全相对表达 |
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
| original_path | 删除前相对路径 |
| trash_path | 回收站内部随机路径 |
| entry_type | file 或 directory |
| size | 删除时估算 |
| deleted_at | UTC |
| deleted_by | 固定 admin |
| affected_quick_run_ids / schedule_ids | 可存快照或通过审计关联 |

### AuditEvent

| 字段 | 约束 |
|---|---|
| id | UUID/有序 ID |
| occurred_at | UTC |
| actor_type | `admin`、`scheduler`、`system`、`startup_config` |
| action | 稳定英文标识 |
| target_type / target_id / target_snapshot | 最小必要信息 |
| outcome | success / failure |
| source_ip | Web 操作时保存 |
| details | 结构化、已脱敏 |

不保存密码、Session、CSRF、变量值、文件内容或请求正文。默认保留一年，不允许逐条修改。

### GitProtection

单实例单记录：

| 字段 | 约束 |
|---|---|
| enabled | 管理员开关 |
| state | `disabled`、`healthy`、`abnormal` |
| repository_id | ScriptBoard 仓库标记 |
| branch | 固定 `scriptboard-managed` |
| git_executable | 最终解析路径 |
| max_tracked_file_bytes | 默认 10 MiB |
| max_repository_bytes | 默认 5 GiB |
| last_commit | 可空 |
| pending_batch_run_ids | 活动批次摘要 |
| abnormal_reason | 可空 |
| updated_at | UTC |

Git 是文件系统中的版本事实来源，数据库保存管理状态与审计关联。

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

- 启动前路径、参数、变量、执行器和 Git 安全门校验失败时不创建 Run，只写审计。
- Run 创建后初始为 starting；进程创建失败才产生 starting → failed。
- stopping 的最终结果固定为 cancelled；timing_out 的最终结果固定为 timed_out。
- disconnected 是 ScriptBoard 失去监督后的终态，不重新接管 PID。
- 终态 Run 不可删除或回到活动状态。

## 4. Git 保护状态机

```mermaid
stateDiagram-v2
    [*] --> disabled
    disabled --> healthy: enable + baseline/adoption checkpoint
    healthy --> disabled: final checkpoint + confirmed disable
    healthy --> abnormal: checkpoint/config/HEAD/integrity failure
    abnormal --> healthy: diagnose + successful retry
    abnormal --> disabled: confirmed disable
```

### Git 状态不变量

- healthy 才允许新的脚本执行和受保护文件修改。
- 未跟踪文件资格不使仓库 abnormal。
- 任意 Run 活动期间不执行 Git add/commit/restore/gc 或启停。
- abnormal 不自动清理、reset 或切换分支。

## 5. 文件引用不变量

- QuickRun 与 Schedule 引用规范相对路径，不引用任意绝对路径。
- 网页移动在文件系统变更和引用更新间提供补偿回滚。
- 外部移动使引用失效，不按名称、摘要或 inode 猜测新路径。
- Run 保存路径快照，不随当前文件移动。
- 变量重命名更新 QuickRun 与 Schedule；Run 历史不改写。

## 6. 建议索引

- Session(token_hash), Session(expires_at)
- Variable(name)
- QuickRun(sort_order), QuickRun(script_path)
- Schedule(enabled, next_fire_at), Schedule(script_path)
- ScheduleTrigger(schedule_id, scheduled_for), ScheduleTrigger(run_id)
- Run(created_at DESC), Run(status, started_at), Run(script_path_snapshot, status)
- Run(source_type, source_id)
- AuditEvent(occurred_at DESC), AuditEvent(action, occurred_at)
- TrashEntry(original_path), TrashEntry(deleted_at)

## 7. 文件布局

```text
managed-root/
  .git/                       # 可选、受保护且不经网页暴露
  .scriptboard-trash/         # 保留回收站
  ...                         # 管理员文件与脚本

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
  migrations/
    pre-upgrade.db            # 最近一次升级前内部快照
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

受管根目录与状态目录可覆盖，但必须互不包含；`.git/` 与回收站不得成为状态数据库或秘密存储位置。

正式受管服务另有独立的程序布局，不属于 State Root：

```text
install-root/
  install.json
  current -> versions/<version>    # Linux 原子符号链接；Windows 服务配置指向版本目录
  versions/<version>/
    RELEASE.json
    scriptboard[.exe]
    scriptboard-updater[.exe]
    ...                            # 对应平台完整 Release 内容
  scriptboard-updater              # 仅 Linux；切换前原子刷新、供恢复使用的独立 helper
  scriptboard-tray-launcher.exe    # 仅 Windows；稳定托盘入口
```

Update Operation 是文件系统持久化事务，不写入 SQLite 作为事实来源，以便数据库本身被恢复时仍能继续判断更新阶段。终态结果由应用在正常启动后幂等导入审计一次。

## 8. 网站监控

| 实体 | 关键字段与保留 |
| --- | --- |
| WebsiteMonitor | 配置 JSON、范围、协议、管理员顺序、状态、失败计数、配置代次、下一检查时间；删除后保留一年 |
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
