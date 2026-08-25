# 文本预览可读性本地部署测试报告

测试时间：2026-08-25（Asia/Shanghai）

测试分支：`codex/copy-tooltip-layer`

基线提交：`dd28cc9 feat(web): improve operational interface controls`

结论：**通过**。文本预览不再出现浅色语法文字叠在白色背景上的低对比度问题。正文实测对比度为 15.03:1，当前最弱语法高亮色为 7.16:1，均高于 WCAG AA 普通文字 4.5:1 的要求。

## 1. 问题与修复

文本预览基础样式使用深色终端背景及配套高亮色，但后置的编辑器页面规则把背景单独覆盖为白色，没有同步改写正文和语法颜色。修复后，编辑器页面明确使用 `--terminal` 背景和 `--terminal-ink` 正文色，并保留原有高对比度语法高亮体系。

## 2. 测试清单

1. 登录页和 CSP 基础访问。
2. 未登录用户访问文本预览时重定向至登录页。
3. 管理员使用新部署生成的一次性密码登录。
4. PowerShell 脚本正文和实际渲染的语法高亮颜色对比度。
5. 中文普通文本的颜色、背景及内容完整性。
6. 字体大小与行高。
7. 390px 窄屏无页面级横向溢出。
8. 浏览器运行时、服务标准错误和签名审计链。

## 3. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18832` |
| 进程 | PID `22460`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-text-preview-contrast-dd28cc9` |
| State Root | `.scratch/local-deploy-text-preview-contrast-dd28cc9/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 3 个事件；哈希链和签名 checkpoint 均有效 |

部署、State Root、浏览器结果、截图和样例文本均保留；报告生成后未停止进程。

## 4. 外部 Chromium 部署验证（7/7）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 基础访问 | 通过；`GET /login` → 200 且包含 CSP |
| 2 | 匿名访问保护 | 通过；文本预览重定向至登录页 |
| 3 | 管理员登录 | 通过；进入 `/monitor` |
| 4 | 脚本可读性 | 通过；正文 15.03:1，最低实际语法色 7.16:1，字号 13.8125px、行高 22.1px |
| 5 | 普通文本可读性 | 通过；中文正文对比度 15.03:1 且内容完整 |
| 6 | 移动端可读性 | 通过；390px 下无页面级横向溢出 |
| 7 | 浏览器运行时 | 通过；无 page error |

保留证据：

- `.scratch/local-deploy-text-preview-contrast-dd28cc9/deployment-text-preview-contrast-probe.cjs`
- `.scratch/local-deploy-text-preview-contrast-dd28cc9/deployment-text-preview-contrast-probe.json`
- `.scratch/local-deploy-text-preview-contrast-dd28cc9/text-preview-script-desktop.png`
- `.scratch/local-deploy-text-preview-contrast-dd28cc9/text-preview-script-mobile.png`
- `.scratch/text-preview-readable-test-data-dd28cc9/readable-preview.ps1`
- `.scratch/text-preview-readable-test-data-dd28cc9/readable-preview.txt`

## 5. 自动化回归

| 项目 | 结果 |
| --- | --- |
| 新增颜色层叠与 WCAG 对比度契约 | 通过；修复前稳定失败，修复后通过 |
| `go test ./... -count=1` | 通过 |
| `npm test`（完整 Chromium 门禁） | 通过 |
| `git diff --check` | 通过 |

## 6. 保留状态

- 新部署继续监听 `127.0.0.1:18832`，PID 为 `22460`。
- 测试使用仓库安装的外部 Playwright Chromium，没有使用应用内浏览器。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
- PowerShell 与普通文本样例保留在独立测试数据目录中，未放入受保护的安装目录或 State Root。
