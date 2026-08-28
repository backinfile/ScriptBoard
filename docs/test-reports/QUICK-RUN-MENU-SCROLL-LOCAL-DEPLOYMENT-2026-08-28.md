# 快捷执行菜单滚动与单活动 Run 直达本地部署测试报告

测试时间：2026-08-28（Asia/Shanghai）

测试分支：`codex/quick-run-menu-scroll`

基线提交：`671a575 merge: add cross-platform host file permissions`

结论：**通过**。长操作菜单现在可以完整上下滚动；左下角恰好只有一个活动 Run 时直接进入其详情；启用“运行二次确认”的快捷执行在手动运行前会弹窗确认。

## 1. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18841` |
| 进程 | PID `47800`，报告更新时仍在监听 |
| 部署目录 | `.scratch/local-deploy-quick-run-menu-scroll-671a575` |
| State Root | `.scratch/local-deploy-quick-run-menu-scroll-671a575/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 5 个事件；哈希链和签名 checkpoint 均有效 |

部署、State Root、浏览器探针结果和截图均按要求保留；报告生成后没有停止进程或清理数据。

## 2. 基础访问与安全响应（5/5）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 登录页基础访问 | 通过；`GET /login` → 200 |
| 2 | 未登录快捷执行页保护 | 通过；`GET /config/quick-runs` → 303 `/login` |
| 3 | Content Security Policy | 通过；登录响应包含 CSP |
| 4 | MIME 嗅探防护 | 通过；响应包含 `X-Content-Type-Options: nosniff` |
| 5 | Frame 防护 | 通过；响应包含 `X-Frame-Options` |

## 3. 外部 Chromium 部署验证（5/5）

外部 Playwright Chromium 使用实际部署生成的一次性管理员密码登录，没有使用应用内浏览器。

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 管理员登录与快捷执行页访问 | 通过；`/login` 与 `/config/quick-runs` 均为 200 |
| 2 | 部署静态资源 | 通过；`app.css` 与 `app-v2.js` 均为 200 |
| 3 | 长菜单完整滚动 | 通过；`scrollTop 243 / 243` |
| 4 | 菜单滚动稳定性与顶层显示 | 通过；滚动期间 0 次几何样式重写，面板位于 Top Layer |
| 5 | 浏览器运行时 | 通过；无 page error |

保留证据：

- `.scratch/local-deploy-quick-run-menu-scroll-671a575/deployment-menu-scroll-probe.cjs`
- `.scratch/local-deploy-quick-run-menu-scroll-671a575/deployment-menu-scroll-probe.json`
- `.scratch/local-deploy-quick-run-menu-scroll-671a575/quick-run-menu-scroll.png`

## 4. 自动化回归（2/2）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | `node integration/browser/action-menu-layer-contract.cjs` | 通过；旧逻辑稳定失败并产生 24 次菜单几何重写，修复后通过且为 0 次 |
| 2 | `go test ./internal/web -count=1` | 通过；`101.936s` |

`git diff --check` 同时通过。

## 5. 单活动 Run 直达验证（5/5）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 状态接口唯一 Run 标识 | 通过；恰好一个活动 Run 时返回 `activeRunId` |
| 2 | 服务端首屏链接 | 通过；直接生成 `/history/runs/{id}` |
| 3 | 实时状态刷新 | 通过；1 个活动 Run 时直达详情，多个时恢复 `/history/runs` |
| 4 | 部署态真实 Run | 通过；从文件页启动测试 PowerShell Run `wulpmvIe3shqMRKIWYR7wS3K` |
| 5 | 左下角实际点击 | 通过；链接及点击后的路径均为 `/history/runs/wulpmvIe3shqMRKIWYR7wS3K`，无 page error |

新增自动化契约：`integration/browser/shell-active-run-link-contract.cjs`。完整 `go test ./... -count=1` 与完整 Chromium desktop gate 均通过。

保留证据：

- `.scratch/local-deploy-quick-run-menu-scroll-671a575/deployment-single-active-run-probe.cjs`
- `.scratch/local-deploy-quick-run-menu-scroll-671a575/deployment-single-active-run-probe.json`
- `.scratch/local-deploy-quick-run-menu-scroll-671a575/single-active-run-detail.png`
- `.scratch/single-active-run-test-data-671a575/hold-active-run.ps1`

## 6. 保留状态

- 新部署继续监听 `127.0.0.1:18841`，PID 为 `47800`。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
- 登录审计、浏览器结果、截图和部署日志均保留在部署目录。

## 7. 快捷执行运行二次确认

功能分支部署由 schema 60 原地迁移到 schema 61；合并到已有分组 schema 61 的 `dev` 后，该字段顺延为 schema 62，以确保两条升级路径都能完整迁移。已有快捷执行默认保持关闭。外部 Playwright Chromium 使用真实部署创建并保留了开启二次确认的测试快捷执行 `Confirmation probe mtchq2so`。

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 创建表单与编辑表单 | 通过；均提供 bool 开关，默认关闭，保存后可回显 |
| 2 | 手动点击运行 | 通过；显示 `Confirm running this Quick Run?` 自定义确认弹窗 |
| 3 | 取消运行 | 通过；停留在 `/config/quick-runs`，未提交启动 |
| 4 | 确认运行 | 通过；进入新 Run `/history/runs/odBhojSrMI2M5a6sYgTr1568` |
| 5 | 非 UI 启动契约 | 通过；服务端启动接口不要求额外确认参数，调度与外部接口链路未改动 |
| 6 | 迁移与版本语义 | 通过；旧数据默认关闭，单独切换确认偏好不会增加快捷执行版本号 |
| 7 | 自动化回归 | 通过；`go test ./... -count=1` 与完整 Chromium desktop gate 均通过 |
| 8 | 浏览器运行时 | 通过；无 page error，部署标准错误日志为 0 字节 |

新增保留证据：

- `.scratch/local-deploy-quick-run-menu-scroll-671a575/deployment-quick-run-confirmation-probe.cjs`
- `.scratch/local-deploy-quick-run-menu-scroll-671a575/deployment-quick-run-confirmation-probe.json`
- `.scratch/local-deploy-quick-run-menu-scroll-671a575/quick-run-confirmation.png`
