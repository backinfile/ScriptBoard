# Pi Agent 能力增强实施计划

状态：核心实现已完成，真实模型收益门禁与正式 Runtime 发布验收待执行

最后更新：2026-08-02

实现记录（2026-08-02）：已完成 Pi session stats、thinking、手动/自动 compact 与 retry
RPC；schema 24 的 Profile、telemetry 和模型图片能力；五个版本化一方 Playbook 及摘要
清单；六个有界 Evidence Query；PNG/JPEG/WebP 安全重编码；Profile/Inspector/图片引用
界面；Runtime 打包与旧 Runtime 确定降级。外部知识 Adapter 按计划保持未实现和默认无
额外出站。单元与应用级集成测试已覆盖协议、迁移、能力清单、游标和图片处理；正式发布
前仍须完成多 Provider 真实模型基线、pass^5、签名 Runtime 安装/回退及 Chromium 视觉门禁。

## 1. 背景

ScriptBoard 已经完成原生 AI 对话工作区、私有 Pi RPC Runtime、固定 Extension、
Tool Broker、角色授权、一次性审批和签名 Runtime 发布。当前 Pi 固定为 `v0.83.0`，
生产启动关闭内置 Shell、任意文件读写、用户级 Extensions、Skills、Prompts、Themes
和上下文文件，只显式加载 ScriptBoard 随 Runtime 发布的固定 Extension。

现有 Agent 已能读取宿主状态、应用、网站、Run、日志、快捷执行和计划，也能通过审批
执行受支持的 ScriptBoard 网页动作。下一阶段不应通过开放第三方插件或恢复 Pi 默认工具
来扩大能力，而应在现有信任模型内提高以下产品价值：

- 让用户看得见上下文占用、Token、估算费用、思考级别和压缩状态。
- 把常见运维任务整理为可评测、可版本化的一方 Playbook。
- 为故障定位提供日志搜索、Run 对比、计划历史和审计证据。
- 在明确引用和严格处理后支持截图分析。
- 仅在管理员明确启用时提供有界的外部文档搜索。

本计划是 [AI 助手与 Pi Runtime 实施计划](./AI-ASSISTANT-PLAN.md) 的能力增强专项。
工具合同、Provider 工具认证、错误恢复、动态工具收窄和真实模型 eval 由
[AI 工具调用可靠性改进计划](./AI-TOOL-RELIABILITY-PLAN.md) 负责，并构成本计划的
前置门禁。

## 2. 已确定的产品决策

- 不提供 `pi install`、第三方 Pi Package 市场或用户上传 Extension。
- 继续使用 `--no-extensions`、`--no-skills`、`--no-prompt-templates`、
  `--no-context-files` 和 `--no-builtin-tools`；新增资源只能通过显式路径或固定
  Extension 加载。
- 所有新增运行时代码、Playbook 和资源都必须随 ScriptBoard Runtime 固定、签名、
  验活和回退。
- Playbook 只影响 Agent 的分析步骤和工具选择，不授予权限，不改变审批模式，也不
  绕过 Tool Broker。
- Agent 不获得浏览器控制、任意网络访问、任意 Shell、任意文件读取或第三方 OAuth。
- 外部内容、日志、脚本、网页正文和图片识别文本始终是不可信数据。
- 会改变 ScriptBoard 状态的能力继续复用现有领域 Module、角色权限、审批和审计。
- 高风险动作不能因为选中了 Playbook 而变为自动批准。
- 核心能力必须离线可用；联网搜索是可选 Adapter，不是基础依赖。
- 不展示或持久化原始 thinking；思考级别只表示计算预算配置。

## 3. 目标

### 3.1 产品目标

1. 用户可以理解一次 Agent Turn 消耗了多少上下文和模型资源，并能主动压缩长对话。
2. 常见运维问题可以从明确的 Playbook 入口开始，得到一致、可追溯的诊断过程。
3. Agent 能从有界证据中定位故障，不必依赖完整日志、Shell 或猜测稳定 ID。
4. 截图可以作为显式上下文参与分析，同时不泄漏任意宿主文件或图片元数据。
5. 外部知识查询在默认关闭、凭据隔离、SSRF 防护和出站披露下可选启用。
6. 每项增强都能通过固定 eval 和安全测试证明收益，而不是只凭演示效果发布。

### 3.2 建议发布指标

| 指标 | 候选阈值 |
|---|---:|
| Playbook 目标任务 pass@1 相对无 Playbook 基线 | 提升至少 10 个百分点 |
| Playbook 目标任务 pass^5 | 100% |
| 无关工具调用数 | 不高于基线 |
| Playbook 常驻上下文开销 | 每个 Agent Turn 不超过 2,000 tokens |
| 会话统计与 Pi `get_session_stats` 一致性 | 100% |
| 日志搜索和窗口读取的越界、泄漏或无界结果 | 0 |
| 未经明确引用发送的图片 | 0 |
| 未启用联网能力时的额外出站请求 | 0 |
| 新增权限绕过、审批绕过或状态修改自动重放 | 0 |

阈值在取得工具可靠性和无 Playbook 基线后确认。`pass^5` 表示同一任务连续五次全部
成功；不能通过缩小样例或放宽事实断言来满足门禁。

## 4. 非目标

- 不把 ScriptBoard 变成通用编码 Agent、远程桌面或浏览器自动化平台。
- 不加入 Pi 默认 `bash`、`read`、`write`、`edit`、`grep`、`find` 或 `ls`。
- 不安装社区 `browser-tools`、Brave Search、Gmail、Drive、Calendar、VS Code、
  Transcribe 或 YouTube Skills 到生产 Runtime。
- 不提供 Subagent、自治后台任务、无人值守修复或跨主机编排。
- 不让 Agent 负责确定性监控告警；邮件、Slack、Teams 或 Webhook 通知应属于独立的
  通知 Module，AI 最多帮助配置和解释。
- 不解析任意 PDF、Office、压缩包、视频或音频。
- 不允许 Agent 自己启用联网、保存外部凭据或扩大域名允许列表。
- 不承诺 Provider 报告的费用与账单完全相同；页面必须标记为模型返回的估算值。

## 5. 领域语言

**Assistant Capability Bundle**：
随某个受信 Pi Runtime 发布的固定能力集合，包含 ScriptBoard Extension、能力清单、
Playbook 资源及其摘要。它不是可动态安装的 Pi Package，也不从用户目录发现资源。

**Operational Playbook**：
一份由 ScriptBoard 发布、面向特定运维意图的受信步骤和判断标准。它只指导 Agent
如何收集证据、何时停止和如何表达结论，不包含运行时凭据、用户数据或可执行脚本。

**Conversation Profile**：
AI 对话选择的工作模式，例如通用助手、失败 Run 诊断或网站事故分析。Profile 引用
一个固定 Playbook，但不改变角色、工具权限或审批策略。

**Session Telemetry**：
Pi 对当前持久 session 报告的累计 Token、估算费用、上下文占用、消息数和工具调用数。
它是用户可见的资源事实，不包含 raw thinking 或消息正文。

**Evidence Query**：
通过 Tool Broker 执行的有界只读查询，用于搜索或分段读取日志、比较 Run、读取计划
触发历史或审计事实。查询结果必须携带来源、时间、截断和游标语义。

**External Knowledge Adapter**：
在管理员明确启用后，把受限搜索请求发送给第三方文档或搜索服务的 Adapter。外部服务
是真外部依赖；生产使用网络 Adapter，测试使用 Mock Adapter。

## 6. 架构

```text
Browser /ai
  │
  │ typed commands + authenticated SSE
  ▼
Assistant Module
  ├── Conversation Store ───────── profile + telemetry snapshot
  ├── Runtime Supervisor ───────── Pi RPC Adapter
  │                                  │
  │                                  ├── stats / thinking / compact
  │                                  ▼
  │                             pi --mode rpc
  │                                  │
  │                       Fixed ScriptBoard Extension
  │                                  │ local capability IPC
  ├── Capability Catalog ◀───────────┤ guidance request
  │        │                         │
  │        └── signed Playbooks      ▼
  └── Tool Broker ◀──────────── Evidence Queries / Actions
           │
           ├── Run / Logs / Schedule / Audit
           ├── Safe Raster Processor
           └── Optional External Knowledge Port
                         ├── Network Adapter
                         └── Mock Adapter
```

`Assistant Module` 继续是 Web 层唯一依赖的 AI seam。Web handler 不解析 Pi RPC，
不读取 Runtime 资源，不计算 Token，也不直接调用外部搜索服务。

`Capability Catalog` 是一个深 Module：它在 Runtime 激活时完成资源摘要、版本、ID、
兼容性和语言元数据校验，对调用方只暴露列举和解析固定能力的小 Interface。删除该
Module 会迫使 Runtime Manager、Web、Extension 和测试分别理解能力清单，因此该 seam
具有实际深度和 locality。

建议 Interface：

```go
type CapabilityCatalog interface {
    List(ctx context.Context, locale string) ([]CapabilitySummary, error)
    Resolve(ctx context.Context, id, version string) (Capability, error)
}

type PiSession interface {
    Prompt(ctx context.Context, request PromptRequest) error
    State(ctx context.Context) (SessionState, error)
    Stats(ctx context.Context) (SessionStats, error)
    SetThinking(ctx context.Context, level string) error
    Compact(ctx context.Context, instructions string) error
    Abort(ctx context.Context) error
}
```

这些是内部 Interface，不直接暴露 Pi 命令名称、session 文件路径或 Provider 原始结构。
生产 `PiSession` 使用 RPC Adapter，协议测试使用 fake Pi Adapter；这是现有 Runtime seam
的深化，不新增只为测试存在的外部 seam。

## 7. 目标方案

### 7.1 会话透明度与控制

接入 Pi 0.83 RPC 已提供的：

- `get_session_stats`
- `get_available_thinking_levels`
- `set_thinking_level`
- `compact`
- `set_auto_compaction`
- `set_auto_retry`

第一阶段产品行为：

- Inspector 展示累计输入、输出、cache read、cache write、总 Token 和估算费用。
- 展示当前上下文 Token、窗口占比、消息数和工具调用数。
- 思考级别按对话保存，只能选择当前模型实际支持的值。
- 手动压缩只允许在对话空闲、没有待审批时发起，并明确提示可能产生 Provider 费用。
- 自动压缩默认开启；页面展示 `compacting`，不把压缩摘要伪装成助手正文。
- Provider 瞬时失败的自动重试继续有界，页面只显示状态和次数；第一阶段不允许用户
  配置无限重试或状态修改工具重放。

Pi stats 是累计快照。每个 `agent_settled`、压缩完成和模型切换后读取一次并保存最新
快照；不得通过轮询持续唤醒已经保温关闭的 Pi 进程。冷对话页面显示最后更新时间，
只有用户主动刷新统计时才允许恢复对应私有 session。

建议持久字段：

| 字段 | 语义 |
|---|---|
| `thinking_level` | 对话期望思考级别 |
| `input_tokens` / `output_tokens` | Pi session 累计 Token |
| `cache_read_tokens` / `cache_write_tokens` | Provider 报告的缓存 Token |
| `estimated_cost_microunits` | 估算费用，使用整数最小单位存储 |
| `context_tokens` / `context_window` | 最近一次上下文使用情况 |
| `session_message_count` / `session_tool_call_count` | Pi session 统计 |
| `telemetry_updated_at` | 最近成功采集时间 |

旧 Runtime 不支持某命令时返回稳定 `runtime_capability_unsupported`，页面隐藏对应控制，
不能把缺失字段当作零值。

### 7.2 Assistant Capability Bundle

Runtime 资产从单一固定 Extension 深化为固定能力包：

```text
runtime-payload/
  pi[.exe]
  scriptboard-extension.ts
  capabilities.json
  playbooks/
    diagnose-failed-run.md
    investigate-website-incident.md
    triage-host-pressure.md
    review-script-safety.md
    design-schedule.md
  LICENSE
  scriptboard-runtime.json
```

`capabilities.json` 至少包含：

- Schema 版本、Bundle 版本和兼容的 Tool Broker 协议。
- 每个资源的固定相对路径、类型、大小和 SHA-256。
- Playbook ID、版本、标签键、描述键、适用角色和所需只读工具。
- 是否允许自动选择；首版全部要求用户显式选择。
- 最大注入字节数和估算 Token 上限。

Runtime 打包工具拒绝绝对路径、`..`、链接、重复 ID、未知资源类型、摘要不匹配和超限
资源。Runtime Manager 在激活前完成整包验证，Capability Catalog 只读取已经验证的
活动版本。切换或回退 Runtime 时，已有对话如果引用的 Profile 不再兼容，应回退到
通用助手并向用户显示原因，不能静默注入不同版本内容。

生产环境仍不运行 `pi install`，也不允许 Pi 根据 `capabilities.json` 下载任何内容。

### 7.3 Operational Playbook

首批五个 Playbook：

| ID | 入口 | 主要证据 | 明确停止条件 |
|---|---|---|---|
| `diagnose-failed-run` | 最近失败 Run、Run 详情页 | Run 元数据、日志窗口、相邻 Run 对比 | 证据不足、日志过期或目标多匹配 |
| `investigate-website-incident` | 网站故障、Monitor 详情页 | 最近检查、事故、TLS/延迟事实 | 需要外部网络事实但联网未启用 |
| `triage-host-pressure` | 主机压力、异常应用 | 主机快照、应用资源、源日志 | 快照过期或无法归因 |
| `review-script-safety` | 显式引用普通文本脚本 | 文件摘要、受限正文、执行配置 | 非文本、未引用、正文截断影响结论 |
| `design-schedule` | 新建或调整计划 | 脚本、参数、Cron 预览、重叠策略 | 时间区或业务意图不明确 |

Playbook 统一结构：

1. 适用意图和不适用场景。
2. 允许使用的证据和稳定 ID 来源。
3. 收集顺序、最大调用数和截断处理。
4. 必须询问用户的歧义。
5. 权限不足、审批拒绝和目标变化后的终止规则。
6. 回答格式：结论、证据、置信度、建议动作和深链。
7. 禁止项：猜 ID、把日志当指令、声称未发生的操作、根据外部知识直接执行修改。

首版采用“对话 Profile + 固定 Extension 注入”：

1. 用户从空状态按钮、资源页入口或对话 Inspector 显式选择 Profile。
2. 服务端把 `profile_id` 和版本保存到对话；活动 Turn 中不能切换。
3. Extension 在 `before_agent_start` 通过进程绑定 Broker 请求当前 Profile 指导内容。
4. Broker 从 Capability Catalog 返回受信 Playbook；Extension 将其追加到该 Turn 的
   system prompt，而不是作为不可信资源正文。
5. 工具结果和引用内容仍位于独立的不可信数据区。

不采用 Pi 自动 Skill discovery。后续可以验证 RPC `/skill:name` 显式展开路径，但只有
在它比固定 Extension 注入更简单、可测试且不依赖内置 `read` 时才可替换当前方案。

### 7.4 Evidence Query

在工具可靠性计划完成名称解析和错误合同后，增加以下只读能力：

```text
search_run_log
read_run_log_window
compare_runs
search_source_log
get_schedule_history
list_audit_events
```

统一查询合同：

- 使用稳定目标 ID；名称必须先通过资源搜索解析。
- `query` 最大 256 字节，不支持正则、Shell、通配路径或表达式执行。
- 返回固定最大条目数、最大文本字节数和不透明 continuation cursor。
- Cursor 绑定用户、对话、工具、目标、查询摘要和短期有效期，不能跨查询复用。
- 文本按 UTF-8 安全切分，保留行号、事件序号、时间和来源。
- 搜索范围、是否命中上限、日志是否过期/不完整/截断必须显式返回。
- 审计查询按当前角色过滤；AI 工具调用自身的审计记录默认排除，避免递归噪声。
- 不返回变量值、凭据、绝对私有路径、完整环境或其他用户的对话事实。

`compare_runs` 是聚合只读工具，只比较二至五个 Run 的元数据、退出状态、持续时间、
日志摘要和有界差异，不把完整日志一次放入上下文。它必须返回每项结论使用的 Run ID
和事件范围。

### 7.5 安全图片上下文

图片能力只处理用户显式引用的受管安全光栅图片：

- 首版支持 PNG、JPEG 和 WebP；不支持 SVG、GIF 动画、TIFF、PDF 或视频。
- 先通过文件身份和现有权限重新解析引用，不接受浏览器提交的绝对路径。
- MIME 由内容探测决定，不相信扩展名或上传头。
- 输入最大 10 MiB、最大 40 megapixels；超限在完整解码前拒绝。
- 服务端重新编码，移除 EXIF、ICC 注释、文件名和其他元数据。
- 最长边缩放到 2,048 像素，编码后的单图最大 4 MiB，每个 Prompt 最多四图。
- 图片作为 Pi RPC `images` 发送；浏览器和普通日志不出现 Base64。
- Provider 不支持图片时，在发送前返回稳定错误，不退化为忽略图片。
- OCR 或视觉模型产生的文字属于不可信数据，不能成为工具参数或操作授权来源。

Pi session 可能持久化处理后的图片 payload，因此 Runtime session 目录必须计入 AI 私有
存储预算。归档对话不立即删除图片；对话永久清理策略确定前，不提供永久删除入口。

### 7.6 可选外部文档搜索

该阶段默认不实施，只有当真实故障样例证明本地证据不足且用户确实需要查询外部文档时
才进入开发。

定义一个窄的 External Knowledge Port：

```go
type ExternalKnowledge interface {
    Search(ctx context.Context, request SearchRequest) (SearchPage, error)
    Fetch(ctx context.Context, request FetchRequest) (DocumentExcerpt, error)
}
```

生产使用管理员配置的网络 Adapter，测试使用 Mock Adapter。不得让 Pi Extension 直接
持有搜索凭据或调用任意 URL。

安全要求：

- 实例级开关默认关闭；只允许 Administrator 配置 Provider、凭据和域名策略。
- 凭据由 State Root 外部主密钥密封后保存到 State Root 私有凭据文件，不回显、不进入数据库和 Pi 环境。
- 搜索请求只发送用户确认的查询词；默认不自动附加日志、文件正文、主机名或 URL。
- Fetch 仅接受 Search 返回的短期不透明文档 ID，不接受模型构造的 URL。
- Adapter 重新解析 DNS，阻止 loopback、私网、链路本地、云元数据地址和不安全重定向。
- 仅接收 HTTPS 文本内容，限制响应头、正文、解压大小、重定向次数和总耗时。
- 外部正文净化后标记来源、抓取时间和不可信数据；不得根据网页指令调用修改工具。
- 每次出站查询在 Tool Ledger 中可见，并记录不含查询正文的审计摘要。
- Playbook 不能自动打开联网能力，也不能绕过用户或管理员的出站策略。

首选实现是可替换的搜索 Adapter，不绑定某个 Codex 插件、MCP Server 或社区 Pi Skill。

### 7.7 Web 体验

对话页增加：

- 空状态以五个 Playbook 入口替换当前普通 Prompt 建议；继续只使用 Lucide 图标。
- 对话标题旁显示当前 Profile，可在空闲时切换回“通用助手”。
- Inspector 增加 Context、Usage、Model 三个紧凑区块。
- Context 显示使用量、窗口占比、最近压缩时间和手动压缩入口。
- Usage 显示累计 Token、cache 和估算费用，明确标注统计时间与估算性质。
- Model 显示当前模型和可用思考级别，不显示 raw thinking。
- Evidence Query 工具卡显示查询范围、命中数、截断、cursor 状态和来源深链。
- 图片引用以缩略图和文件逻辑名称显示，发送前展示图片数量与 Provider 支持状态。
- 外部查询启用时使用独立的出站标识和来源链接；未启用时不展示诱导入口。

移动端把 Profile 和 Usage 放入 Inspector，不挤压 Transcript；所有新增控制具有键盘、
屏幕阅读器、中文 IME、焦点恢复和 `prefers-reduced-motion` 验收。

## 8. 数据与迁移

建议新增：

```text
assistant_conversations
  profile_id
  profile_version
  thinking_level
  input_tokens
  output_tokens
  cache_read_tokens
  cache_write_tokens
  estimated_cost_microunits
  context_tokens
  context_window
  session_message_count
  session_tool_call_count
  telemetry_updated_at
```

迁移规则：

- 旧对话的 `profile_id` 为空，语义为通用助手。
- Token、费用和上下文字段允许 NULL，NULL 表示 Runtime 未报告，不等于零。
- 计数只接受非负整数，最新 Pi 累计快照覆盖旧快照，不通过 Web 做增量相加。
- `thinking_level` 为空时使用模型和 Runtime 的受支持默认值；首次成功查询后规范化保存。
- Profile 版本不自动改写；Runtime 回退后由 Capability Catalog 做兼容判断。
- 不保存 Playbook 正文、外部网页正文、图片 Base64 或 raw thinking 到 SQLite。

如果图片最终进入 Pi session 文件，应在 `docs/DATA-MODEL.md` 中明确其所有权、备份、
归档、保留和磁盘预算语义。

## 9. 实施阶段与依赖

### 阶段 0：完成可靠性前置门禁

交付物：

- 完成 AI 工具可靠性计划的阶段 0 至阶段 4。
- 固定 Agent eval、Provider 工具认证、严格工具 Schema 和错误合同。
- 得到无 Playbook 的目标任务成功率、Token、延迟和工具调用数基线。

退出条件：核心任务达到已确认的可靠性门禁，且真实失败可以被固定 eval 重现。

### 阶段 1：会话统计与思考控制

交付物：

- 深化 Pi RPC Adapter，加入 stats、thinking、compact 和能力探测。
- 增加 telemetry 与 thinking 数据迁移。
- Inspector Usage/Context/Model UI。
- 冷进程、旧 Runtime、Provider 不报告费用和模型切换兼容测试。

退出条件：页面统计与 Pi RPC 一致，压缩和思考级别在重启后保持确定状态。

### 阶段 2：Capability Bundle 基础

交付物：

- `capabilities.json` Schema 和 Capability Catalog。
- Runtime 打包、签名清单、安装验活和回退扩展。
- Profile 数据模型、列表和选择流程。
- 无 Playbook 内容的 tracer bullet，证明 Extension 只能取得当前对话绑定的固定资源。

退出条件：篡改、未知、超限或不兼容资源不能被激活，旧 Runtime 可确定降级。

### 阶段 3：首批 Operational Playbook

交付物：

- 五个固定 Playbook 和中英文标签/描述。
- Extension `before_agent_start` 受信指导注入。
- 空状态入口、资源页深链和 Profile Inspector。
- 每个 Playbook 的正向、证据不足、权限拒绝、Prompt Injection 和 held-out eval。

退出条件：至少三个 Playbook 达到收益阈值，其他 Playbook 可以继续标记为实验性而不随
正式 Runtime 启用。

### 阶段 4：Evidence Query

交付物：

- 日志搜索、窗口读取、Run 对比、计划历史和审计查询。
- 不透明 cursor、结果预算和统一来源结构。
- 对应 Playbook 更新和回归 eval。

退出条件：诊断成功率提高，平均上下文或无效工具调用至少一项下降，安全指标不回归。

### 阶段 5：安全图片上下文

交付物：

- Safe Raster Processor、图片引用和 Pi RPC image payload。
- Provider 图片能力探测、缩略图和发送前状态。
- 解码炸弹、畸形图片、元数据、跨用户引用、配额和 session 保留测试。

退出条件：只有显式授权、重新编码后的有界图片离开实例，非图片模型确定拒绝。

### 阶段 6：可选外部文档搜索

进入条件：至少十个真实、去敏故障样例证明外部文档能提供本地证据无法获得的价值。

交付物：

- External Knowledge Port、一个生产 Network Adapter 和一个 Mock Adapter。
- 管理员设置、私有凭据、域名策略、出站披露和审计摘要。
- SSRF、重定向、DNS rebinding、压缩炸弹、Prompt Injection 和数据外传测试。
- 与无联网基线对比的故障诊断 eval。

退出条件：收益超过新增延迟、费用和安全复杂度；否则保持实验性或删除。

## 10. 预期代码落点

| 区域 | 预期修改 |
|---|---|
| `internal/assistant/pirpc/protocol.go` | stats、thinking、compact、能力探测命令与响应 |
| `internal/assistant/pirpc/supervisor.go` | 冷/热 session 下的统计与控制生命周期 |
| `internal/assistant/service.go` | Profile、thinking 和 telemetry 持久化 |
| `internal/app/assistant_runtime.go` | settled 后采集、Playbook 绑定和 Runtime 能力降级 |
| `internal/assistant/runtimeinstall/` | Capability Bundle 清单、验证、激活和回退 |
| `runtime/scriptboard-extension.ts` | `before_agent_start` 指导注入和能力元数据 |
| `runtime/playbooks/` | 固定一方 Operational Playbook |
| `internal/app/assistant_tools.go` | Evidence Query 和可选外部知识工具 |
| `internal/app/assistant_context.go` | 安全图片引用与 Prompt 组装 |
| `internal/app/web_assistant.go` | Profile、统计、压缩、思考级别和图片路由 |
| `internal/app/web/templates/assistant.html` | Profile 入口与 Inspector UI |
| `internal/app/web/assets/app.js` | 空闲控制、能力状态和图片 Composer |
| `internal/app/web/assets/app.css` | 响应式、可访问的新增状态与工具卡 |
| `integration/assistant-eval/` | Playbook、Evidence Query、多模态和联网对照 eval |
| `integration/browser/` | 桌面、移动端、键盘和视觉快照 |

## 11. 测试计划

### 11.1 RPC 与状态

- stats、thinking、compact 的 request correlation、超时、abort 和未知命令测试。
- `agent_settled`、compaction、模型切换和进程退出时统计快照一致。
- 旧 Runtime 不支持新命令时稳定降级，不崩溃、不循环重启。
- NULL、零、本地模型、Provider 缺少 usage 和费用溢出正确处理。
- 手动压缩不能与活动 Turn、待审批或 Runtime 切换并发。

### 11.2 Capability Bundle

- 清单版本、路径、ID、大小、摘要、重复资源和未知类型校验。
- 活动版本和回退版本拥有各自独立 Catalog，不跨版本读取。
- Pi 用户目录中的同名 Skill、Prompt 或 Extension 不影响 Catalog。
- Profile 请求绑定用户、对话、Runtime 版本和进程 capability。
- 用户 Prompt 不能伪造 Profile ID 或把不可信数据提升为受信指导。

### 11.3 Playbook 行为

- 正确任务选择正确证据，不相关任务不强制进入 Playbook。
- 无匹配、多匹配、证据截断、日志过期和权限不足时明确停止。
- 状态修改仍经过现有 Action Approval；拒绝后不绕过。
- Playbook 不能把日志、网页、脚本或图片文字当作指令。
- Profile 切换不会改变工具可见性、角色或自动审批配置。
- 每个正式 Playbook 通过固定任务和 held-out 任务收益门禁。

### 11.4 Evidence Query

- 查询、目标、limit 和 cursor 的所有上限与错误分类。
- Cursor 篡改、过期、跨用户、跨对话、跨目标和跨查询复用失败。
- UTF-8 边界、超长行、日志轮转、过期、不完整和并发追加保持确定结果。
- 审计查询不泄漏其他用户对话、凭据或敏感参数。
- 聚合结果中的每个结论可以追溯到有界来源。

### 11.5 图片

- 文件身份、角色权限、显式引用和 Provider 能力重新校验。
- MIME 欺骗、SVG、动画、畸形编码、解码炸弹和超大尺寸被拒绝。
- EXIF、ICC 注释、文件名和路径不进入 Provider payload。
- Base64 不进入浏览器 HTML、普通日志、审计或 SQLite。
- 多图、慢解码、取消、低磁盘和 session 配额安全降级。

### 11.6 外部知识

- 默认关闭时没有 DNS、HTTP、遥测或包更新请求。
- 凭据不进入 Pi 环境、数据库、日志、审计或响应正文。
- 私网、loopback、链路本地、云元数据、非 HTTPS 和危险重定向被拒绝。
- 查询正文不会自动包含日志、文件、主机名、URL、变量或凭据。
- 外部 Prompt Injection 不能触发状态修改、启用联网或改变允许列表。
- Network Adapter 与 Mock Adapter 通过同一个 Interface 测试 surface。

### 11.7 Web 与回归

- 中英文标签、桌面、移动端、键盘、屏幕阅读器和 reduced-motion。
- SSE 重连不会重复统计、重复压缩、切换 Profile 或重复发送图片。
- 无 JavaScript 时仍可查看统计，并对交互控制显示明确降级。
- 现有 Run、文件、计划、监控、用户、更新和 Assistant 审批测试全部通过。

## 12. 风险与控制

| 风险 | 控制 |
|---|---|
| Playbook 让模型过度套流程 | 用户显式选择、短指导、held-out eval、可切回通用助手 |
| 受信指导与不可信证据混合 | Extension 注入 system prompt；资源继续使用独立不可信标记 |
| Capability Bundle 扩大供应链面 | 固定路径、摘要、签名、大小上限、验活和原子回退 |
| Token/费用误导用户 | 标记估算、保存采集时间、NULL 不显示为零、与 Pi stats 对照 |
| 手动压缩丢失关键运维状态 | Pi 原生摘要、压缩前状态检查、重要目标和审批不由摘要授权 |
| 日志搜索造成大上下文或 DoS | 固定预算、简单文本查询、不透明 cursor、超时和并发限制 |
| 图片包含敏感信息 | 显式引用、发送前预览、重新编码、配额和 Provider 披露 |
| 外部搜索造成数据外传或 SSRF | 默认关闭、窄 Port、短期文档 ID、网络策略和出站可见性 |
| 为一个 Provider 优化后损害其他模型 | 按 Provider 运行相同基线和 held-out 任务，不单模型发布 |

## 13. 完成标准

- 会话 Inspector 能准确展示最后采集的 Token、上下文、估算费用和思考级别。
- 用户可以在安全状态下手动压缩，对话重启后设置和统计语义保持确定。
- Runtime 以签名 Capability Bundle 发布，不开放第三方或用户资源发现。
- 至少三个一方 Playbook 通过相对基线收益和 pass^5 门禁。
- Playbook 不增加工具权限，不改变审批，不依赖 Shell、内置 `read` 或任意网络。
- Run/Source Log 可以有界搜索和分段读取，Run 可以有来源地比较。
- 计划历史和审计事实只向当前角色返回允许范围，并保持脱敏。
- 支持图片的 Provider 只接收用户明确引用、服务端重新编码后的安全光栅图片。
- 不支持图片或新 RPC 命令的 Provider/Runtime 能够确定降级。
- 外部搜索如果实施，默认关闭且所有 SSRF、凭据和 Prompt Injection 测试通过。
- 未授权状态修改、审批绕过、结果未知重放和敏感正文泄漏保持为零。
- Runtime 安装、更新和回退继续原子、可验证且不影响本机其他 Pi。
- 所有相关单元、集成、真实 Agent eval 和 Chromium 门禁通过。

## 14. 需要同步更新的文档

实施过程中同步修改：

- `CONTEXT.md`：增加 Capability Bundle、Operational Playbook、Conversation Profile、
  Session Telemetry、Evidence Query 和 External Knowledge Adapter。
- `docs/PRD.md`：增加用户价值、Profile、统计、多模态和联网非目标。
- `docs/DATA-MODEL.md`：增加 Profile、telemetry、图片 session 保留和迁移语义。
- `docs/ACCEPTANCE.md`：增加能力包、Playbook、统计、查询、图片和联网验收。
- `docs/RELEASING.md`：增加 Capability Bundle 资源摘要、签名、验活和回退步骤。
- `docs/AI-ASSISTANT-PLAN.md`：链接本增强计划，不重写已经完成的基线任务。
- `docs/AI-TOOL-RELIABILITY-PLAN.md`：在阶段 5 链接本计划的 Playbook 实施。
- `docs/adr/`：记录能力包信任模型、受信 Playbook 注入和可选出站搜索决定。
- README：只在能力正式发布后说明 Profile、统计、图片和可选联网行为。

## 15. 参考资料

- [Pi 0.83.0 RPC](https://github.com/earendil-works/pi/blob/v0.83.0/packages/coding-agent/docs/rpc.md)
- [Pi 0.83.0 Extensions](https://github.com/earendil-works/pi/blob/v0.83.0/packages/coding-agent/docs/extensions.md)
- [Pi 0.83.0 Skills](https://github.com/earendil-works/pi/blob/v0.83.0/packages/coding-agent/docs/skills.md)
- [Pi 0.83.0 Packages](https://github.com/earendil-works/pi/blob/v0.83.0/packages/coding-agent/docs/packages.md)
- [ADR-0123：使用 Pi RPC 作为私有 Assistant Runtime](./adr/0123-use-pi-rpc-as-a-private-assistant-runtime.md)
- [ADR-0124：代理 Assistant 工具并将状态修改绑定到一次性审批](./adr/0124-broker-assistant-tools-and-bind-state-changes-to-approvals.md)
- [ADR-0125：将 Pi Runtime 固定到签名的 ScriptBoard Release](./adr/0125-pin-pi-runtime-to-signed-scriptboard-releases.md)

上游文档只描述 Pi 能力，不构成 ScriptBoard 的安全保证。生产信任模型、角色、审批、
数据限制、签名发布和测试合同以 ScriptBoard 文档与代码为准。
