# 全量代码审查修复与简化计划

状态：已实施并完成本地验收；Race 门禁待 CI 的 CGO Runner 执行

制定日期：2026-08-27

基线分支：`dev`
重点审查范围：`v2.4.0..v2.4.7`，并补充全仓结构扫描

## 实施结果（2026-08-27）

本轮已完成七个审查问题的修复与回归测试，并完成两项结构简化：

- 新增 `internal/mcpcommand` 深模块，集中拥有 MCP 写命令的持久化 claim、过期续租、完成结果缓存和结果未知保护；Web 层不再直接读写 `mcp_idempotency`。
- OAuth 撤销改为事务操作，真实存储故障会返回错误；Refresh 消费和授权码消费同时补齐条件更新与影响行检查。
- CIMD 删除独立 IP 分类和拨号策略，统一复用 `internal/outboundpolicy`。
- 当前授权范围成为 Access/Refresh Token 的即时能力上限；所有凭据熵源错误均 fail closed 并回滚事务。
- Quick Run 分页以已扫描数据库行推进游标，并为单次请求设置 1000 行扫描上限。
- 限流器增加 IPv6 `/64` 归一化、空闲回收和 4096 个桶的硬上限。
- Web、Broker、Runner 的 Windows SCM 生命周期统一由 `internal/platform/windowsservice` 管理。

验证结果：全仓非缓存测试、vet、原生及 Windows 交叉构建、模块完整性、补丁检查、5 组 30 秒安全 fuzz 和外部 Chromium + HTTP 黑盒 55 项全部通过。本地部署保留在 `127.0.0.1:18794`；详细证据见 `docs/test-reports/CODE-REVIEW-REMEDIATION-LOCAL-DEPLOYMENT-2026-08-27.md`。

当前 Windows Go 工具链未启用 CGO，`go test -race` 无法执行，必须由 CI 的 CGO Runner 完成。计划中更大范围的 `mcpaccess.Store` 文件拆分、其他 Web 纵向切片和第三方 MCP 客户端互操作属于后续简化，不与本轮安全修复混合。

## 1. 目标与结论

本计划处理最近 OAuth/MCP 提交中确认的执行安全、撤销可靠性、出站访问、分页和资源治理问题，并继续收敛 Web 层与 Windows 服务生命周期中的重复实现。

实施顺序必须保持为：

1. 先补充可复现的失败测试和故障注入能力。
2. 独立修复三个 P1 问题：MCP 幂等、Token 撤销、CIMD 出站策略。
3. 修复授权范围、分页、安全随机数和限流桶四个 P2 问题。
4. 在行为门禁稳定后深化 MCP 模块，并以小切片继续简化 Web 与 Windows SCM 代码。
5. 完成本地重新部署、逐项黑盒验证并保留部署与测试数据。

安全修复不得与大范围目录移动混在同一提交。每个切片必须可独立编译、测试和回滚。

## 2. 当前基线

审查时确认：

- 工作区干净；创建 worktree 时本地 `dev` 比 `origin/dev` 超前 1 个提交，worktree 基于该本地 `dev`。
- `v2.4.0..v2.4.7` 涉及 71 个文件，约新增 2990 行、删除 98 行。
- `go test ./...`、`go vet ./...`、`go build ./cmd/...` 与 `git diff --check` 通过。
- 禁用测试缓存时，Windows 并行链接曾因系统分页文件不足中断；将失败包改为单并发后全部通过，不属于测试断言失败。
- `internal/web` 当前有 54 个生产 Go 文件、约 499 个 `App` 方法和 114 个直接 SQL 调用点；`internal/web/app.go` 为 5802 行。

## 3. 范围

### 3.1 必须修复

| 编号 | 优先级 | 问题 | 主要位置 |
| --- | --- | --- | --- |
| MCP-01 | P1 | 超过 24 小时的幂等记录仍占用主键，复用相同 `request_id` 后每次重试都可能重复执行 | `internal/web/mcp_backend.go` |
| MCP-02 | P1 | Token 撤销忽略数据库错误，可能返回成功但令牌仍有效 | `internal/mcpaccess/store.go` |
| MCP-03 | P1 | CIMD 使用独立地址判断，遗漏共享出站策略禁止的保留网段 | `internal/mcpaccess/cimd.go` |
| MCP-04 | P2 | 授权范围缩小后，旧 Execute Token/Refresh Token 仍可继续使用 | `internal/mcpaccess/store.go` |
| MCP-05 | P2 | Quick Run 在分页后过滤，无效记录较多时会截断后续有效结果 | `internal/web/mcp_backend.go` |
| MCP-06 | P2 | 授权码和 Token 生成忽略安全随机数错误 | `internal/mcpaccess/store.go` |
| MCP-07 | P2 | 限流桶从不清理，长期运行时状态基数可无界增长 | `internal/mcpaccess/limiter.go` |

### 3.2 结构简化

- 把 MCP 写操作的幂等、动作结果、审计和恢复集中到一个深模块。
- 将 `mcpaccess.Store` 的客户端注册、授权生命周期和调用记录按内部职责拆分，避免继续扩大单文件，但不为单一实现制造无意义接口。
- 以纵向切片减少 Web handler 中的直接 SQL，让领域规则和事务回到拥有该行为的模块。
- 将主 Web 进程仍然重复的 Windows SCM 状态机收敛到 `internal/platform/windowsservice`。
- 删除 CIMD 自建的地址策略与拨号逻辑，复用 `internal/outboundpolicy` 的唯一事实来源。

### 3.3 不在本计划内

- 新增 MCP 工具、Scope 或 OAuth Grant 类型。
- 改变现有角色权限、Quick Run 发布语义或连接安全策略。
- 前端视觉改版。
- 一次性重写全部 Web handler 或替换 SQLite。
- 修改已发布的 release 分支或正式 Tag。

## 4. 目标模块与 seam

### 4.1 MCP 命令模块

新增或深化一个拥有机器写操作完整生命周期的模块。外部接口保持窄，只暴露调用方真正需要的动作：

```go
type CommandModule interface {
	StartQuickRun(context.Context, Principal, StartQuickRunCommand) (StartQuickRunResult, error)
	StopRun(context.Context, Principal, StopRunCommand) (StopRunResult, error)
}
```

该模块的实现必须隐藏：

- 24 小时幂等窗口和原子 claim。
- `processing`、`completed`、`unknown` 等执行状态。
- 动作已经发生但结果保存失败时的 reconciliation。
- 参数摘要、调用记录和审计关联。
- 并发请求、进程中断和重启后的恢复语义。

`mcpserver` 只作为协议 adapter，`web` 只负责装配和错误映射。测试通过同一个命令接口验证可观察结果，不越过接口检查内部步骤。

### 4.2 OAuth 授权模块

OAuth 授权模块拥有客户端、授权、授权码、Token family 和撤销的一致性规则。SQLite 是可本地替代的依赖，测试使用真实临时 SQLite；不新增一方法一接口的 Repository 镜像。

需要保证：

- 授权范围缩小时立即限制旧 Access Token，并阻止旧 Refresh Token 恢复已撤销范围。
- 客户端、授权和 Token family 的撤销在事务中收敛。
- 存储故障与“Token 不存在”是不同错误模式。
- 安全随机数由可注入的内部熵源提供，生产使用 `crypto/rand`，测试可稳定注入失败。

### 4.3 CIMD 出站 adapter

CIMD 属于真实外部依赖。模块接收共享出站 transport adapter；生产 adapter 使用 `outboundpolicy.Policy{}`，测试 adapter 提供受控 DNS、TLS 和响应。

生产行为保持：

- 仅允许 HTTPS。
- 仅允许策略准入的公共地址和端口 443。
- 禁止环境代理、URL 凭据和重定向。
- 固定 DNS 校验后的实际地址，保留 TLS 主机名验证和 TLS 1.2 最低版本。
- 响应体继续限制为 64 KiB，并严格验证 JSON 文档。

## 5. 实施切片

### 切片 1：冻结回归行为

先添加能够在当前代码上失败的测试：

- 幂等记录超过 24 小时后复用，同一轮重试只能执行一次。
- 幂等结果保存失败后，请求进入 `unknown` 或可恢复状态，禁止再次执行动作。
- SQLite 写失败时撤销返回存储错误，旧 Token 不得被错误报告为已撤销。
- CIMD 拒绝 `100.64.0.0/10`、`192.0.0.0/24`、`198.18.0.0/15`、文档网段、元数据地址和私网地址。
- 授权从 Execute 缩小到 Observe 后，旧 Access/Refresh Token 均不能继续获得 Execute。
- 前一批存在大量失效 Quick Run 时，下一批有效项仍能遍历到。
- 熵源失败时不写入授权码、family 或 Token。
- 限流器经历大量过期 key 后，桶数量回落到有界范围。

验收：测试准确描述外部行为；除新增失败用例外，现有测试保持通过。

### 切片 2：修复 MCP 幂等生命周期

- 为幂等记录建立明确状态，不再只保存最终 JSON。
- 使用数据库唯一键和条件更新原子 claim；进程内互斥只能作为优化，不能作为正确性基础。
- 过期记录允许以条件更新方式进入新一代执行，不能被旧主键永久阻挡。
- 已完成请求返回原结果；正在处理或结果未知的请求不得重新执行。
- 动作成功但完成记录失败时，持久化 reconciliation 项并记录审计/指标。
- 定时和启动恢复任务收敛未完成记录；无法证明结果时保持 `unknown`，不得推断为失败。
- 为已存在的 `mcp_idempotency` 数据提供向前兼容 migration。

验收：并发、24 小时过期、数据库短暂失败、进程重启四种场景均证明动作最多执行一次；结果最终完成或明确保持未知。

### 切片 3：修复撤销事务

- Access Token 与 Refresh Token/family 的撤销使用一个事务。
- 检查每个 SQL 操作和 `RowsAffected` 错误。
- 未知 Token 继续返回协议要求的成功响应；真实存储失败返回内部错误并进入审计。
- `RevokeClient`、`RevokeAuthorization` 和管理员撤销同步采用相同的错误处理原则。
- 增加 SQLite busy、关闭连接和事务回滚测试。

验收：接口报告成功后，相关 Access/Refresh Token 必须全部不可用；任何部分写入失败均不留下虚假的成功结果。

### 切片 4：统一 CIMD 出站策略

- 删除 `publicIP` 和 CIMD 自建 DNS/拨号实现。
- 复用 `outboundpolicy.Policy{}.Transport()`，仅在其上配置 TLS 最低版本、ServerName、超时和禁止重定向。
- 为共享出站策略补齐 CIMD 需要的可测试 resolver/transport seam。
- 覆盖 DNS 返回混合公私地址、DNS rebinding、IPv4-mapped IPv6 和非常规端口。

验收：CIMD 与更新下载、Dashboard 等公网客户端使用同一地址准入规则；仓库只保留一个公共出站地址分类实现。

### 切片 5：修复授权范围与安全随机数

- 当前授权范围成为 Access/Refresh 验证的上限。
- 缩小范围时撤销不再满足当前范围的 Token family，或在认证时取 Token 与当前授权范围的交集并禁止 Refresh 恢复旧范围。
- 不允许通过重新授权或 Refresh 扩大范围；扩大范围必须经过新的明确同意和必要的 Step-up。
- 检查每一次授权码、family ID、Access Token 和 Refresh Token 生成错误。
- 将熵源作为模块内部依赖注入，添加逐个失败点的回滚测试。

验收：管理界面显示的当前范围与实际可用能力一致；任一随机数生成失败均不产生持久化凭据。

### 切片 6：修复分页与限流状态

- Quick Run 分页以最后扫描的数据库行推进游标，而不是只依赖最终返回项。
- 循环读取有界批次，直到收满页面或确认到达末尾。
- 保持每页数据库扫描量有上限，避免全部失效记录造成单请求长时间占用。
- 限流桶记录最后活动时间并周期清理；设置全局最大 key 数和确定的溢出策略。
- 来源 key 继续使用可信代理处理后的实际地址，并按 IPv6 策略规范化，避免单个主体轻易制造大量 key。

验收：分页无遗漏、无重复且每次查询有界；限流器在压力测试后的常驻状态量保持在配置上限内。

### 切片 7：深化 MCP 模块

- 将 `internal/mcpaccess/store.go` 按客户端注册、授权生命周期、Token、管理查询和调用记录拆成职责清晰的实现文件。
- 对外保留少量有行为含义的接口，不把 SQL 操作逐一暴露给调用方。
- 把 `internal/web/mcp_backend.go` 的幂等 SQL、审计编排和动作恢复移入 MCP 命令模块。
- 删除被新接口替代的浅测试，在命令模块接口上建立行为测试。

验收：Web adapter 不直接读写 MCP 持久化表；幂等、执行和审计问题集中在一个模块中修改和验证。

### 切片 8：Web 与 Windows SCM 小步简化

- 选择 Quick Run 或 Identity 作为下一条纵向切片，将 handler 内规则和事务移入领域模块。
- 每个切片完成后立即删除旧实现，不保留长期 facade 或双套测试。
- 给 `internal/platform/windowsservice.Run` 增加受限的日志初始化能力，让主 Web、Broker、Runner 共用同一 SCM 状态机。
- 删除 `cmd/scriptboard/service_windows.go` 中重复的启动、停止、超时和状态处理逻辑。

验收：至少减少一组 Web 直接 SQL 调用；三个受管进程只有一份 SCM 生命周期实现；现有服务名、停止超时、日志与退出码保持不变。

## 6. 提交建议

建议使用功能分支 `codex/code-review-remediation`，从最新 `dev` 创建。若使用 worktree，目录为 `../worktrees/ScriptBoard/code-review-remediation`。

建议提交顺序：

1. `test(mcp): capture review regressions`
2. `fix(mcp): make command idempotency recoverable`
3. `fix(oauth): make revocation transactional`
4. `fix(mcp): route CIMD through outbound policy`
5. `fix(oauth): enforce current grants and entropy failures`
6. `fix(mcp): preserve pagination and bound limiter state`
7. `refactor(mcp): deepen command and authorization modules`
8. `refactor(web): remove one direct-SQL vertical slice`
9. `refactor(windows): share managed service lifecycle`
10. `docs(test): record local deployment verification`

每个 bug 修复需按仓库要求在附近添加简短注释，正向说明原问题和修复方式，不保留错误做法的描述。

## 7. 测试与门禁

### 7.1 Go 行为测试

```powershell
go test -count=1 ./internal/mcpaccess ./internal/mcpserver ./internal/runcontrol ./internal/web
go test -count=1 ./internal/outboundpolicy ./internal/platform/windowsservice
go test ./... -count=1
go vet ./...
go build ./cmd/...
git diff --check
```

Windows 内存紧张时允许使用 `go test -p 1 ./... -count=1`，但 CI 仍须通过正常并发配置。

### 7.2 并发与安全门禁

```bash
bash ./scripts/run-race-security-gate.sh
bash ./scripts/run-fuzz-security-gate.sh
```

至少新增或扩展以下 race/fuzz 目标：

- 同一 `request_id` 并发 Start/Stop。
- Refresh reuse 与撤销并发。
- 限流桶清理与 Acquire 并发。
- CIMD 主机、端口、IPv4/IPv6 地址分类。

### 7.3 浏览器与协议测试

- OAuth 登录恢复、同意、拒绝、Step-up 和撤销。
- DCR、CIMD、PKCE、授权码重放、Refresh rotation/reuse。
- Observe/Execute 工具目录和隐藏工具调用。
- Quick Run 重叠确认、幂等重试、停止权限和分页。
- MCP Inspector、Codex 与至少一个通用 DCR 客户端互操作。

### 7.4 本地重新部署

修改完成后严格执行仓库本地部署流程：

1. 先列出 MCP/OAuth、基础登录、概览、文件、Quick Run 和审计测试项。
2. 重新本地部署并获取登录用户名与一次性密码。
3. 使用外部浏览器或 HTTP 客户端逐项测试，不使用内部浏览器。
4. 可创建并保留测试用户、OAuth 客户端、Quick Run、Run 和审计数据。
5. 测试结束后保留当前部署。
6. 在 `docs/test-reports/` 生成带日期的部署测试报告。

## 8. 量化完成标准

全部条件满足才可合并：

- 七个审查问题都有先失败后通过的回归测试。
- 相同 MCP `request_id` 在并发、过期、数据库失败和重启场景下均不会重复动作。
- 撤销成功后所有相关 Token 立即失效；存储失败不会伪装成成功。
- CIMD 不再维护独立 IP 分类函数，并通过共享出站策略全部安全语料。
- 当前授权范围与实际 Token 能力一致。
- 所有安全随机数错误均 fail closed，事务无残留。
- Quick Run 全量遍历无遗漏或重复。
- 限流 key 数量存在自动化验证的硬上限。
- Web 不再直接读写 `mcp_idempotency` 和 `mcp_invocations`。
- 三个 Windows 受管进程共用同一 SCM 生命周期实现。
- 全量 Go、vet、build、race、fuzz、浏览器和本地部署门禁通过。
- 测试报告、必要 ADR、`CONTEXT.md`、README/Release Notes 按用户可见影响同步更新。

## 9. 回滚与发布

- 数据库 migration 必须向前安全；上线前使用生产等价 State Root 副本演练升级和恢复。
- 幂等状态 migration 失败时必须保持旧表和旧数据完整，应用启动 fail closed。
- 任一安全切片失败，只回滚对应功能分支提交，不回退已发布 Tag。
- 所有修复先合并到 `dev` 并完成测试；需要发布时再从 `dev` 创建新的 `release/X.Y.Z` 和更高版本 Tag。
- 发布任务必须主动检查并遵循现有 `.github/workflows/release.yml`，不得修改既有 release 分支或正式 Tag。

## 10. Review 清单

每个切片合并前必须回答：

- 模块接口是否隐藏了幂等、事务、恢复或地址策略复杂度？
- 同一问题是否只需在一个模块内修改和验证？
- 测试是否从模块接口验证可观察行为，而非内部 SQL 步骤？
- 是否新增了只有一个 adapter 的假想 seam？
- 是否处理了数据库失败、并发、取消、进程中断和重启？
- 是否删除了被替代的重复实现与测试？
- 是否保持 SSL/TLS、明文连接和显式跳过证书验证的既有连接兼容性？
