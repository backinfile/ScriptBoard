# AI 助手功能移除本地部署测试报告

测试时间：2026-08-25（Asia/Shanghai）

测试分支：`codex/remove-ai-assistant`

基线提交：`28dba7b`

结论：**通过**。AI 助手的 Web 入口、运行时、Provider 凭据域、独立 Host、发布物和活动文档已移除；新数据库不再创建 Assistant 表，旧 State Root 中已有的 Assistant 表只保留升级兼容性，不再有可调用功能。

## 1. 测试清单

1. 登录页、未认证重定向与管理员登录。
2. 监控、文件、数据库、账户设置和更新设置基础访问。
3. 主导航不再显示 AI 助手入口。
4. 设置导航不再显示 AI 设置入口。
5. `/ai` 与 `/settings/ai` 返回 404。
6. 部署目录不包含 `scriptboard-ai-host`。
7. 服务日志不再提供 AI Host 筛选项。
8. Windows 与 Linux 升级路径清理遗留 AI 服务、socket 和防火墙规则。
9. 新数据库不创建 Assistant 表，旧 Assistant schema 仍可升级。
10. 发布、安装、更新、Go 全仓测试、Linux 交叉编译与 Chromium 门禁。

## 2. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18841` |
| 进程 | PID `6716`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-ai-removal-28dba7b` |
| State Root | `.scratch/local-deploy-ai-removal-28dba7b/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 2 个事件；哈希链与外部签名 checkpoint 有效 |

最终代码重新构建后复用了同一 State Root 完成再次部署。部署、登录审计、探针脚本和 JSON 结果均保留；报告生成后未停止进程。

## 3. HTTP 部署验证（13/13）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | `GET /login` | 通过，200 |
| 2 | 管理员登录 | 通过，进入 `/monitor` |
| 3 | `/monitor` | 通过，200 |
| 4 | `/resources/files` | 通过，200 |
| 5 | `/resources/databases` | 通过，200 |
| 6 | `/settings/account` | 通过，200 |
| 7 | `/settings/updates` | 通过，200 |
| 8 | 主导航 AI 入口 | 通过，不存在 |
| 9 | 设置导航 AI 入口 | 通过，不存在 |
| 10 | `/ai` | 通过，404 |
| 11 | `/settings/ai` | 通过，404 |
| 12 | AI Host 二进制 | 通过，不存在 |
| 13 | 初始管理员凭据 | 通过，仅保留在私有文件中 |

保留证据：

- `.scratch/local-deploy-ai-removal-28dba7b/deployment-ai-removal-probe.ps1`
- `.scratch/local-deploy-ai-removal-28dba7b/deployment-ai-removal-probe.json`
- `.scratch/local-deploy-ai-removal-28dba7b/stdout.log`
- `.scratch/local-deploy-ai-removal-28dba7b/stderr.log`

## 4. 自动化回归

| 项目 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过；最终变更后 Web 包独立重跑通过 |
| 完整 `pnpm test` Chromium 门禁 | 通过 |
| Linux amd64 `internal/platformservice` 测试二进制交叉编译 | 通过 |
| 遗留 Linux AI systemd 单元清理回归 | 通过 |
| 遗留 Windows AI 服务与防火墙规则清理回归 | 通过 |
| 新数据库无 Assistant 表 | 通过 |
| 旧 Assistant schema 升级兼容 | 通过 |
| `git diff --check` | 通过 |

Chromium 门禁首次运行暴露三处基线断言漂移：数据库共享标题已由 “Database backups / 数据库备份” 统一为 “Databases / 数据库”，Quick Runs 标题区也已是 3 个操作而非 4 个。服务端模板与既有 Go 契约均证明当前产品行为正确，因此只更新过期的浏览器期望值；未以此改变数据库或 Quick Runs 行为。

## 5. 保留状态

- 部署继续监听 `127.0.0.1:18841`，PID 为 `6716`。
- 测试通过 HTTP 接口和仓库安装的外部 Playwright Chromium 完成，没有使用应用内浏览器。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
- 测试数据保留为同一 State Root 中的管理员登录审计记录和探针结果。
