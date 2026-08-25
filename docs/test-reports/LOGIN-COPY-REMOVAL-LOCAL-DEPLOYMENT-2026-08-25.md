# 登录页冗余文案移除本地部署测试报告

测试时间：2026-08-25（Asia/Shanghai）

测试分支：`codex/copy-tooltip-layer`

基线提交：`b385328 fix(web): restore readable text preview contrast`

结论：**通过**。主登录页已移除“一台主机，一份清晰的运行记录。”和“本机管理员”，英文对应文案也同步移除；标题与账号表单之间没有留下空文案区域。身份二次验证页所需的步骤说明保持不变。

## 1. 变更范围

- 删除主登录模板中的 eyebrow 文案节点。
- 主登录页不再渲染描述段落；二次验证页继续渲染必要说明。
- 删除对应中英文本地化键和不再使用的专用样式。
- 为仅包含标题的登录页头部消除标题底部预留间距。

## 2. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18833` |
| 进程 | PID `29964`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-login-copy-removal-b385328` |
| State Root | `.scratch/local-deploy-login-copy-removal-b385328/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 1 个事件；哈希链和签名 checkpoint 均有效 |

部署、State Root、浏览器 JSON 结果和截图均保留；报告生成后未停止进程。

## 3. 外部 Chromium 部署验证（7/7）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 登录页基础访问 | 通过；`GET /login` → 200 且包含 CSP |
| 2 | 中文登录页 | 通过；两段目标文案和 eyebrow 节点均不存在，账号字段保持 |
| 3 | 布局收紧 | 通过；标题到表单间距为 28px，无空文案槽位 |
| 4 | 英文登录页 | 通过；英文对应文案同步移除 |
| 5 | 登录错误态 | 通过；错误提示正常，目标文案不会重新出现 |
| 6 | 390px 移动端 | 通过；目标文案不存在且无横向溢出 |
| 7 | 浏览器运行时 | 通过；无 page error |

保留证据：

- `.scratch/local-deploy-login-copy-removal-b385328/deployment-login-copy-removal-probe.cjs`
- `.scratch/local-deploy-login-copy-removal-b385328/deployment-login-copy-removal-probe.json`
- `.scratch/local-deploy-login-copy-removal-b385328/login-copy-removed-zh-desktop.png`
- `.scratch/local-deploy-login-copy-removal-b385328/login-copy-removed-mobile.png`

## 4. 自动化回归

| 项目 | 结果 |
| --- | --- |
| 双语登录页文案回归测试 | 通过；修复前稳定失败，修复后通过 |
| 登录页基础、AJAX 增强与认证测试 | 通过 |
| `go test ./... -count=1` | 通过 |
| `npm test`（完整 Chromium 门禁） | 通过 |
| `git diff --check` | 通过 |

## 5. 保留状态

- 新部署继续监听 `127.0.0.1:18833`，PID 为 `29964`。
- 测试使用仓库安装的外部 Playwright Chromium，没有使用应用内浏览器。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
