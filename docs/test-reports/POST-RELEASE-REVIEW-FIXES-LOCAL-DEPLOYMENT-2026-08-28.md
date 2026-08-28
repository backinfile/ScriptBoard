# 上次发布后 Review 修复本地部署测试报告

测试时间：2026-08-28（Asia/Shanghai）

测试分支：`codex/fix-post-release-review`

修复提交：`9cf234d7c45a7d21fec434dafe005e0829cef077`

Review 范围：`v2.4.7`（`14a4dd209517dd268b27e1e946dc704ab9ca3e13`）之后、`dev` 截止 `2b15adc` 的 11 个提交。

结论：**通过**。Review 发现的 5 个问题均已修复并通过回归测试；最终 Windows 便携部署完成外部 Chromium 与 HTTP 验收，部署、State Root 和测试数据均保留。

## 1. 测试条目

1. 登录页、受保护路由跳转和管理员认证。
2. CSP、MIME 嗅探防护和 Frame 防护响应头。
3. Windows 权限表单只提交一个 `inheritance_enabled` 字段。
4. 目录继承先关闭、再通过表单开启，并回读确认保持开启。
5. 为当前 Windows 身份写入只适用于文件子项的直接 Read ACE，并回读确认范围仍为 `files`。
6. 从计划任务入口打开统一分组任务，确认任务类型和提交路由已切换至 `/config/groups`。
7. 新建共享分组后，同时在计划任务与快捷运行页面确认可见，避免旧门禁重复创建同名分组。
8. MCP 命令重叠冲突不进入幂等缓存，同一 `request_id` 可在冲突结束后重试执行。
9. Schema 60→61 在旧分组名称与占位名称形成循环冲突时仍能完成迁移。
10. 全仓 Go 测试、Go Vet、浏览器门禁与差异格式检查。

## 2. 最终部署

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署，当前用户身份 |
| 地址 | `http://127.0.0.1:18852` |
| 进程 | PID `5316`，报告生成时仍在监听 |
| 部署目录 | `.scratch/post-release-review-final-20260828` |
| State Root | `.scratch/post-release-review-final-20260828/state` |
| 权限测试数据 | `.scratch/post-release-review-host-data-20260828/acl-files-only` |
| 管理员 | `admin`；一次性凭据仅保留在隔离 State Root 和本地测试会话中 |
| 标准错误 | 0 字节 |

最终进程、State Root、部署探针、探针结果和测试数据均保留。探针调试过程中创建的共享分组也作为测试数据保留。

## 3. 部署态外部验收

部署态验证由仓库外置 Playwright Chromium 进程执行，没有使用应用内浏览器。

| 条目 | 结果 |
| --- | --- |
| `GET /login` | 200 |
| 未认证访问 `/config/schedules` | 跳转到 `/login` |
| 管理员认证 | 通过，进入 `/monitor` |
| 安全响应头 | CSP、`X-Content-Type-Options: nosniff`、Frame 防护均存在 |
| 统一分组任务 | `data-task-kind="record-group-new"`，Action 指向 `/config/groups` |
| 共享分组复用 | 在 `/config/schedules` 与 `/config/quick-runs` 同时可见 |
| 权限平台 | `windows` |
| 继承字段数量 | 1 |
| 继承关闭后重新开启 | 通过，重新读取仍为 checked |
| 文件子项 ACE 写入与回读 | 通过，`data-applies-to="files"` |
| 最终监听状态 | `127.0.0.1:18852` 正常监听 |
| 服务标准错误 | 0 字节 |

完整部署探针结果保存在 `.scratch/post-release-review-final-20260828/deployment-results.json`。

## 4. 自动化回归

| 命令 / 测试 | 结果 |
| --- | --- |
| `go test ./...` | 通过；最终完整运行无失败 |
| `go vet ./...` | 通过 |
| `npm test`（`integration/browser`） | 通过；Chromium desktop gate 与 contracts 均通过 |
| `go test ./internal/mcpcommand` | 通过；覆盖未缓存冲突释放 claim 后重试 |
| `go test ./internal/store/migrations` | 通过；覆盖 60→61 名称循环冲突迁移 |
| `go test ./internal/hostfiles ./internal/privilegebroker ./internal/web` | 通过；覆盖精确 ACL 范围和权限表单解析 |
| `git diff --check` | 通过 |

一次全仓测试曾遇到 Windows 临时目录清理的共享锁瞬态失败；对应单测立即单独重跑通过，随后再次执行完整 `go test ./...` 全部通过。

## 5. Review 修复对应关系

| 问题 | 修复与验证 |
| --- | --- |
| 继承复选框被隐藏 fallback 值覆盖 | 移除重复字段，并兼容解析多个值中的显式 true；部署态完成关闭→开启→回读 |
| 编辑 Windows 直接 ACE 丢失或扩大子项范围 | 端到端传递 `files` / `folders` / `children` 精确范围；部署态真实写入和回读 `files` |
| MCP 重叠冲突污染 24 小时幂等缓存 | 新增 uncached outcome，冲突释放 claim；Ledger 回归测试验证同 request ID 可重试 |
| 60→61 占位分组名碰撞唯一约束 | 迁移时先清除旧关联再重建共享目录；循环冲突 fixture 通过 |
| 浏览器门禁仍使用旧分组入口并重复创建 | 更新为统一入口和 `record-group-new`，快捷运行复用计划任务创建的分组；门禁与部署态均通过 |
