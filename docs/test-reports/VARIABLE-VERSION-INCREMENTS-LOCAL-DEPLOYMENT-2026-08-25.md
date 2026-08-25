# 变量版本号快捷递增本地部署测试报告

测试时间：2026-08-25（Asia/Shanghai）

测试分支：`codex/copy-tooltip-layer`

基线提交：`cfcdb4f docs: rewrite user-facing readme`

结论：**通过**。全仓 Go 测试、完整 Chromium 门禁和 12/12 部署探针均通过。版本号变量的“…”菜单提供增加主版本号、增加次版本号、增加修订版本号三项操作；非版本号变量不显示这些操作。

## 1. 测试清单

1. 登录页、匿名访问保护和 CSP 基础访问测试。
2. 管理员使用新部署生成的一次性密码登录。
3. 通过页面创建版本号变量与普通文本变量并保留。
4. 英文菜单包含 Major、Minor、Patch 三项操作。
5. 普通文本变量不显示版本递增操作。
6. Patch 只增加修订段；Minor 增加次版本并归零修订段；Major 增加主版本并归零后两段。
7. 每次递增同步推进变量数据库修订号。
8. 中文菜单文案准确。
9. 390px 窄屏菜单完整位于视口内。
10. 三类操作写入审计历史，浏览器无运行时错误。
11. 服务标准错误为空，审计哈希链与签名 checkpoint 有效。

## 2. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18831` |
| 进程 | PID `12768`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-variable-version-increments-cfcdb4f` |
| State Root | `.scratch/local-deploy-variable-version-increments-cfcdb4f/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 18 个事件；哈希链和签名 checkpoint 均有效 |

部署、State Root、登录审计、浏览器 JSON 结果、截图与测试变量均保留；报告生成后未停止进程。

## 3. 外部 Chromium 部署验证（12/12）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 登录页基础访问 | 通过；`GET /login` → 200 |
| 2 | 基础安全响应 | 通过；Content Security Policy 存在 |
| 3 | 匿名路由保护 | 通过；变量页重定向至登录页 |
| 4 | 管理员登录 | 通过；进入 `/monitor` |
| 5 | 保留测试数据 | 通过；通过 UI 创建版本号与文本变量 |
| 6 | 英文操作菜单 | 通过；Major、Minor、Patch 三项均存在 |
| 7 | 非版本号变量 | 通过；不显示版本递增操作 |
| 8 | 版本递增语义 | 通过；最终轮次为 `4.0.0` → `4.0.1` → `4.1.0` → `5.0.0`，修订号推进至 `v13` |
| 9 | 中文操作菜单 | 通过；三项文案准确 |
| 10 | 390px 窄屏 | 通过；菜单完整可见且未越界 |
| 11 | 审计历史 | 通过；三类成功操作均可检索 |
| 12 | 浏览器运行时 | 通过；无 page error |

保留证据：

- `.scratch/local-deploy-variable-version-increments-cfcdb4f/deployment-variable-version-probe.cjs`
- `.scratch/local-deploy-variable-version-increments-cfcdb4f/deployment-variable-version-probe.json`
- `.scratch/local-deploy-variable-version-increments-cfcdb4f/variable-version-actions-desktop.png`
- `.scratch/local-deploy-variable-version-increments-cfcdb4f/variable-version-actions-mobile.png`

## 4. 自动化回归

| 项目 | 结果 |
| --- | --- |
| 版本领域测试 | 通过；覆盖三段规则、非法值、非法操作与超大整数 |
| Web 行为测试 | 通过；覆盖菜单、CSRF、类型边界、修订号和审计 |
| 权限路由测试 | 通过；仅管理员和维护者可执行 |
| `go test ./... -count=1` | 通过 |
| `npm test`（完整 Chromium 门禁） | 通过 |
| `git diff --check` | 通过 |

## 5. 保留状态

- 新部署继续监听 `127.0.0.1:18831`，PID 为 `12768`。
- 测试使用仓库安装的外部 Playwright Chromium，没有使用应用内浏览器。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
- 测试数据保留为 `VERSION_INCREMENT_TEST=5.0.0`（`v13`）与 `VERSION_INCREMENT_TEXT_TEST=stable`。
