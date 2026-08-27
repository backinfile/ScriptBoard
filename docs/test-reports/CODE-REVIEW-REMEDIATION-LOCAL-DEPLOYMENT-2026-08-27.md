# 代码审查修复本地部署验收 — 2026-08-27

## 结论

- 外部 Playwright Chromium + HTTP 黑盒套件：55 项通过，0 项失败。
- `go test -p 1 ./... -count=1`、`go vet ./...`、原生构建、Windows 交叉构建、`go mod verify` 与 `git diff --check` 全部通过。
- 5 个既有安全 fuzz 目标各运行 30 秒并通过。
- 当前 Windows Go 工具链未启用 CGO，Race 门禁未执行，不计为通过；需由 CI 的 CGO Runner 执行。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18794` |
| 进程 | `scriptboard-final.exe`，PID `30504` |
| 监听 | 仅 `127.0.0.1:18794` |
| 部署目录 | `D:\Github\worktrees\ScriptBoard\code-review-remediation\.scratch\code-review-remediation-20260827` |
| 登录用户 | `admin` |
| State Root | `.scratch/code-review-remediation-20260827/state-final` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password`，未写入报告或测试结果 |
| 结构化结果 | `.scratch/code-review-remediation-20260827/artifacts-final/blackbox-results.json` |
| 浏览器证据 | `oauth-execute-consent.png`、`account-agent-connections-after-revoke.png` |

部署、OAuth 客户端、授权、Quick Run、Run、日志和审计测试数据均保留，进程在测试结束后继续运行。

## 测试清单

| 范围 | 结果 |
| --- | --- |
| 基础访问 | 登录页 200、匿名重定向、安全响应头、未知路径与异常请求目标处理通过 |
| 网络边界 | 仅回环监听，不允许的 Host 返回 421，通过 |
| OAuth 发现与 DCR | 资源/授权服务器元数据、PKCE S256、公开客户端注册和非法元数据拒绝通过 |
| 授权生命周期 | 登录恢复、同意/拒绝、授权码交换与重放拒绝、Refresh 轮换/复用检测通过 |
| 撤销 | 用户撤销授权后旧 Access Token 立即失效，通过 |
| MCP | 无凭据/无效 Bearer、工具目录 Scope 过滤、初始化、Host 状态和请求体上限通过 |
| Quick Run | 创建发布、列表、启动、重叠确认、停止、request_id 幂等、Run 查询和日志上限通过 |
| 浏览器 | 外部 Chromium 登录、授权同意页、账户 Agent connections、控制台零错误通过 |

完整 55 项逐条结果保存在上述结构化结果文件中。测试脚本只在进程内持有密码、Cookie、授权码和 Token，结果及截图不包含这些秘密。

## 回归与故障注入

本轮新增自动化覆盖：

- 幂等记录过期后的原子续租、并发 claim、完成记录写失败后禁止重复动作。
- 撤销事务写失败回滚，以及 HTTP 层真实存储错误返回 500。
- CIMD 对私网、回环、链路本地、共享地址、基准测试网段和文档网段的统一拒绝。
- 授权范围缩小后旧 Access/Refresh Token 立即失效。
- 授权码、Token family、Access/Refresh Token 熵源失败时不留下持久化凭据。
- 前置失效记录不再导致 Quick Run 分页漏项。
- 限流桶空闲回收、硬上限与 IPv6 `/64` 归一化。
- Windows 三个受管进程共享同一 SCM 生命周期，并通过 Windows 交叉构建。

## 执行命令

```powershell
go test -p 1 ./... -count=1
go vet ./...
go build ./cmd/...
go mod verify
git diff --check

$env:GOOS='windows'
$env:GOARCH='amd64'
go build ./cmd/...
```

Race 门禁在当前环境返回 `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`。本报告保留该限制，不以普通测试替代 Race 结果。
