# ScriptBoard 全量代码结构重构与 Release 修复计划

状态：计划中
制定日期：2026-08-13
基线：`dev`，最后正式版本 `v2.0.21` 之后的全部变更
交付方式：一个功能分支、一个 PR、一次合并、一次正式发布

## 1. 结论与执行约束

本次工作采用“最终一次性切换”，但不采用不可验证的单提交大爆炸：所有修改必须在同一功能分支内按下述提交序列完成，每个提交保持可编译、可测试，只有全部门禁通过后才允许整体合并到 `dev`。不得把中间态、兼容 facade 或只迁移一半的目录合入 `dev`，也不得从中间提交发布。

建议分支名：`codex/codebase-restructure`。若需要 worktree，必须按仓库约定创建在 `../worktrees/codebase-restructure`。

本计划同时完成两类工作：

1. 修复 `v2.0.21..dev` 评审中确认的安全性和一致性问题。
2. 重构当前项目目录与代码依赖，把 `internal/app` 从“全能包”拆成明确的组合层、Web 层和领域模块。

最终交付不包含新产品功能、UI 视觉改版、数据库产品替换或对外 API 的主动破坏性变更。除 Registry 凭据迁移和外部调用记录恢复所需的 schema/IPC 协议升级外，用户可见行为、路由、权限和数据语义保持不变。

## 2. 启动门禁

当前工作区存在尚未提交的登录挑战、MFA、Passkey 和前端改动。开始重构前必须先完成以下动作，禁止直接覆盖：

- 将现有改动单独提交到 `dev`，或明确提交到本次功能分支的第一个基线提交。
- 记录 `git status --short`、`git rev-parse HEAD`、`git describe --tags --always` 和 `git diff --stat v2.0.21..HEAD`。
- 运行并保存基线结果：`go test ./... -count=1`、`go vet ./...`、`go build ./cmd/...`。
- 在 `integration/browser` 中执行 `pnpm install --frozen-lockfile` 和 `pnpm test`。
- 生成路由、schema 版本、IPC 协议版本、配置项和嵌入资源清单快照，后续用结构化测试对比，不依赖人工记忆。
- 确认工作区根目录中的本地二进制仍由 `.gitignore` 排除；新的构建产物统一输出到 `dist/` 或临时目录。

若基线门禁失败，先在独立提交中修复或记录既有失败；不得让重构掩盖基线问题。

## 3. 必须先处理的评审项

### 3.1 Registry 凭据的信任边界与一致性

当前 `internal/customdashboard/secret_store.go` 把密文和 AES 主密钥放在同一个 State Root 中。备份或读取 State Root 的主体可以同时获得两者，这不满足 Broker-owned secret 边界。

最终方案：

- 删除 Web 进程内的 `credentialStore` 和 `custom-dashboard-registry.master-key` 新建逻辑。
- Registry 密码只允许通过受认证的 Broker IPC 写入、读取用于探测或删除；Web 不持久化、不回读明文。
- Broker 使用现有受保护秘密存储约定；生产密钥不得进入 State Root、普通备份或日志。
- Registry 连接配置与凭据更新使用同一个版本号和幂等操作 ID。Broker 保存待提交版本，Web 的 SQLite 事务提交后激活；任一步失败都可按操作 ID 补偿或由启动 reconciliation 收敛。
- 更新、匿名化、卡片类型切换和删除都必须具备故障注入测试，证明不会出现“新 endpoint + 旧密码”、孤儿凭据或丢失当前可用版本。
- 首次启动迁移旧 `custom-dashboard-registry.json`：逐项导入 Broker，验证后再删除旧密文和主密钥；迁移失败时保留原数据、拒绝使用不一致的新配置并输出脱敏诊断。
- State Root 备份和恢复清单明确排除旧主密钥；恢复后通过 Broker 重新关联凭据状态，缺失时显示“需重新配置”，不得静默降级。

### 3.2 外部动作完成记录不可静默失败

当前动作已经执行后，`CompleteInvocation` 失败会保留业务响应，这一行为避免客户端重试造成重复副作用，本身应保留；问题在于失败没有进入可靠的补偿和可观测路径。

最终方案：

- `runRecordedExternalAction` 返回的 `RecordError` 必须由调用方处理。
- 动作开始记录持久化操作 ID；完成记录失败时，把结果写入有界、持久化的 reconciliation 队列并记录审计/指标。
- HTTP 仍返回真实动作结果，并附原 request ID；不得把已成功执行的动作改报 500 而诱发重复执行。
- 启动和定时任务重放待完成记录；同一操作 ID 的完成写入必须幂等。
- 超过恢复期限的 `processing` 记录转为明确的 `unknown`，保留“动作可能已执行”的事实，禁止推断为失败。

### 3.3 Development installer 合同澄清

复核 `docs/RELEASING.md` 和测试后，development installer 被限制为仅支持 `--version-json` 是既定设计，不应放开受管服务安装，也不应把它当作可安装产物。本次不改变该安全边界，只消除工作流歧义：

- `development-installers` job 构建后必须对四个平台产物执行元数据/归档结构验证。
- 明确断言无参数安装和 `--extract-to` 均被拒绝。
- Artifact 名称和文档继续标明“packaging validation only”。
- 正式 Tag 的 installer smoke test 保持独立，development 结果不能替代正式发布门禁。

## 4. 目标目录与依赖方向

最终目录如下；现有职责清晰的叶子包保持原位，不做无意义搬家：

```text
cmd/
  scriptboard/                 # 参数解析和进程入口
  scriptboard-broker/
  scriptboard-runner/
  scriptboard-installer/
  scriptboard-updater/
  scriptboard-release/
  scriptboard-tray*/
internal/
  bootstrap/                   # 三个运行时的唯一组合根
    web.go
    broker.go
    runner.go
    runtime.go
  store/                       # SQLite 生命周期和 schema
    sqlite.go
    transaction.go
    migrations/
  web/                         # HTTP、路由、中间件、view model
    server.go
    routes.go
    middleware/
    ui/
      assets/
      templates/
      locale/
  identity/                    # 用户、密码、session、MFA、Passkey、step-up
  quickrun/                    # 快速执行定义、发布、复制和触发
  scheduling/                  # cron、并发策略、调度持久化
  fileworkflow/                # 托管文件、引用、冲突、Host Files 边界
  audit/                       # Web 侧审计查询、保留和外部记录 reconciliation
  platform/windowsservice/     # Broker/Runner 共用的 SCM 包装
  ...                          # customdashboard、runner、privilegebroker 等既有叶子包
```

强制依赖方向：

```text
cmd -> bootstrap -> web / domain services -> leaf packages / store
```

约束：

- `cmd/*` 不构造数据库、Broker client、Host Files 或领域 manager；只解析参数并调用 `bootstrap`。
- `bootstrap` 是唯一允许同时依赖多个领域模块的组合根，不包含业务规则和 HTTP handler。
- `web` 只依赖领域 service 接口，不直接调用 `database/sql`，不执行 migration，不拥有秘密。
- 领域模块拥有自己的行为、校验和持久化语义；接口由使用方定义，避免通用 Repository/Service 抽象。
- `store` 提供连接、事务和 migration 执行，不承载领域规则。
- 叶子包不得反向依赖 `web`、`bootstrap` 或 `cmd`。
- 禁止新增 `common`、`utils`、`helpers`、全局 service locator 或新的巨型 `Config`。

最终 `internal/app` 必须删除；不得留下转发类型、别名或长期兼容层。

## 5. 逐文件职责迁移

| 当前区域 | 最终归属 | 迁移要求 |
| --- | --- | --- |
| `internal/app/app.go` 启动、依赖构造、关闭 | `internal/bootstrap/web.go` | 只保留生命周期和装配；配置按 Web、Store、Broker、Runtime 分组 |
| `internal/app/app.go` schema 与 migration | `internal/store/migrations/` | 每个版本独立文件；单一 registry；事务和失败恢复测试 |
| `internal/app/app.go` HTTP server 与 handlers | `internal/web/` | server、route catalog 和 feature routes 分离 |
| `route_spec.go`、host/origin/auth middleware | `internal/web/routes.go`、`middleware/` | 保留 fail-closed RouteSpec；新增完整路由快照测试 |
| `web/assets`、`web/templates`、`web_locale.go` | `internal/web/ui/` | 单一 embed 入口；模板/locale key contract 保持 |
| `password_policy.go`、`mfa_web.go`、`passkey_web.go`、登录挑战和用户/session 逻辑 | `internal/identity/` | 合并临时 type assertion 能力为显式接口；领域错误稳定 |
| `quick_execution.go`、`quick_runs.go` | `internal/quickrun/` | 深接口覆盖创建、保存、复制、发布和触发；路由只编排输入输出 |
| `schedules_cron.go` 及 app 内调度 SQL | `internal/scheduling/` | 调度规则和存储同属模块；复用现有 scheduler 叶子包 |
| `file_*`、`host_files_boundary.go` | `internal/fileworkflow/` | 所有路径/引用/冲突规则集中；特权操作仍通过 Broker |
| `audit_retention.go`、外部 invocation reconciliation | `internal/audit/` | 审计链写入仍复用既有安全包；Web 只查询/触发策略 |
| `custom_dashboard_transfer.go` | `internal/customdashboard/` | import/export 与 manager 同属一个深模块 |
| `cmd/scriptboard-broker/main.go` 的 DB/MySQL/Host Files 装配 | `internal/bootstrap/broker.go` | main 收敛为 flags + signal + bootstrap |
| 三份重复 `service_windows.go` | `internal/platform/windowsservice/` | 共用 install/start/stop/status wrapper，服务参数由各 cmd 提供 |

迁移测试与生产代码同时移动。不得复制代码后长期保留两套实现；每个切片在新包测试通过后，立即删除旧实现和旧测试。

## 6. 模块接口设计

### 6.1 Bootstrap

对外只暴露三个启动函数和统一 Runtime 生命周期：

```go
type Runtime interface {
    Run(context.Context) error
    Close(context.Context) error
}

func OpenWeb(WebConfig) (Runtime, error)
func OpenBroker(BrokerConfig) (Runtime, error)
func OpenRunner(RunnerConfig) (Runtime, error)
```

配置按信任边界分组；禁止重建当前约 40 字段的平铺 `app.Config`。

### 6.2 Web

`web.New` 接收已经构造完成的模块集合、RouteSpec catalog、UI bundle 和时钟。Handler 不自行打开资源。鉴权、CSRF、host/origin、request ID、审计 actor 和安全 header 是显式中间件链，并由 route catalog 决定是否启用。

### 6.3 Store 与领域事务

`store` 暴露窄的 `DBTX` 和事务执行器，领域包在自己的 store 文件中写明确 SQL。禁止为了“解耦”生成一套一方法一接口的 repository 镜像。需要跨领域原子性时，由拥有该用例的 service 开启事务并调用 transaction-aware 子操作。

### 6.4 能力接口

删除运行时 type assertion 检测 MFA/Passkey 等可选能力。身份模块在构造时提供显式完整能力；测试替身必须实现同一合同。Broker 和 Runner 的 IPC adapter 继续 fail closed，并保持协议版本矩阵测试。

## 7. 单分支实施顺序

### 提交 1：冻结行为和架构门禁

- 添加路由、schema、IPC、配置和 embed 资源快照测试。
- 添加依赖方向测试：解析 Go imports，禁止 `web -> database/sql`、叶子包反向依赖和 `cmd` 内构造领域对象。
- 添加文件规模/复杂度报告脚本，只报告不以单一 LOC 数值替代设计评审。
- 记录基线测试结果和已知例外。

验收：只增加测试/脚本/文档，产品行为不变，全量测试通过。

### 提交 2：修复 Registry 秘密边界

- 扩展 Broker IPC 协议和 client/server adapter。
- 实现版本化、幂等的 Registry credential operation 与启动 reconciliation。
- 迁移旧凭据并删除 Web 侧 key/store。
- 更新 State Root backup/restore、诊断和脱敏测试。

验收：故障注入覆盖 prepare、DB commit、Broker activate、delete、进程崩溃五个中断点；State Root 单独泄露不能解密 Registry 密码。

### 提交 3：修复外部调用记录恢复

- 实现完成记录 outbox/reconciliation。
- 处理并观测 `RecordError`。
- 增加幂等、重启、过期 `processing -> unknown` 和客户端不重复执行测试。

验收：业务动作成功而数据库短暂失败时，HTTP 结果正确、动作只执行一次、记录最终收敛。

### 提交 4：集中 Store 与 migrations

- 建立 `internal/store` 和 migration registry。
- 把 `app.go` 中的数据库打开、PRAGMA、schema 检测和 migration 循环移出。
- 各既有 `SchemaStatements` 经 registry 统一排序和执行。
- 增加空库、逐历史版本升级、失败回滚和重复启动测试。

验收：`internal/app` 不再拥有 schema 生命周期；全部历史 fixture 均可升级到当前版本。

### 提交 5：提取 Identity

- 移动用户、密码、session、登录挑战、MFA、Passkey 和 step-up 行为。
- 把可选 type assertion 改成显式能力接口。
- Web handler 仅做 HTTP 映射。

验收：原身份、授权、MFA、Passkey 和浏览器测试无语义变化；敏感错误继续对外模糊、对内可诊断。

### 提交 6：提取 Quick Run 与 Scheduling

- 先以 Quick Run 完成第一个纵向 tracer bullet，验证 service/store/web seam。
- 随后迁移 schedule、cron 摘要、并发和触发策略。
- 调整相应 external trigger adapter，保持 request ID 和审计关联。

验收：创建、保存、复制、发布、立即执行、cron 触发、并发冲突和重启恢复全部通过。

### 提交 7：提取 File Workflow、Audit 和 Dashboard transfer

- 移动文件列表、引用、编辑冲突、Host Files adapter 和下载规则。
- 移动审计查询/保留/reconciliation 编排。
- 将 Dashboard import/export 收回 `customdashboard`。

验收：路径穿越、链接、普通文件、冲突版本、Broker 不可用、审计链和 retention 测试通过。

### 提交 8：拆分 Web 与 UI bundle

- 创建 `internal/web` server、middleware、feature route 文件和 `ui` embed 包。
- 将 RouteSpec 作为唯一注册表；禁止 handler 在 catalog 外注册。
- 移动模板、CSS、JS 和 locale，不做视觉重设计。
- 按领域移动 handler contract tests 和 Playwright fixtures。

验收：路由快照差异为空或仅包含本计划明确新增的内部协议端点；页面、locale、键盘和响应式 contract 通过。

### 提交 9：收敛三进程组合根和 Windows service wrapper

- 把 Web/Broker/Runner 的构造移入 `bootstrap`。
- 三份 Windows SCM wrapper 合并到 `platform/windowsservice`。
- `cmd/*` 仅保留 flags、signal、退出码和 bootstrap 调用。
- 保持三信任边界、服务 SID、Named Pipe DACL 和整体版本 fail-closed 行为。

验收：`go build ./cmd/...`、Windows SCM security gate、Linux build/smoke 和混合协议版本拒绝测试通过。

### 提交 10：删除旧结构并修正文档

- 删除 `internal/app`、所有转发 alias、废弃 helper 和重复测试。
- 为新包添加 `doc.go`，说明职责、信任边界和允许的依赖方向。
- 修复重复 ADR 编号 `0109`、`0128`，同步 `docs/adr/README.md`；只重编号后创建的冲突项，保持 Git 历史可追踪。
- 更新 `CONTEXT.md`、`docs/DATA-MODEL.md`、`docs/RELEASING.md`、开发说明和架构图。
- development installer workflow 增加既定合同 smoke test。

验收：仓库中不存在 `internal/app` import；ADR 编号唯一；文档和代码目录一致。

### 提交 11：完整门禁与发布候选证据

- 执行第 9 节全部本地和 CI 门禁。
- 对生产等价 State Root 副本执行 Registry 迁移演练和恢复演练。
- 对 Windows 与至少两个 systemd Linux 环境执行安装、整体升级、失败回滚、三服务逐一崩溃恢复和卸载无残留测试。
- 生成最终 diff、依赖图、迁移报告和 release note 草稿。

验收：无豁免失败、无未解释快照变化、无中间兼容层。

## 8. 测试策略

- 以领域 service 的公共行为作为主要测试面；不要继续通过巨型 `App` fixture 间接测试所有行为。
- 数据库测试使用临时真实 SQLite 和历史 schema fixture，不用内存 map 假装 SQL 事务。
- IPC 使用生产 codec + 内存 transport adapter；协议版本、身份校验、超时和 Broker 不可用必须覆盖。
- HTTP 测试覆盖 method、auth、CSRF、host/origin、内容类型、错误映射和审计 actor。
- Playwright 只覆盖关键用户旅程和前后端合同，不重复 Go 领域测试。
- 迁移前先写 characterization tests；新模块测试通过后删除旧包测试，避免永久双测。
- Registry 和外部 invocation 使用可控 failpoint，逐一模拟写盘、事务、IPC、进程终止和重启。

## 9. 必须通过的门禁

本地可执行门禁：

```powershell
gofmt -w <本次修改的 Go 文件>
go test ./... -count=1
go vet ./...
go build ./cmd/...
git diff --check
```

Linux/安全门禁：

```bash
bash ./scripts/run-race-security-gate.sh
bash ./scripts/run-fuzz-security-gate.sh
```

浏览器门禁：

```powershell
Set-Location integration/browser
pnpm install --frozen-lockfile
pnpm exec playwright install chromium
pnpm test
```

CI 必须完整通过 `.github/workflows/ci.yml` 和 `.github/workflows/security.yml`，包括 Windows Go、race、Chromium、三服务 SCM 安全矩阵、fuzz、govulncheck、gitleaks、SBOM 和 CodeQL。正式 Tag 还必须在目标 Tag commit 上重新通过 `.github/workflows/release.yml` 的全部门禁；不得用功能分支历史结果替代。

## 10. 量化完成标准

全部条件同时满足才算完成：

- `internal/app` 目录和 import 数均为 0。
- `internal/web` 对 `database/sql` 的直接 import 和直接 SQL 调用均为 0。
- `cmd/scriptboard-broker`、`cmd/scriptboard-runner` 不再构造数据库或领域 manager。
- 三个进程只有 `internal/bootstrap` 是跨领域组合根；架构测试阻止依赖逆流。
- schema 和 migration 只有一个有序 registry，所有历史版本升级测试通过。
- Registry 主密钥不在 State Root、备份或日志中；故障注入证明配置/凭据最终一致。
- 外部动作 completion 失败可观测、可幂等恢复，动作不会因记录失败被重复执行。
- development installer 的非安装合同由 workflow 自动验证。
- RouteSpec、权限、配置、IPC 和资源快照的所有差异都有明确评审记录。
- `App.Config` 式平铺巨型配置和 MFA/Passkey 可选 type assertion 均消失。
- 新增包都有 package documentation；ADR 编号无重复。
- 全量 Go、race、fuzz、browser、安全扫描、Windows SCM 和 Linux smoke 门禁通过。
- 没有 TODO 形式的迁移债、临时 alias、双实现或“下一次再删”的兼容层。

## 11. 合并、回滚与发布

### 合并前

- 保留上述细粒度提交，便于 review 和二分；PR 只能在提交 11 完成后标记 ready。
- 使用 merge commit 或平台保护规则允许的等价原子合并；不得分批 cherry-pick 到 `dev`。
- 若任一门禁失败，在功能分支修复并重跑相关矩阵，不通过 feature flag 绕过架构迁移。

### 合并后、发布前

- 在 `dev` 再跑一次完整 CI 和关键生产等价迁移演练。
- 按需更新面向用户的 README 和 release notes。
- 从 `dev` 创建 `release/X.Y.Z`，在同一提交打 `vX.Y.Z` Tag，严格使用现有 release workflow。

### 发布后

- 重点观察 Registry migration/reconciliation、外部 invocation backlog、三服务 IPC 协议拒绝和数据库 migration 指标。
- 正式 release 分支和 Tag 不可修改。若发现问题，回到 `dev` 修复、测试并发布更高版本；不得移动 Tag 或热改 release 分支。
- 数据迁移必须 forward-safe。回滚旧二进制前先验证其 schema/IPC 兼容性；不兼容时使用新版本修复，不允许旧进程读写新 schema。

## 12. Review 清单

每个提交 reviewer 都必须回答：

- 这个模块隐藏了什么复杂性，调用方是否比迁移前更简单？
- 接口是否由使用方需要定义，而不是照搬底层数据结构？
- 状态、校验、事务和错误语义是否仍由同一个模块拥有？
- 是否引入新的跨层依赖、秘密越界或运行时可选能力？
- 测试是否验证行为和故障模式，而不是只验证文件搬家？
- 删除旧实现后是否仍有唯一事实来源？

只有答案均明确且第 10 节全部满足，才允许把这次“全量修改”视为一次性完成。
