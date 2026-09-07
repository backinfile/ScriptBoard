# ScriptBoard 实例嵌入测试报告

日期：2026-09-07

## 测试范围

- 基础访问：父实例登录页、登录、目标实例登录页及登录后配置页。
- 默认禁止嵌入；配置精确 HTTP/HTTPS 父 Origin 后允许嵌入。
- YAML、环境变量、重复 CLI 参数优先级与非法 CSP/通配符来源拒绝。
- HTTP 与 HTTPS 登录 Cookie；默认策略保持不变，启用嵌入的 HTTPS 使用 Secure + SameSite=None。
- 实际跨站 iframe 内登录、目标导航与刷新后登录保留。
- 嵌套自定义页签同时保留 frame-ancestors 与目标 frame-src。
- 即使父 Origin 受信任，跨来源管理 POST 仍返回 403。
- 原有自定义页签、可信代理及来源校验回归。

## 保留部署

从本次 worktree 构建新的 scriptboard.exe，使用独立状态目录启动两个实例；原有部署未修改。

| 实例 | 地址 | PID |
| --- | --- | --- |
| 父实例 | https://localhost:29787 | 22908 |
| 被嵌入实例 | https://127.0.0.1:29788 | 49848 |

两个实例账号均为 admin，测试密码均为 Local embedding validation 2026!。
本地自签名证书仅用于测试；自动化显式忽略本地证书错误。手动浏览时需信任该证书。

目标实例配置 frame_ancestors: ["https://localhost:29787"]。父实例保留已启用的 Embedded ScriptBoard HTTPS 页签，模式为 target_state。

部署文件、日志、状态、证书、浏览器脚本及截图位于本次 worktree 的 .scratch/embedding-test/；测试数据与进程保留。

## 验证结果

- 新增配置与四种默认/启用嵌入 × HTTP/HTTPS 组合回归通过。
- 外部 Microsoft Edge（Playwright，headless，允许第三方 Cookie）完成实际两个实例间跨站登录、配置页导航、刷新和 Cookie 属性检查：6 项通过。
- 已人工查看 embedded.png，目标监控页正常显示在父实例页签内。
- 初次广泛测试中 cmd/scriptboard 的一项既有测试因 Windows 临时 SQLite 文件占用在清理时失败；单项复跑通过。

最终整包复查：`go test ./internal/config ./internal/web ./internal/bootstrap ./cmd/scriptboard` 全部通过；`git diff --check` 通过。

同步 dev 后，嵌入、自定义页签、文件跳转、可信代理和来源检查的针对性测试通过。整包同步复查中 internal/bootstrap、cmd/scriptboard、internal/workbench 通过；internal/web 的 TestResetAdminCredentialsRejectsInvalidUsername 在临时 SQLite 清理时遇到 Windows 文件占用，单项复跑通过。此前整包完整运行已全部通过，未发现嵌入功能断言失败。

## 使用限制

浏览器禁止第三方 Cookie 时跨站登录可能受限；可在新窗口打开。两个实例应使用不同主机名以避免 Cookie 冲突。跨站登录使用 HTTPS，同站 HTTP 保留支持。自定义页签沿用现有 sandbox；本次验证账号密码登录，不提供实例间单点登录。
