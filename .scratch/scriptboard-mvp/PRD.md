# ScriptBoard MVP 产品需求文档

Status: ready-for-agent

**需求状态**：已冻结  
**冻结日期**：2026-07-20  
**适用版本**：MVP

## Problem Statement

单机管理员经常需要在 Windows 或 Linux 主机上管理一组文件，并可靠地执行其中的可信脚本。现有做法通常把文件传输、命令行执行、定时触发、日志查看、历史追踪和误改恢复拆散在多个工具中，导致操作入口不一致、执行上下文不透明、运行历史容易丢失，也难以在移动浏览器中完成紧急操作。

管理员需要一个自托管、单机、单管理员的工具，把受管根目录作为文件管理边界，让受支持的脚本无需注册即可原地执行；同时提供实时日志、不可删除的 Run 历史、快捷执行、内置 Schedule、审计和可选的本地 Git 版本保护。该工具面向完全可信的管理员与 Trusted Script，不承担多用户授权、不可信代码隔离或分布式编排职责。

## Solution

ScriptBoard 提供一个以单个 Go 服务进程为核心、由 SQLite 保存私有状态、通过服务端渲染网页操作的管理界面。管理员登录后可以浏览和维护 Managed Root 中的 Managed Entry，选择受 Host Capability 支持的 Script，填写参数模板和 Variable，启动 Run，并通过 SSE 查看有序 Run Log。管理员还可以停止活动 Run、查询永久保留的元数据、从历史 Run 保存 Quick Run、配置由内置调度器触发的 Schedule，并查看 Audit Event。

系统在 Windows 与 systemd Linux 上使用平台原生服务能力。Script 继承服务进程的 Runtime Identity 与环境；ScriptBoard 不提供沙箱、提权、降权或身份切换。可选 Version Protection 使用 Managed Root 内的本地 Git 仓库记录 Versioned File 的安全检查点并支持单文件恢复，但不承担备份或远程同步职责。

完整交付应让管理员完成“登录 → 管理文件 → 启动脚本 → 实时查看输出 → 停止或等待结束 → 查询历史 → 保存 Quick Run 或 Schedule → 必要时恢复文件”的闭环，并满足冻结的数据模型、状态机与验收标准。

## User Stories

1. 作为首次部署的管理员，我希望系统自动创建 admin 并生成一次性初始密码，以便无需预置数据库即可安全开始配置。
2. 作为首次登录的管理员，我希望系统强制我修改初始密码，以便一次性凭据不会继续有效。
3. 作为管理员，我希望密码支持中文、空格和密码短语，并由 Argon2id 安全保存，以便兼顾可用性与离线攻击防护。
4. 作为管理员，我希望可以在多个浏览器中保持持久 Session，以便在桌面端和移动端管理同一实例。
5. 作为管理员，我希望修改账号凭据时撤销全部 Session，以便迅速收回旧凭据对应的访问权限。
6. 作为实例所有者，我希望登录失败受到指数退避但不会永久锁死，以便降低暴力尝试风险且保留本机恢复路径。
7. 作为实例所有者，我希望明文 HTTP 只能绑定回环地址，非回环访问必须使用 TLS 或可信反向代理，以便避免凭据明文暴露。
8. 作为管理员，我希望所有受保护页面、下载、SSE 和状态修改入口都验证 Session，且修改操作验证 CSRF Token，以便浏览器会话不会被匿名访问或跨站滥用。
9. 作为管理员，我希望浏览 Managed Root 中的普通文件和目录，以便以文件服务器方式管理 Script 和辅助资料。
10. 作为管理员，我希望 Restricted Link 与 Restricted Mount 可见但不可进入、读取、覆盖、下载或执行，以便了解磁盘内容而不突破目录边界。
11. 作为管理员，我希望文件列表支持分页、自然排序、字段排序和当前目录名称搜索，以便在大目录中快速定位 Managed Entry。
12. 作为实例所有者，我希望超大目录扫描被有界终止，以便异常目录不会耗尽服务内存。
13. 作为管理员，我希望批量上传普通文件并创建目录，以便从网页布置 Script 和输入文件。
14. 作为实例所有者，我希望上传按文件、请求和批次数量限流并流式落盘，以便大文件不会占满内存或留下半文件。
15. 作为管理员，我希望同名文件默认拒绝，只有二次确认后才原子替换且旧文件进入 Trash，以便避免意外覆盖。
16. 作为管理员，我希望预览和编辑小型 UTF-8 文本，并在外部修改后收到冲突提示，以便不会静默覆盖并发变更。
17. 作为管理员，我希望仅内嵌安全栅格图片，而将 SVG、HTML、PDF、Office、音视频和未知格式作为附件下载，以便减少主动内容风险。
18. 作为管理员，我希望单文件下载支持 HTTP Range，以便可靠下载大文件和续传。
19. 作为管理员，我希望网页重命名或移动 Script 时同步更新 Quick Run 与 Schedule 引用，以便既有自动化继续指向正确路径。
20. 作为管理员，我希望删除操作把内容移入 Trash，并允许无冲突恢复或经确认永久清理，以便误删可以恢复。
21. 作为管理员，我希望删除被引用 Script 前看到影响，删除后 Schedule 自动禁用而 Quick Run 标记失效，以便失效自动化不会静默执行错误目标。
22. 作为 Linux 管理员，我希望只切换普通文件的所有者执行位，以便满足脚本执行需求而不暴露通用权限编辑器。
23. 作为管理员，我希望受支持扩展名的 Script 无需注册即可在原位置执行，以便文件管理和执行保持同一个事实来源。
24. 作为管理员，我希望系统按平台 Executor Chain 选择可用解释器，并仅在进程创建前回退，以便执行选择可预测且失败脚本不会被重复运行。
25. 作为管理员，我希望 Run 的工作目录固定为 Script 父目录，并记录路径、摘要、执行器和 Runtime Identity，以便历史可解释、相对路径行为稳定。
26. 作为管理员，我希望用简化 shellwords 编写参数并以独立参数形式引用 Variable，以便跨平台传参而不执行管道、重定向或命令替换。
27. 作为管理员，我希望系统保存参数模板和启动时解析出的实际参数数组，以便历史 Run 可以准确解释和复用。
28. 作为管理员，我希望 Variable 具有明确名称、大小和数量上限，且被引用时不能直接删除，以便自动化配置保持完整。
29. 作为管理员，我希望不同 Script 可立即并发执行且不存在队列，以便相互独立的工作无需等待全局槽位。
30. 作为管理员，我希望重复启动同一 Script 时看到现有活动实例并二次确认，以便有意重叠而不是误触重复运行。
31. 作为管理员，我希望 Run Lease 阻止网页修改活动 Script 及移动其祖先目录，以便执行期间路径和内容不会被 ScriptBoard 自己改变。
32. 作为管理员，我希望为 Run 配置超时，超时后先优雅终止再强杀完整进程树，以便失控进程有退出机会且最终能被清理。
33. 作为管理员，我希望第一次停止请求优雅退出、第二次停止立即强杀进程树，以便我能按现场情况控制中断强度。
34. 作为实例所有者，我希望正常停服先停止接收新工作，再终止全部活动 Run，以便服务升级或关机不会遗留托管进程。
35. 作为管理员，我希望服务异常重启后旧活动 Run 变为 Disconnected，而不是假装重新接管进程，以便历史状态诚实反映监督关系。
36. 作为管理员，我希望 Run 只按冻结状态机转换且终态不可逆，以便执行历史具有稳定语义。
37. 作为管理员，我希望进程创建前的校验失败不创建 Run，而创建阶段失败留下 failed Run，以便区分请求拒绝与真实启动故障。
38. 作为管理员，我希望 stdout 和 stderr 进入一个带来源、时间和稳定序号的 Run Log，以便实时输出顺序可追踪。
39. 作为管理员，我希望刷新页面后通过 Last-Event-ID 续接 SSE，以便不重放全部日志也不静默遗漏可用事件。
40. 作为实例所有者，我希望单 Run 日志和全局日志存储都有上限，且达到上限仍持续排空进程管道，以便磁盘和进程不会被无限输出拖垮。
41. 作为管理员，我希望 Run 元数据永久且不可删除，即使日志过期仍保留结果标记，以便操作历史可持续审计。
42. 作为管理员，我希望从历史 Run 主动保存具名 Quick Run，并能排序、删除和再次启动，以便常用执行配置一键复用。
43. 作为管理员，我希望创建五段 cron Schedule、预览未来触发时间并选择重叠策略，以便跨平台配置可预测的定时执行。
44. 作为管理员，我希望 Schedule 使用实例统一时区且停机期间不补跑，只汇总错过次数，以便恢复服务时不会制造执行风暴。
45. 作为管理员，我希望跳过的 Schedule Trigger 留痕但不创建 Run，以便区分调度尝试和实际执行。
46. 作为管理员，我希望 Schedule Run 与手动 Run 共用参数、执行器、Version Protection、安全检查、日志和状态模型，以便所有触发来源行为一致。
47. 作为管理员，我希望查看认证、文件、Run、Schedule、Variable、Quick Run、Git 和清理操作产生的 Audit Event，以便追踪高影响操作。
48. 作为管理员，我希望审计记录不包含密码、Session、CSRF Token、Variable 值或文件内容，以便追踪行为时不泄露敏感数据。
49. 作为管理员，我希望可选启用本地 Version Protection，以便从误改中恢复 Versioned File 而无需远程仓库。
50. 作为管理员，我希望启用、停用和 Run 批次边界产生明确 checkpoint，以便 Git 历史反映安全操作边界。
51. 作为管理员，我希望活动 Run 期间不执行 Git 维护或提交，以便并发脚本修改只形成一致的批次时间线。
52. 作为管理员，我希望 pre-run checkpoint 失败时拒绝执行，post-run 失败时进入 abnormal 安全门，以便版本承诺失效时不继续产生不可保护的变更。
53. 作为管理员，我希望过大或被忽略的文件仍可执行但显示未保护原因，以便 Version Protection 资格不会错误变成执行资格。
54. 作为管理员，我希望查看单文件历史并通过新提交恢复历史版本，以便撤销误改而不改写可达历史。
55. 作为已有仓库的管理员，我希望在完整检查和确认后接管本地非 bare 仓库，同时保留既有对象和引用，以便安全迁移到 ScriptBoard 管理。
56. 作为实例所有者，我希望 Git Hooks、过滤器、外部 diff、凭据助手、子模块和远程操作被禁用，以便本地版本操作不会执行仓库提供的代码或访问网络。
57. 作为 Windows 管理员，我希望用原生服务和系统托盘启动、停止、重启、打开网页及查看状态，以便无需命令行即可管理常驻实例。
58. 作为 Linux 管理员，我希望通过 systemd 安装和管理服务，以便遵循主机原生服务生命周期。
59. 作为运维人员，我希望使用 CLI 校验配置、管理服务、重设 admin、运行只读 doctor 和查看版本，以便在网页不可用时诊断实例。
60. 作为实例所有者，我希望配置遵循默认值、YAML、环境变量、命令行参数的固定优先级，以便最终配置可预测且覆盖不会意外写回。
61. 作为实例所有者，我希望 SQLite 使用耐久设置、排他实例锁和只向前迁移，并在迁移前创建内部安全快照，以便单实例状态可靠升级。
62. 作为实例所有者，我希望低磁盘空间拒绝新的高写入操作但不自动杀死已运行 Script，以便先保护状态完整性再人工处置容量问题。
63. 作为运维人员，我希望 doctor 只读检查配置、目录、数据库、日志、Executor、Git、网络、服务和磁盘且不泄露敏感值，以便安全收集诊断信息。
64. 作为发布使用者，我希望获得 Windows 与 Linux 的 amd64/arm64 便携归档和 SHA-256，以便无需额外运行时或安装器即可部署并验证制品。
65. 作为中文管理员，我希望网页、错误、托盘和 CLI 帮助均使用简体中文，以便 MVP 的操作体验一致。
66. 作为移动端管理员，我希望能登录、浏览文件、启动 Run、查看日志、启动 Quick Run 和停止 Run，以便离开桌面时仍可处理核心操作。

## Implementation Decisions

- 产品采用单机、单管理员、单服务进程模型；每台主机正式支持一个使用同一 State Root 的实例，不引入多用户、RBAC、队列或运行时集群依赖。
- 使用 Go 单模块、标准 HTTP 服务、服务端模板、少量原生 JavaScript、SSE、嵌入式静态资源和纯 Go SQLite；不引入 Node.js 构建链、SPA、Redis 或消息队列。
- 核心边界分为认证与 Session、Managed Root 文件服务、Run 编排与进程监督、Run Log、Quick Run、Schedule、Variable、Audit Event、Version Protection、平台服务适配和诊断发布。
- 所有受保护 HTTP 路由共享 Session 验证；所有状态修改使用非 GET 方法并验证 CSRF。匿名面只包含登录入口和无敏感内容的静态资源。
- admin 凭据使用版本化 Argon2id 参数；Session 使用高熵随机 ID，客户端只持有 ID，服务端只保存哈希。启动凭据覆盖是权威配置，发生变化时重设账号并撤销全部 Session。
- Managed Root 与 State Root 必须解析为互不包含的真实路径。文件访问在每个操作边界验证路径、条目类型、文件系统或卷边界；Restricted Link 与 Restricted Mount 只展示、不解引用。
- Managed Entry 操作采用流式 I/O、同目录临时文件和原子替换。会覆盖旧内容的网页操作先保留旧版本到 Trash；文本编辑使用内容摘要做乐观并发控制。
- Quick Run 与 Schedule 引用规范相对路径。网页移动同步更新活动引用并提供补偿回滚；外部移动不按名称、摘要或 inode 猜测新路径。历史 Run 保存启动时路径快照。
- Script 无需注册。Host Capability 由平台实际可用的 Executor Chain 决定；回退只发生在候选不可用或无法创建进程时，一旦 Script 启动就不因退出结果选择其他 Executor。
- 参数输入只实现冻结的 shellwords 子集；Variable 引用必须独占参数。启动前将模板解析为实际参数数组并执行大小、数量、路径、Variable、Executor、磁盘和 Version Protection 安全门校验。
- Run Source 与 Runtime Identity 分离记录。Script 继承服务身份和环境，应用只注入非敏感运行元数据，不提供沙箱或身份切换。
- Run 使用冻结状态机：starting、running、stopping、timing_out 与五个终态。启动前拒绝不落 Run；进程创建失败落 failed Run；终态不可逆且 Run 元数据不可删除。
- 活动 Run 由规范真实路径和操作系统文件 ID 建立 Run Lease。Lease 覆盖 Script 本身与祖先目录的相关网页变更，但不承诺阻止 Script 或管理员直接修改磁盘。
- Linux 使用独立进程组与父进程死亡能力，Windows 使用关闭即终止的 Job Object。停止、超时、服务关闭均作用于完整进程树，并按触发原因固定最终 Run 状态。
- Run Log 是追加式有序事件流，保存稳定序号、时间、stdout/stderr 来源和原始字节；SQLite 只保存 Run 元数据，不承载高频输出。SSE 以事件序号支持断线续接。
- 每个 Run Log 默认上限 100 MiB，保留头部 5 MiB 和约 95 MiB 尾部并插入截断事件。全局日志按时间与容量清理，但永久保留 Run 元数据和日志过期或不完整标记。
- Schedule 使用服务进程内调度器与五段 cron；统一实例时区、UTC 持久化、不补跑停机触发、不特殊处理夏令时，并持久化未创建 Run 的 Schedule Trigger 及后续聚合。
- Version Protection 默认关闭，依赖系统 Git CLI，使用 Managed Root 中的单一本地仓库与固定 `scriptboard-managed` 分支。它是误改恢复能力，不是备份，不支持远程操作或历史改写。
- Version Protection 只跟踪符合资格的普通文件，强制排除应用保留路径并遵守 `.gitignore`。文件是否 Versioned 不改变它是否可执行。
- Git 以无活动 Run 时的网页操作和显式边界为 checkpoint；活动 Run 期间不进行提交、恢复、维护或启停。pre-run 失败拒绝启动，post-run 失败进入 abnormal 安全门。
- Git 接管先验证仓库形态、完整性和危险配置，再经确认采用最小安全配置；保留对象与引用，禁用 Hooks、可执行 attributes、过滤器、外部 diff、fsmonitor、credential helper、submodule 和远程操作。
- Versioned File 恢复只支持单文件并产生新提交；不提供整仓或目录恢复、reset、rebase、merge 等历史改写入口。仓库达到容量上限后停止新提交，不删除可达历史。
- SQLite 启用 WAL、FULL synchronous、foreign keys 和 busy timeout。Schema 只向前迁移；迁移前创建内部安全快照，失败则事务回滚并拒绝启动，不提供用户可见的 backup/restore 命令。
- 网络默认监听回环端口 8787。非回环必须使用内置 TLS；可信代理显式配置后才采信转发地址。不提供公共 API、API Token、Basic Auth 或远程 CLI。
- Windows 使用原生服务控制管理器和独立托盘程序，Linux 正式支持 systemd。托盘退出不停止服务，Windows Terminal 只可作为打开目录的便利入口，不参与 Script 执行。
- 发布 Windows 10/11、Windows Server 2019+ 与代表性 systemd Linux 的 amd64/arm64 便携归档及摘要；不制作原生安装包、不自动更新、不联网检查版本。
- MVP 用户界面、错误、托盘与 CLI 帮助为简体中文，结构化标识保持英文；核心网页流程支持现代桌面和移动浏览器。
- 冻结的数据模型、Run 与 Git 状态机、文件引用不变量和验收标准是实现约束；已标记 superseded 的历史决策不得作为当前方向。

## Testing Decisions

- 主要测试 seam 采用最高可观察边界：启动真实 ScriptBoard 服务或打包后的 CLI，通过 HTTP/HTML、SSE、文件系统副作用、进程行为、SQLite 可观察状态和 Git 历史验证管理员可见行为。测试不依赖内部 handler、私有函数或 SQL 实现细节。
- 首要端到端流程覆盖首次启动与强制改密、登录与 Session、文件上传/编辑/移动/Trash、Script 启动、实时 Run Log、停止与终态、历史保存 Quick Run、Schedule 触发、Audit Event 和单文件 Git 恢复。
- 认证安全测试覆盖匿名访问、CSRF、Cookie 属性、Session 时限与撤销、启动凭据覆盖、登录退避和敏感值不泄漏。以 HTTP 响应、持久 Session 行为和可下载审计结果作为断言。
- 文件边界测试在真实临时文件系统上覆盖路径穿越、编码路径注入、Restricted Link、Restricted Mount、硬链接文件 ID、跨卷边界、原子替换、上传中断、编辑冲突、Range 下载和 Run Lease。
- Run 编排测试以短生命周期的 fixture Script 覆盖 Executor Chain、参数解析、Variable 解析、并发重叠确认、完整进程树、优雅停止、强制停止、超时、正常停服和异常重启后的 Disconnected。
- 状态机测试以对外事件和最终持久状态覆盖每条允许转换，并证明启动前拒绝不创建 Run、进程创建失败创建 failed Run、停止或超时原因不被退出码覆盖、终态不可逆。
- Run Log 与 SSE 测试覆盖 stdout/stderr 有序来源、Last-Event-ID 续接、无效 UTF-8 与控制字符、DOM 初始容量协议、单 Run 截断、全局清理和日志写入失败时持续排空。
- Schedule 测试使用可控时钟覆盖五段 cron、未来预览、统一时区、停机不补跑、错过汇总、允许重叠、禁止重叠的 skipped Trigger，以及与手动 Run 共用的启动安全门。
- Version Protection 测试使用隔离的真实 Git 仓库和禁用用户级配置的环境，覆盖首次启用、外部仓库接管、安全配置、文件资格、批次 checkpoint、pre/post-run 故障、abnormal 恢复、容量限制及单文件恢复新提交。
- 平台 seam 通过共享进程监督契约加平台集成测试验证；Windows 覆盖服务控制管理器、Job Object 与托盘状态，Linux 覆盖 systemd、进程组和父进程死亡行为。两平台都运行服务生命周期验收。
- 数据库测试通过启动不同 schema 版本的真实实例验证耐久 PRAGMA、外键、排他实例锁、迁移前内部快照、迁移失败回滚、完整性异常安全模式和旧二进制拒绝新 schema。
- 资源边界测试应实际越过目录条目数、上传大小、参数、Variable、日志、Git 仓库和低磁盘阈值，验证操作被有界拒绝且已有 Run 不因低磁盘被自动终止。
- 发布验收对 Windows/Linux、amd64/arm64 制品执行启动、CLI 帮助、配置校验、doctor 和核心浏览器流程；现代桌面浏览器覆盖完整闭环，移动视口覆盖登录、浏览、启动、日志、Quick Run 与停止。
- 当前仓库尚无应用代码或测试先例；实现应以本节的单个高层服务 seam 建立第一套验收夹具，并仅在操作系统能力无法从该 seam 稳定观测时增加平台契约测试。
- 测试只验证冻结的外部行为和不变量；不锁定模块拆分、函数调用次数、SQL 文本、HTML 内部结构或其他可重构实现细节。

## Out of Scope

- 多用户、角色、用户组、LDAP、OAuth、SSO 或逐 Script 授权。
- 不可信代码沙箱、容器隔离、应用内提权/降权或逐 Script Runtime Identity 切换。
- 公共 API、API Token、Basic Auth、远程 CLI 或第三方集成接口。
- DAG、流水线、全局执行队列、并发上限、多服务器、集群或 Kubernetes 编排。
- Docker 作为正式部署方式，以及除 systemd 以外的 Linux 服务管理器正式支持。
- 系统 crontab 读取、修改或触发集成。
- Git 远程同步、clone、fetch、pull、push、子模块、LFS、外部过滤器和可执行 attributes。
- 用户可见的数据库或文件 backup/restore 命令；迁移前内部安全快照仍属于实现要求。
- 邮件、聊天通知、插件系统、Webhook 或自动化扩展市场。
- 交互式终端、PTY、stdin、Shell 模式和 Windows Terminal 执行器。
- 文件夹上传、服务端解压、目录或多选 ZIP 下载。
- 通用 chmod、chown、chgrp 和目录权限编辑。
- 整仓或目录 Git 恢复，以及 branch、merge、rebase、reset、cherry-pick 等历史操作。
- 自动更新、联网版本检查、MSI、DEB、RPM 或其他原生安装器。
- 多语言界面和正式多实例支持。

## Further Notes

- 本 PRD 是 2026-07-20 已冻结 MVP 需求在本地 Markdown issue tracker 中的规范发布版本，状态直接设为 `ready-for-agent`，无需额外 triage。
- 领域术语以 Script、Trusted Script、Runtime Identity、Run Source、Host Capability、Executor Chain、Managed Root、State Root、Managed Entry、Restricted Link、Restricted Mount、Trash Entry、Version Protection、Versioned File、Run、Disconnected、Run Log、Audit Event、Quick Run、Schedule、Schedule Trigger、Overlap Policy、Variable 和 Run Lease 为准，不使用领域词汇中明确要求避免的同义词。
- 实现拆分应采用可独立交付的 tracer-bullet vertical slice，而不是按前端、后端、数据库或平台层水平切割。每个后续 issue 都应引用本 PRD 的相关用户故事、实现决策和测试决策。
- 当前工作区尚未初始化为 Git 仓库；开始实现前应初始化版本控制，除非维护者明确选择延后。这里指开发工作区版本控制，与产品内可选 Version Protection 是两个不同概念。
- 测试 seam 的推定依据是当前没有应用代码或既有测试、而产品具有完整 CLI/HTTP/文件系统可观察边界。若实现过程中发现无法在最高 seam 稳定验证的平台行为，可增加最少数量的平台契约 seam，但不应把内部模块结构固化成验收契约。
