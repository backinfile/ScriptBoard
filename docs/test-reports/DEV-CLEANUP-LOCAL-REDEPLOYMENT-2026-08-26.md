# dev 全量收尾与本地重新部署测试报告

测试时间：2026-08-26（Asia/Shanghai）

测试分支：`dev`

部署提交：`db3e72c`

## 结论

通过。遗留的两份本地测试报告已提交到 `dev`；所有本地功能分支均已进入 `dev`，已合并功能分支与 Git worktree 注册均已清理。当前源码通过全仓 Go 测试和完整 Chromium 浏览器门禁，并已使用新的独立 State Root 重新部署。真实管理员登录、核心页面、静态资源、Doctor 与审计链校验均通过，部署保持运行。

## 合并与清理

| 项目 | 结果 |
| --- | --- |
| 遗留测试报告 | 已提交，提交为 `db3e72c docs(test): retain local deployment reports` |
| 未合并本地分支 | 0 |
| Git worktree | 只保留 `D:\Github\ScriptBoard` 的 `dev` worktree |
| 已合并功能分支 | 本地 `codex/`、`feat/`、`feature/` 分支已删除 |
| 历史归档分支 | `archive/release-v2.0.16` 无净文件差异，已删除本地分支 |
| 旧测试部署 | 10 个 ScriptBoard 实例及配套测试 mock 已停止 |

Git 已解除全部旧 worktree 注册。Windows 文件清理边界仍留下五个不受 Git 管理的旧目录；这些目录不包含未合并提交、不再关联分支，也没有运行中的进程：

- `D:\Github\worktrees\codebase-restructure`
- `D:\Github\worktrees\http-clipboard-fallback`
- `D:\Github\worktrees\ScriptBoard\copy-tooltip-layer`
- `D:\Github\worktrees\ScriptBoard\remove-ai-assistant`
- `D:\Github\worktrees\ScriptBoard-security-hardening`

## 测试清单与结果

| 序号 | 测试项 | 结果 |
| --- | --- | --- |
| 1 | `go test ./... -count=1` | 通过；全部 Go 包通过，`internal/web` 约 118 秒 |
| 2 | `npm test`（`integration/browser`） | 通过；完整契约测试和 Chromium desktop gate 通过 |
| 3 | 当前源码构建 | 通过；生成 47,341,056 字节 Windows 二进制 |
| 4 | 根路径 | 通过；返回 303 并跳转 `/login` |
| 5 | 登录页 | 通过；返回 200 |
| 6 | 真实管理员登录 | 通过；使用生成的一次性密码进入 `/monitor` |
| 7 | 核心页面 | 通过；Monitor、Files、Quick Runs、Runs、Databases、External Interfaces、Account 均返回 200 |
| 8 | 静态资源 | 通过；`app.css` 与 `app-v2.js` 均返回 200 |
| 9 | Doctor | 通过；State Root、密钥、SQLite integrity/WAL/schema 59、Run 日志、执行器和监听端口均为 OK |
| 10 | 审计校验 | 通过；2 条事件、签名 checkpoint 有效、哈希链有效 |

浏览器门禁第一次与 Go 全量测试并发运行时，在 Clipboard fallback 合约的 3 秒点击等待处超时；Go 测试结束后独立完整复跑通过，未发现产品回归。

## 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 隔离便携部署 |
| 地址 | `http://127.0.0.1:18788` |
| PID | `42936` |
| 二进制 | `.scratch/local-deploy-dev-clean-20260826/scriptboard-dev.exe` |
| State Root | `.scratch/local-deploy-dev-clean-20260826/state` |
| 管理员 | `admin` |
| 密码位置 | State Root 私有文件 `secrets/initial-admin-password` |
| 数据库 schema | 59 |

## 保留状态

- 新部署继续监听 `127.0.0.1:18788`。
- 新 State Root、管理员凭据、SQLite 数据库和测试产生的审计数据均保留。
- 初始管理员密码未写入报告或版本库，仅保存在 State Root 的私有秘密文件中。
