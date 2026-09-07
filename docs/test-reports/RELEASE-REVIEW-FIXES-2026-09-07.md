# Release 后审查修复测试报告

日期：2026-09-07。环境：Windows、Go、外部 Microsoft Edge（headless）。基线：`dev` / `d196d22`。

## 修复内容

- 新建灵感空间的响应丢失后，重试同一请求可确认已保存的内容，不重复创建、不产生版本字段引起的冲突。真实的并发改名或模块编辑仍会提示冲突。
- 启用嵌入的 HTTPS 实例使用 `SameSite=None; Secure; HttpOnly` 保存语言选择。默认部署及 HTTP 保留原有 Cookie 策略。
- 同步更新中英文 README。

## 测试条目与结果

| 条目 | 结果 |
| --- | --- |
| 修复前回归 | 空空间、含模块空间的创建重放均返回 `board:created`；嵌入 HTTPS 语言 Cookie 错误返回 Lax，测试按预期失败 |
| 修复后创建重放 | 空空间和含模块空间均通过；不新增重复空间、不增加修订号；后续真实并发修改受到保护 |
| 嵌入配置矩阵 | 默认/启用嵌入 × HTTP/HTTPS 四组通过，覆盖登录、语言 Cookie、嵌套页签及跨来源 POST 拦截 |
| 完整 Go 包回归 | `internal/workbench`、`internal/config`、`internal/store/migrations`、`internal/web`、`internal/bootstrap` 通过 |
| 本地构建 | `go build -o .scratch/review-test/scriptboard.exe ./cmd/scriptboard` 通过 |
| 基础访问 | 登录页、CSS、JavaScript 返回 200；匿名空间状态请求跳转登录；登录后监控、账户、文件页可访问 |
| 真实响应丢失 | 外部 Edge 将成功保存的新建 PATCH 响应丢弃；重试后无冲突、无重复；继续添加笔记并刷新，内容保留 |
| 明文 HTTP | 启用嵌入时语言 Cookie 仍为 Lax，未设置 Secure |
| 真实跨站 iframe | 真实父页面 `http://localhost:29880` 嵌入 `https://127.0.0.1:29882`；登录页切换中文后重载、登录、导航均保留；登录后切换英文并重载成功 |
| 差异检查 | `git diff --check` 通过 |

首次整包运行遇到 `TestMCPProtectedResourceMetadataUsesCanonicalResource` 的 Windows 临时 SQLite 文件占用清理失败，未出现功能断言失败。随后 `go test ./internal/web -parallel 4 -count=1` 全包通过（106.641 秒）。

首次 iframe 脚本通过路由拦截模拟父页面，触发 Edge 本地网络访问限制。改为真实本地父页面服务器后通过；未禁用浏览器网络访问检查。自签名证书仅用于本地测试，浏览器测试显式忽略证书错误。

## 保留部署

| 实例 | 地址 | PID |
| --- | --- | --- |
| HTTP | http://127.0.0.1:29881 | 62548 |
| HTTPS | https://127.0.0.1:29882 | 73044 |

账号均为 `admin`，密码均为 `Review fixes local password 2026!`。

部署、证书、日志、状态、截图及测试脚本位于：
`D:\Github\worktrees\ScriptBoard\review-fixes-20260907\.scratch\review-test`。

- `browser.cjs`：外部 Edge 回归脚本；`results.json`：通过结果。
- `retry-saved.png`、`embedded-language.png`：测试截图。
- `http-state`、`https-state`：独立状态目录，保留测试空间和笔记。
- 父页面仅作浏览器测试夹具，脚本结束时关闭。两个 ScriptBoard 实例继续运行，因此保留其 worktree。
