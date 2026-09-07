# 首次管理员设置测试报告

日期：2026-09-07。环境：Windows、Go、外部 Chrome/Chromium；独立 worktree `codex/first-admin-setup`。

## 实现范围

- 新安装通过本机初始化令牌设置用户名与密码，保存后自动登录。
- 令牌仅保存摘要到数据库，有效期 24 小时，未完成设置时重启轮换。URL 使用 fragment，页面读取后清除。
- SQLite schema 68 单独记录待初始化状态，令牌消费与账号更新在同一事务完成。
- 已有 schema 67 账号升级后直接登录；显式密码文件配置可跳过设置；本机账号恢复继续可用。
- 保留 Argon2id、密码策略、多用户角色、MFA、CSRF、限流和审计。

## 验证记录

| 项目 | 结果 |
| --- | --- |
| 首次设置七组集成测试 | 通过：初始化与自动会话、重放、过期与轮换、并发、升级与配置、限流与数据库故障、HTTPS Cookie、密码策略、事务回滚、用户名覆盖 |
| 外部 Chrome 首次设置 | 通过：令牌自动填充与 URL 清除、错误重试、自动登录、文件删除、设置后常规登录 |
| 中英文与移动布局 | 通过：1280px 桌面、390px 移动，无横向溢出；截图已检查 |
| 无 JavaScript 设置 | 通过：英文表单手动输入令牌后进入应用 |
| 最终构建重启与 HTTP 访问 | 通过：账号保留、设置页转登录、监控/账户/文件/静态资源访问 |
| 仓库 Chromium 桌面门禁 `npm test` | 通过，包含既有浏览器契约与业务页面验证 |
| `go vet ./...` | 通过 |
| `go build ./cmd/...` | 通过 |
| 完整 Go 回归 | 全部功能断言通过；一次 Windows 临时 SQLite 文件清理失败，单项复测通过。`go test ./internal/web -parallel 4 -count=1` 全套复测通过（169.714 秒）。 |

设计检测只报告共享样式中既有的规则告警；本次沿用现有登录页视觉，未扩展修改无关页面。Linux 测试、Linux race 门禁和 Windows SCM 安装门禁未在本机执行，也未触发远程 CI。

## 保留的测试部署

- 主实例：<http://127.0.0.1:5066>；账号 `setup-owner`；密码 `ScriptBoard Setup Test 2026!`。
- 无 JavaScript 验证实例：<http://127.0.0.1:5067>；账号 `manual-owner`；密码 `Manual Setup Passphrase 2026!`。
- 两个实例都已完成初始化；访问 `/setup` 会转到登录页。
- 最终程序、配置、PID、数据、日志、截图和复现脚本位于 `D:\Github\worktrees\ScriptBoard\first-admin-setup\.scratch\first-admin-tests`，测试数据保留。
- 截图：`setup-desktop.png`、`setup-mobile.png`、`setup-complete.png`；结果：`browser-result.json`、`restart-http.json`。

## 集成状态

主工作区正在修改 README、共享 CSS/JavaScript 等相同文件，本功能保留在独立 worktree 和分支，暂不合并，避免覆盖进行中的工作。
