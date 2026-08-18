# ScriptBoard

ScriptBoard 是面向单机、少量可信用户场景的主机文件与脚本操作台。它通过受限领域边界访问主机文件、执行可信脚本并追踪结果；它不是通用运维编排平台或不可信代码沙箱。

## Code architecture

进程入口遵循 `cmd -> bootstrap -> web / domain -> store / leaf packages`。`internal/bootstrap` 是 Web、Broker、Runner 与 AI Host 的唯一运行时组合根；`internal/web` 只拥有 HTTP、路由和 UI 适配；SQLite 生命周期与迁移由 `internal/store` 统一管理。身份授权、快捷执行规则、可恢复文件操作与审计保留分别位于 `internal/identity`、`internal/quickrun`、`internal/fileworkflow` 和 `internal/audit`。旧 `internal/app` 已删除，架构测试禁止其恢复。

## Language

**Kubernetes 连接（Kubernetes Connection）**：
ScriptBoard 可访问的一个 Kubernetes 集群配置，拥有稳定 ID、显示名称、主机 kubeconfig 路径、可选 context 和操作模式。快照、指标、版本历史及有限操作都在连接 ID 范围内隔离；连接不是集群生命周期或跨集群编排对象。
_Avoid_: 集群资源、集群成员、多集群控制面

**用户（User）**：
由系统管理员手动创建、可以登录当前 ScriptBoard 实例的本地身份。用户拥有稳定 ID、唯一用户名、固定角色、启用状态和授权版本；账号不永久删除，以保留 Run 与审计归属。
_Avoid_: 项目成员、操作系统账号、执行身份

**固定角色（Fixed Role）**：
用户在整个实例范围内拥有的四种互斥权限之一：系统管理员、维护员、执行员或观察员。角色不是可编辑权限集合；快捷执行分组和计划分组只组织页面，不参与授权。
_Avoid_: 用户组、自定义角色、分组授权、权限开关

**系统管理员（Administrator）**：
实例中唯一、始终启用且不能降级的用户。系统管理员拥有全部能力，并且是唯一可以创建、改名、停用、恢复、改角色或重置其他用户密码的角色。
_Avoid_: root、运行身份、普通管理员组

**维护员（Maintainer）**：
除用户管理和 Docker Engine 配置外拥有全部 Web 能力的固定角色，包括主机文件与执行配置变更、任意 Run 停止、审计和更新。
_Avoid_: 第二管理员、自定义权限

**执行员（Operator）**：
可以观察运行状态、读取服务身份可读的普通主机文件、执行主机脚本和全部快捷执行项，并且只能停止自己启动的 Run；不能修改文件、配置、变量、计划或运行一次性源码。
_Avoid_: 受限维护员、分组执行员

**观察员（Viewer）**：
只能查看监控、应用、网站、快捷执行、计划、Run 历史与原始 Run 日志，不能访问文件页、变量、审计、用户或系统设置，也不能发起或停止执行。
_Avoid_: 匿名用户、只读文件用户

**启动用户（Run Initiator）**：
发起某个 Run 的用户稳定 ID 与当时用户名快照。系统计划触发没有普通用户启动者；启动用户与实际操作系统运行身份严格分离。
_Avoid_: 运行身份、触发来源、当前用户名

**脚本（Script）**：
主机文件系统中可由宿主能力直接执行、且不位于受保护路径的普通文件，无需注册即可被有权用户启动。
_Avoid_: 注册任务、作业、Action

**受信脚本（Trusted Script）**：
管理员明确发布并交由 ScriptBoard 执行、被视为可信业务代码的脚本。它仍受独立 Runner 的身份、环境、资源与网络边界约束，但这些边界不构成不可信代码沙箱。
_Avoid_: 沙箱脚本、安全脚本

**运行身份（Runtime Identity）**：
操作系统为某个 ScriptBoard 组件配置的独立账号或服务 SID；Run 的运行身份始终是 Runner 身份，不是启动用户、Web 身份或应用角色。
_Avoid_: 权限模式、启动用户、应用角色

**Web 控制面（Web Control Plane）**：
处理 HTTP、身份、授权与页面状态的低权限组件。它协调领域操作，但不拥有主机特权、脚本执行身份或 AI Runtime 能力。
_Avoid_: 后台单体、执行服务、特权服务

**特权操作 Broker（Privileged Broker）**：
通过固定领域操作持有主机修改能力、Broker-owned 秘密，以及受管部署的 Docker/Kubernetes 宿主访问能力的受信组件。它不是任意命令、Shell、Docker/Kubernetes 请求透传或通用提权代理。
_Avoid_: root Web、命令代理、通用 Agent

**Runner**：
在独立运行身份中复核并执行已绑定脚本与作业描述的组件。它不继承 Web 凭据，也不拥有 Broker 秘密或 AI 工作区。
_Avoid_: Web 子进程、脚本沙箱、启动用户

**AI Host**：
在独立运行身份中承载受管 AI Runtime 的组件。它只持有会话绑定的短期能力，不拥有 Web 会话或 Broker 秘密。
_Avoid_: Web 子进程、Tool Broker、模型提供商

**触发来源（Run Source）**：
发起一次执行的入口类型，区分手动、快捷执行和内置调度器，不等同于启动用户或进程的运行身份。
_Avoid_: 启动用户、运行账号、创建人

**宿主能力（Host Capability）**：
当前 ScriptBoard 实例所在主机实际可用的脚本解释器集合；实例只承诺执行其宿主能力覆盖的脚本类型。
_Avoid_: 全平台脚本支持

**执行器链（Executor Chain）**：
某类脚本按优先级排列的一组候选执行器。只有候选不可用或无法创建进程时才回退；任一候选成功启动脚本后，本次选择即告结束。
_Avoid_: 失败重试、脚本重跑

**主机文件系统（Host Filesystem）**：
ScriptBoard 的特权操作 Broker 身份可访问的本机文件系统集合。Windows 顶层是可用的本地卷、移动卷和网络卷，Linux 顶层是 `/`；最终可访问性始终由 Broker 的操作系统权限决定。
_Avoid_: 受管根目录、文件库、应用沙箱

**内部状态目录（State Root）**：
仅供 ScriptBoard 保存数据库、密钥、执行日志、文件操作日志和暂存数据的私有目录。它是受保护路径，不属于用户可通过文件页面查看或管理的内容。
_Avoid_: 主机文件系统、共享目录、文件库

**私有状态备份（Private State Backup）**：
由 ScriptBoard 从一致性 SQLite snapshot 和固定私有状态白名单生成的认证加密恢复包。它携带逐文件摘要与签名审计 checkpoint，但不包含外部主密钥、checkpoint 签名私钥、启动配置、TLS 材料、诊断日志、上传 inbox 或 MySQL 备份；因此不是 State Root 的目录副本，也不是完整主机灾备。
_Avoid_: State Root 副本、配置导出、MySQL 备份、整机镜像

**AI 对话（Assistant Conversation）**：
由一个 ScriptBoard 用户拥有、绑定一个必选 LLM 配置并保存消息、资源引用、审批模式和 Pi session 身份的持久对话。用户只能查看、订阅和修改自己的 AI 对话；归档不删除历史或 session。
_Avoid_: 全局聊天室、Pi 终端会话、共享提示词

**LLM 配置（LLM Configuration）**：
由一个 ScriptBoard 用户拥有的模型端点与服务端凭据配置。端点可显式使用 HTTP 或 HTTPS；HTTP 是明文传输选择，不改变凭据仍只保存在服务端私有文件的边界。配置默认私有；所有者可以显式公开，允许其他用户在自己的 AI 对话中选择和测试，但其他用户不能编辑、设为默认或删除该配置。每个用户的默认配置彼此独立。
_Avoid_: 全局模型列表、共享默认模型、客户端 API Key

**Agent Turn**：
从一条用户消息被持久接受，到对应助手消息完成、中断或失败的一次处理。一个 AI 对话同一时间最多有一个活动 Agent Turn，服务重启不会自动重放未完成 Turn。
_Avoid_: Run、消息、后台队列

**Pi Runtime**：
由 ScriptBoard 明确解析和启动、位于 State Root 私有版本目录的 Pi Agent 可执行程序及固定扩展。它不使用 PATH 中的全局 Pi，也不共享用户级 Pi 配置、会话、扩展、Skill 或工作目录。
_Avoid_: Installed Release、系统 Node.js、用户安装的 Pi

**Assistant Capability Bundle**：
随受信 Pi Runtime 一起发布并由摘要清单固定的能力集合，包含 ScriptBoard Extension 和 Operational Playbook。它不从用户目录或项目目录发现资源，也不是可动态安装的第三方 Pi Package。
_Avoid_: 插件市场、用户 Skill、动态提示词目录

**Operational Playbook**：
面向一种明确运维意图的版本化受信指导，例如失败 Run 诊断或网站事故调查。Playbook 只约束证据收集和结论表达，不授予权限、不改变审批，也不是可执行脚本。
_Avoid_: 自动化脚本、永久系统提示、权限模板

**Conversation Profile**：
AI 对话显式选择的工作模式；通用模式不加载 Playbook，其余模式只引用当前 Runtime Capability Bundle 中版本匹配的一个 Operational Playbook。
_Avoid_: 角色、模型、工具权限

**Session Telemetry**：
Pi 为持久 session 报告的累计输入/输出/缓存 Token、估算费用、上下文占用、消息数和工具调用数。它不包含原始 thinking、提示正文或凭据。
_Avoid_: 账单、思维链、审计事件

**Evidence Query**：
通过 Tool Broker 执行的有界只读查询，用于搜索或分段读取日志、比较 Run、读取计划触发历史或审计事实。结果携带来源、截断状态和绑定当前用户、对话、工具及查询的不透明短期游标。
_Avoid_: 任意 SQL、完整日志导出、永久书签

**Safe Raster Processor**：
对用户明确引用的 PNG、JPEG 或 WebP 进行类型探测、尺寸限制、缩放、重新编码和元数据移除的进程内边界。只有已配置且由 Pi 确认支持图片输入的模型才能接收处理后的图片。
_Avoid_: 任意附件解析、原文件直传、OCR 存储

**工具调用（Tool Invocation）**：
Pi 在一个 Agent Turn 内请求 ScriptBoard 执行某个版本化工具及固定参数的记录。它不是 Run，也不能直接访问数据库、任意主机文件、Shell 或内部 Go 对象。
_Avoid_: 脚本执行、RPC 消息、审批

**操作审批（Action Approval）**：
将单次状态修改工具调用绑定到参数摘要、目标状态、对话所有者和授权快照的有时限一次性决定。自动审批只自动产生同样受约束的批准，不放宽角色权限或领域校验。
_Avoid_: 永久授权、角色权限、确认弹窗状态

**工具代理（Tool Broker）**：
ScriptBoard 在 Pi 与现有领域模块之间提供的版本化本地能力边界。它负责 capability、实时授权、参数限制、结果脱敏、审批和审计；没有已发布 Broker/Extension 时 Pi 必须以无工具模式运行。
_Avoid_: 插件系统、公共 API、Pi 内置工具

**文件快捷访问固定项（File Quick Access Pin）**：
当前 ScriptBoard 实例中保存、供所有有文件页权限的用户共用的常用主机目录入口。它只保存规范绝对路径、平台比较键、显示名称与顺序，不复制目录内容；路径暂时不可访问时保留固定项但禁用打开。固定项不依赖浏览器本地存储。
_Avoid_: 浏览器书签、快捷执行、操作系统快捷方式

**主机条目（Host Entry）**：
主机文件系统中可由文件页展示的普通文件、目录或受限条目。普通文件不必先注册为脚本，也能在角色与保护策略允许时被上传、下载、移动、编辑或删除。
_Avoid_: 附件、资源、数据库文件记录

**受保护路径（Protected Path）**：
ScriptBoard 必须从文件页和执行入口隔离的应用关键位置，包括 State Root、Install Root、活动配置、管理员密码文件、TLS 私钥和各文件系统的回收区。受保护路径自身及后代不显示且拒绝直接访问；任何包含受保护路径的祖先也不能被编辑、移动、删除、覆盖或执行。
_Avoid_: 隐藏文件、只读文件、操作系统权限

**受限链接（Restricted Link）**：
主机文件系统中的符号链接、Junction 或其他重解析点。它可被看见，但不能被跟随、进入、读取、下载、覆盖或执行。
_Avoid_: 主机条目、快捷方式

**受限条目（Restricted Entry）**：
可以显示但不能进入、读取、修改或执行的主机条目，包括链接、Junction、重解析点、特殊文件，以及 Linux `/proc`、`/sys`、`/dev` 等内核虚拟文件系统入口。真实磁盘、普通挂载、网络挂载和临时文件系统不因跨卷本身受限。
_Avoid_: 受保护路径、权限拒绝、普通挂载

**文件系统回收区（Filesystem Trash）**：
ScriptBoard 在删除目标所在文件系统中建立、带实例所有权标记的私有回收区。网页删除只在能够安全创建并确认该回收区时执行，不会降级为永久删除；回收条目保留原绝对路径与删除时间，可恢复或永久清理。
_Avoid_: 操作系统回收站、备份、State Root

**执行（Run）**：
脚本在一组确定参数下的一次执行实例，拥有独立的状态、时间、输出及结果。
_Avoid_: 任务、Job、Execution

**断开（Disconnected）**：
ScriptBoard 服务重启后，之前的活动执行已失去可靠监督关系的状态。已有日志仍可读取，但系统不再声称掌握进程结果或具备停止控制。
_Avoid_: 失败、离线、已取消

**执行日志（Run Log）**：
某次执行产生的追加式输出事件序列，每条事件拥有稳定序号并标明时间及 stdout 或 stderr 来源。
_Avoid_: stdout 文件、stderr 文件、终端会话

**源日志（Source Log）**：
由本机 Docker Engine 或普通主机文本文件按需读取的输出。ScriptBoard 只在认证页面的有界工作集中展示，不把源日志写入数据库、审计、执行日志或服务诊断日志。
_Avoid_: 执行日志、日志副本、日志采集库

**日志游标（Log Cursor）**：
绑定日志源身份与读取边界的版本化不透明令牌，用于向前分页和 SSE 断线续传。文件游标绑定平台文件身份与字节偏移；Docker 游标绑定容器 ID、时间戳、输出来源和边界摘要，不能跨源复用。
_Avoid_: 行号、数据库主键、永久书签

**审计事件（Audit Event）**：
对认证、用户生命周期和高影响操作所作的不可逐条修改记录，包含时间、动作、目标、结果，以及操作者稳定 ID、用户名和角色快照，但不包含密码、Cookie、会话、变量值、请求正文或文件内容。
_Avoid_: 执行日志、应用日志、历史记录

**快捷执行项（Quick Run）**：
维护员从主机脚本文件、一次历史执行或已有快捷执行项中主动保存的具名入口，引用脚本的规范绝对路径、平台比较键、参数模板和超时，用于再次发起同样配置的执行。名称、参数与超时可编辑；软锁只防止误编辑和误删除，不阻止执行、复制、移动分组或系统维护引用。
_Avoid_: 收藏脚本、历史记录、定时任务

**快捷执行分组（Quick Run Group）**：
维护员定义并排序的快捷执行项容器。快捷执行项至多属于一个分组，未选择分组的条目统一显示在派生的“未分组”区域；删除分组只移除容器并保留其中条目。分组只用于页面组织，不参与用户授权。
_Avoid_: 文件夹、标签、执行队列

**快捷执行软锁（Quick Run Soft Lock）**：
快捷执行项上的持久化操作保护，锁定时拒绝编辑和删除，但不改变权限、执行语义或引用关系。
_Avoid_: 权限锁、进程锁、文件锁

**计划项（Schedule）**：
由 ScriptBoard 保存和调度的定时脚本执行配置，包含可空计划分组、脚本路径、参数、时间规则和重叠策略。
_Avoid_: crontab、系统计划、定时任务文件

**计划分组（Schedule Group）**：
维护员定义并排序的计划项容器。计划项至多属于一个分组，未选择分组的计划统一显示在派生的“未分组”区域；删除分组只移除容器并保留其中计划项。分组只用于页面组织，不参与用户授权。
_Avoid_: 文件夹、标签、执行队列

**计划触发（Schedule Trigger）**：
内置调度器按计划项发起的一次启动尝试；它可能创建执行，也可能根据重叠策略跳过并留痕。
_Avoid_: 执行、排队任务、重试

**重叠策略（Overlap Policy）**：
同一脚本已有活动执行时，计划项决定是否仍启动新实例的规则；默认允许重叠，也可选择跳过本次计划触发。
_Avoid_: 并发上限、队列、脚本锁

**变量（Variable）**：
由管理员在 ScriptBoard 中预存的具名普通值，具有 `text`、`bool`、`integer`、`float` 或严格 `x.y.z` 的 `version` 类型，可在参数中通过名称引用，并在启动脚本前解析为字符串。类型只约束写入格式，不提供业务范围。变量可标记为密码类型以便在变量页面默认隐藏，但该标记不改变其明文存储和解析方式。
_Avoid_: 秘密、凭据、环境变量

**执行租约（Run Lease）**：
某个脚本文件从启动请求被接受到执行结束期间持有的应用级保护；持有期间不能通过网页改变脚本文件或其祖先目录的路径与内容。
_Avoid_: 操作系统文件锁、依赖锁、目录冻结

**路径租约（Path Lease）**：
主机文件操作与 Run 在绝对路径及其祖先/后代范围上取得的应用级互斥约束，用于阻止执行、移动、删除、恢复和跨文件系统事务互相竞争。它不替代操作系统锁，也不能阻止外部进程修改文件。
_Avoid_: 文件句柄锁、数据库行锁、永久锁

**文件操作（File Operation）**：
一次持久化的跨文件系统移动事务，记录扫描、复制、校验、目标提交、源回收、引用更新、完成或失败阶段及字节进度。服务重启后依据操作日志恢复或回滚，不把半成品暴露为已完成目标。
_Avoid_: Run、上传任务、后台队列

**宿主状态（Host Status）**：
当前 ScriptBoard 实例所在宿主机的资源事实与短期历史，包括 CPU、内存、存储、磁盘 I/O、网络和 ScriptBoard 服务进程。它用于日常观察与定位资源压力，不构成资源隔离、告警平台或长期可观测性系统。
_Avoid_: 集群监控、资源配额、告警中心、机器管理

**应用观测（Application Observation）**：
当前 ScriptBoard 实例对本机宿主应用和本机 Docker 容器应用资源事实的只读采集、聚合与展示。它帮助管理员定位当前资源压力，不提供进程或容器控制、阈值告警、通知或远程监控。
_Avoid_: 应用管理、容器编排、告警平台、APM

**宿主应用（Host Application）**：
由同一规范化可执行路径标识的一组本机进程。多个 PID 的 CPU、内存、磁盘 I/O、进程数和线程数聚合为一个应用观测条目；无法读取路径的进程可以显示，但身份受限且不能 Pin。
_Avoid_: Top 进程、服务、Run

**Docker 容器应用（Docker Container Application）**：
由本机 Docker Engine 与规范化容器名共同标识的运行容器。容器以整体展示 CPU、内存、块 I/O 和 PID 数，不拆分容器内进程，也不表示 ScriptBoard 自身支持容器化部署。
_Avoid_: 容器服务、Pod、工作负载

**应用 Pin（Application Pin）**：
管理员保存到 ScriptBoard 本机状态中的应用观测关注项。Pin 只改变展示优先级；它不启动、停止、保护或改变对应进程与容器。
_Avoid_: 收藏、监控规则、资源策略

**安装根目录（Install Root）**：
操作系统级的 ScriptBoard 程序目录，用于保存版本化程序文件、当前版本选择信息和安装元数据；Linux 通过稳定 `current` 入口选择版本，Windows 通过服务目标选择版本。它是受保护路径，不包含内部状态目录。
_Avoid_: 主机文件系统、内部状态目录、数据目录

**已安装 Release（Installed Release）**：
已经完整写入 `Install Root/versions/<version>`、通过本地内容校验并可由系统服务切换到的正式 Release。
_Avoid_: 当前版本、源码 Commit、下载缓存

**更新 Release（Update Release）**：
固定官方 GitHub 仓库中，由签名更新清单描述并面向一个正式稳定 Tag 发布的一组平台归档。
_Avoid_: 已安装 Release、主机文件版本、Git 工作区

**更新清单（Update Manifest）**：
由 Release 工具生成、经 Ed25519 签名的规范 JSON，声明产品、仓库、版本、Tag、Commit、数据库 Schema、updater 协议及各平台归档的名称、大小和摘要。
_Avoid_: `SHA256SUMS`、`RELEASE.json`、GitHub Release 说明

**更新操作（Update Operation）**：
从准备、交接、版本切换、验证到提交或回滚的一次持久化更新事务，拥有稳定 Operation ID 和可恢复阶段。
_Avoid_: 下载任务、Run、数据库迁移

**更新程序（Update Helper）**：
独立于 ScriptBoard 主服务进程运行、负责停止/启动服务、保存数据库快照、切换 Installed Release、验活和失败回滚的受信程序。
_Avoid_: 后台下载器、主服务、安装向导

**验证模式（Validation Mode）**：
目标版本首次启动但更新尚未提交时的维护状态；计划暂停，Run 与业务写请求被拒绝，只提供完成验活所需的最小读取能力。
_Avoid_: 只读模式、安全模式、降级模式

**便携安装（Portable Installation）**：
从任意解压目录直接运行、没有新版安装元数据和专用 Install Root 的部署形态；可以检查更新，但不能由 ScriptBoard 原地切换版本。当前程序所在安装目录仍属于受保护路径。
_Avoid_: 系统服务安装、旧式服务安装

**网站监控（Website Monitor）**：
由当前 ScriptBoard 主机主动检查的 HTTP、HTTPS、WebSocket 或 WSS 端点配置，以及其短期可用性证据、确认故障和故障时间线。它与宿主状态是相邻上下文，不提供外部通知、跨主机采集或长期可观测性。
_Avoid_: 宿主状态、端口扫描、告警平台、服务发现

**自定义面板（Custom Dashboard）**：
由多个数据卡片组成的实例级监控视图，拥有独立名称、稳定公开地址标识和公开状态；删除面板同时删除其卡片并使公开地址失效。
_Avoid_: 状态页、监控分组、报表

**数据卡片（Data Card）**：
自定义面板中的一个有序展示项，表示通过 HTTP 或 HTTPS 获取的远程 JSON 数据、Registry 镜像版本结果，或者对既有网站监控结果的只读引用；它保留最近一次成功快照，但不保存历史序列。
_Avoid_: 小组件、告警规则、网站监控副本

**应用消息（Application Message）**：
WebSocket 连接中的文本帧或二进制帧，可配置发送内容和匹配规则；它不包括 Ping、Pong 或 Close 控制帧。
_Avoid_: Ping 消息、Pong 消息、控制帧文本

**活性检查（Ping/Pong Check）**：
按 RFC 6455 发送 Ping 控制帧，并且只在收到载荷逐字节完全一致的 Pong 控制帧时成功的检查。相同字节的文本帧或二进制帧不构成成功。
_Avoid_: 文本心跳、应用消息检查、字符串 Ping

**一次性执行（One-time Execution）**：
系统管理员或维护员提交源码并立即创建的 Run。源码由 Run 持有在私有 State Root Run 目录，
执行 workdir 则是独立选择的普通主机目录；源码不会成为主机条目或快捷执行项。
_Avoid_: 临时文件执行、快捷执行、交互式终端

**一次性源码（One-time Source）**：
一次性 Run 实际执行字节的只读快照，拥有 SHA-256 和受审计控制的有限保留期。
源码回收不删除 Run 元数据、结果或 Run 日志。
_Avoid_: Run 日志、主机脚本、审计正文

**快捷创建（Quick Create）**：
从源码在允许写入的主机目录创建持久脚本并同时建立快捷执行项的事务；创建本身不运行脚本。
_Avoid_: 一次性执行、保存历史 Run、上传文件

**外部功能分组（External Function Group）**：
一组共享访问边界的固定入站调用路径。分组可以先于路径和 Key 创建，并拥有多个调用路径与多个访问 Key。
_Avoid_: Key 分组、单接口 Key、Webhook

**外部接口 Key（External Interface Key）**：
隶属于一个外部功能分组、供可信外部系统调用该分组全部路径的凭据。Key 有独立启停状态与可选到期时间，完整值仅在创建或轮换时显示一次；它不是用户 Session，也不是通用 API Token。
_Avoid_: 用户密钥、脚本令牌、公开 API Key

**外部调用路径（External Call Path）**：
隶属于一个外部功能分组、以稳定路径名区分的预配置动作。路径只允许记录日志、受限上传、启动已有快捷执行、约束变量修改或读取网站监控，调用方不能选择任意路径、脚本、参数或变量。
_Avoid_: Webhook 脚本、远程命令、动态动作

**MySQL 实例（MySQL Instance）**：
由管理员或维护员登记的一个 MySQL 或 MariaDB TCP 服务连接边界，包含地址、可显式关闭或启用的 TLS 策略与加密保存的凭据引用。它不是 SQLite 状态库，也不代表 SSH 隧道、Socket 或复制拓扑。
_Avoid_: 数据源、连接字符串、数据库服务器文件

**MySQL 备份（MySQL Backup）**：
针对一个普通数据库生成或导入的独立逻辑备份产物，拥有来源、保留类别、SHA-256 与稳定 ID。计划轮换只处理该计划自己的成功产物；手动、导入和安全备份不自动轮换。
_Avoid_: 物理快照、增量备份、PITR、状态库快照

**MySQL 操作（MySQL Operation）**：
一次持久化的备份、导入、恢复、回滚或安全删除流程，记录可恢复阶段、脱敏结果与取消请求。破坏性恢复在服务重启后必须继续安全回滚，或明确进入 `needs_attention`。
_Avoid_: Run、SQL 查询、后台作业日志

**MySQL 备份计划（MySQL Backup Plan）**：
绑定单个 MySQL 实例、数据库集合和五字段 Cron 的逻辑备份规则。同一实例串行执行；重叠触发被记录为跳过且不补跑。
_Avoid_: ScriptBoard 脚本计划、系统 crontab、复制计划
