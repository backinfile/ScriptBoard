# 文件与目录权限本地部署测试报告

测试时间：2026-08-27（Asia/Shanghai）

测试分支：`codex/set-file-permissions`

基线提交：`21f14b489154a7f1ef9534a09b2534afa9f06aaf`

结论：**通过**。文件和目录菜单均提供“设置权限”；当前 Windows 部署能读取所有者、继承和 DACL，所有者编辑默认收起，并完成一条直接 Allow ACL 的真实写入与回读。

## 1. 测试条目

1. 登录页、认证跳转和文件页基础访问。
2. CSP、MIME 嗅探防护和 Frame 防护响应头。
3. 文件与文件夹更多操作中的“设置权限”入口。
4. Windows 平台标识、所有者、DACL 和继承控件。
5. 所有者编辑区默认收起。
6. 为当前测试身份写入直接 Full control Allow 规则，并重新读取确认。
7. 旧 `toggle-executable` 路由退役，执行位只由统一权限矩阵写入。
8. 非提升便携进程的所有者写入按 Windows 实际令牌权限拒绝，并返回可解释错误。
9. Windows Host Files 权限读取/写入单元测试、Broker 协议边界、Web 路由与模板回归。
10. Linux POSIX 模式、递归受限条目预检测试及 Linux amd64 交叉编译。
11. 全仓测试、`go vet` 和差异格式检查。

## 2. 最终部署

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署，当前用户身份 |
| 地址 | `http://127.0.0.1:18839` |
| 进程 | PID `38964`，报告生成时仍在监听 |
| 部署目录 | `.scratch/file-permissions-final-local-20260827` |
| State Root | `.scratch/file-permissions-final-local-20260827/state-unified` |
| 测试数据 | `.scratch/file-permissions-host-data-20260827` |
| 管理员 | `admin`；凭据只保留在隔离 State Root 和本地测试会话中 |
| 标准错误 | 0 字节 |

部署、State Root、测试目录及最终进程均保留。较早的 `127.0.0.1:18837`、`127.0.0.1:18838` 验证实例也未被清理或改写。

## 3. 部署态 HTTP 验收

| 条目 | 结果 |
| --- | --- |
| `GET /login` | 200 |
| 管理员认证 | 通过，最终进入 `/monitor` |
| 主机文件页 | 200 |
| 文件菜单权限入口 | 通过 |
| 文件夹菜单权限入口 | 通过 |
| 权限任务页 | 200，`data-platform="windows"` |
| Windows ACL 列表 | 通过 |
| 继承开关 | 通过 |
| 所有者默认收起 | 通过，`details` 不含 `open` |
| 直接 ACL 写入 | 通过；提交后回到 `/resources/files` |
| ACL 回读 | 通过；当前身份的直接 Full control 规则可见 |
| 旧执行位路由 | `POST /resources/files/toggle-executable` → 404 |
| 安全响应头 | CSP、`X-Content-Type-Options`、`X-Frame-Options` 均存在 |
| 服务标准错误 | 0 字节 |

部署态验证使用直接 HTTP 会话完成，没有使用应用内浏览器。外部 Chrome 对无需认证的设计预览已用于前序视觉确认；最终受保护页面没有向浏览器传递隔离部署密码。

## 4. 平台与安全边界

- Windows 所有者写入路径已实现，但非提升便携部署对测试目录执行 `SetOwner` 时由操作系统返回 `Access is denied`；界面保留原所有者且展示明确错误。受管部署由特权 Broker 的实际服务身份决定最终能力。
- Windows ACL 写入在同一便携部署中真实成功，证明 DACL 读取、更新与回读链路完整。
- Broker 拒绝混合 POSIX/Windows 字段、零权限 ACE、任意访问掩码、缺少主体的规则和操作禁用字段。
- Linux 递归模式在任何写入前拒绝符号链接、特殊文件、受保护路径和超过 10,000 项的树；中途失败回滚已应用项。

## 5. 自动化回归

| 命令 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./internal/privilegebroker ./internal/web ./internal/hostfiles -count=1` | 通过 |
| Linux amd64 交叉编译 Host Files、Broker、Web 测试包 | 通过 |
| `git diff --check` | 通过 |
