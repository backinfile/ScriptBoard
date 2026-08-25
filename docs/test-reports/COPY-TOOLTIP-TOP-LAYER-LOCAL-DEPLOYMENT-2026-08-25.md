# 复制图标 Tooltip 顶层显示本地部署测试报告

测试时间：2026-08-25（Asia/Shanghai）

测试分支：`codex/copy-tooltip-layer`

基线提交：`cfcdb4f docs: rewrite user-facing readme`

结论：**本次功能与部署验证通过（12/12 部署探针通过）**。完整 Chromium 门禁中的新增 tooltip 契约通过；门禁随后被 `dev` 现有“更多操作”菜单断言阻断：实现为 `position: fixed`，旧断言仍期待 `position: absolute`，不属于本次改动。

## 1. 测试清单

1. 登录页与未认证路由基础访问。
2. CSP 等基础安全响应头。
3. 管理员使用新部署生成的一次性密码登录。
4. 监控、文件、变量与自定义面板页面访问。
5. 文件页复制路径图标的 hover tooltip 层级。
6. 变量名复制图标的 hover、复制成功状态与文案更新。
7. 自定义面板公开地址复制图标的 hover、复制成功状态与文案更新。
8. tooltip 逃离裁剪容器、覆盖高 `z-index` 内容、focus 与 Escape 的浏览器契约。
9. JavaScript 语法、Go 前端契约、全仓 Go 测试与部署审计完整性。

## 2. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18829` |
| 进程 | PID `9036`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-copy-tooltip-cfcdb4f` |
| State Root | `.scratch/local-deploy-copy-tooltip-cfcdb4f/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 6 个事件；哈希链有效，签名 checkpoint 有效 |

部署、State Root、测试变量、自定义面板、登录审计、浏览器 JSON 结果和截图均保留；报告生成后未停止进程。

## 3. 外部 Chromium 部署验证（12/12）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | `GET /login` | 通过，200 |
| 2 | Content Security Policy | 通过 |
| 3 | 未登录访问 `/monitor` | 通过，303 至 `/login` |
| 4 | 管理员登录 | 通过，进入 `/monitor` |
| 5 | 认证页面基础访问 | 通过；监控、文件、变量、自定义面板均为 200 |
| 6 | 文件页复制路径提示 | 通过；tooltip 进入浏览器 Top Layer |
| 7 | 保留变量测试数据 | 通过；`COPY_TOOLTIP_LAYER_TEST` 已创建并保留 |
| 8 | 变量名复制 hover | 通过；本地化文案位于 Top Layer |
| 9 | 变量复制成功反馈 | 通过；同一顶层 tooltip 更新为 `Copied` |
| 10 | 自定义面板复制 hover | 通过；公开地址复制提示位于 Top Layer |
| 11 | 自定义面板复制反馈 | 通过；文案原位更新为“已复制” |
| 12 | 浏览器运行时 | 通过；无 page error |

保留证据：

- `.scratch/local-deploy-copy-tooltip-cfcdb4f/deployment-tooltip-probe.cjs`
- `.scratch/local-deploy-copy-tooltip-cfcdb4f/deployment-tooltip-probe.json`
- `.scratch/local-deploy-copy-tooltip-cfcdb4f/variable-copy-tooltip.png`
- `.scratch/local-deploy-copy-tooltip-cfcdb4f/dashboard-copy-tooltip.png`

## 4. 自动化回归

| 项目 | 结果 |
| --- | --- |
| 新增 Go 顶层 tooltip 契约 | 通过；修复前稳定失败，修复后通过 |
| 新增 Playwright 顶层 tooltip 契约 | 通过；覆盖裁剪祖先、超高层覆盖、模态对话框、hover、focus、文案更新和 Escape |
| `node --check internal/web/ui/assets/app.js` | 通过 |
| 相关 Go 前端契约 | 通过 |
| `go test ./... -count=1` | 通过 |
| 完整 `pnpm test` | 新增 tooltip 契约通过；随后在现有 action-menu position 断言处失败 |
| Impeccable mechanical detector | 已运行；仅报告 `app.css` 中既有全局设计告警，本次 tooltip 沿用现有视觉 token 与样式 |

## 5. 保留状态

- 部署继续监听 `127.0.0.1:18829`，PID 为 `9036`。
- 测试使用仓库安装的外部 Playwright Chromium，没有使用应用内浏览器。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
- 测试数据保留为变量 `COPY_TOOLTIP_LAYER_TEST` 和自定义面板 `Copy Tooltip Test`。
