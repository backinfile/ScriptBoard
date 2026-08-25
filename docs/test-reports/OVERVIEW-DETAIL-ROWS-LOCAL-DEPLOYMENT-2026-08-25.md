# 主机概览详细数据分行布局本地部署测试报告

测试时间：2026-08-25（Asia/Shanghai）

测试分支：`codex/copy-tooltip-layer`

基线提交：`cfcdb4f docs: rewrite user-facing readme`

结论：**通过（9/9 部署探针通过）**。“存储与 I/O”和“网络接口”在桌面及窄屏均各占一整行，不再并列；宽表保持容器内响应式展示，页面无横向溢出。

## 1. 测试清单

1. 登录页和详情页基础访问。
2. 管理员使用新部署生成的一次性密码登录。
3. 桌面端两部分上下排列、左右边界一致。
4. 桌面端宽表不造成页面级横向溢出。
5. 390px 窄屏保持相同阅读顺序和等宽布局。
6. 部署版本 JavaScript 静态资源访问。
7. 浏览器 page error、服务标准错误与审计完整性。
8. CSS 布局回归契约、机械布局扫描和相关 Go 测试。

## 2. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18830` |
| 进程 | PID `30992`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-overview-detail-rows-cfcdb4f` |
| State Root | `.scratch/local-deploy-overview-detail-rows-cfcdb4f/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 2 个事件；哈希链和签名 checkpoint 均有效 |

部署、State Root、登录审计、浏览器 JSON 结果和截图均保留；报告生成后未停止进程。

## 3. 外部 Chromium 部署验证（9/9）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 登录页基础访问 | 通过；`GET /login` → 200 |
| 2 | 基础安全响应 | 通过；Content Security Policy 存在 |
| 3 | 管理员登录 | 通过；进入 `/monitor` |
| 4 | 详细数据页面 | 通过；`/monitor?node=local&tab=details` → 200 |
| 5 | 桌面独立成行 | 通过；两部分上下排列且等宽 |
| 6 | 桌面页面宽度 | 通过；页面无横向溢出 |
| 7 | 窄屏布局 | 通过；390px 下保持上下顺序、等宽且无页面溢出 |
| 8 | 静态资源访问 | 通过；部署版本 `app-v2.js` → 200 |
| 9 | 浏览器运行时 | 通过；无 page error |

保留证据：

- `.scratch/local-deploy-overview-detail-rows-cfcdb4f/deployment-overview-layout-probe.cjs`
- `.scratch/local-deploy-overview-detail-rows-cfcdb4f/deployment-overview-layout-probe.json`
- `.scratch/local-deploy-overview-detail-rows-cfcdb4f/overview-details-desktop.png`
- `.scratch/local-deploy-overview-detail-rows-cfcdb4f/overview-details-mobile.png`

## 4. 自动化回归

| 项目 | 结果 |
| --- | --- |
| 新增布局回归契约 | 通过；旧双列规则下稳定失败，单列规则下通过 |
| Impeccable layout mechanical detector | 通过；0 项发现 |
| JavaScript 语法检查 | 通过 |
| 相关 Go 前端契约 | 通过 |
| `go test ./... -count=1` | 业务测试均完成；`internal/web` 在临时数据库清理时遇到一次 Windows 文件占用失败，失败用例与本次布局契约单独重跑均通过 |

## 5. 保留状态

- 新部署继续监听 `127.0.0.1:18830`，PID 为 `30992`。
- 测试使用仓库安装的外部 Playwright Chromium，没有使用应用内浏览器。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
