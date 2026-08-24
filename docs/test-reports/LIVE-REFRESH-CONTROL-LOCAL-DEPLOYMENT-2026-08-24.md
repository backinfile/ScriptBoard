# 实时更新控件重塑本地部署测试报告

测试时间：2026-08-24（Asia/Shanghai）

测试分支：`codex/action-menu-top-layer`

结论：**通过**

## 1. 设计结果

- 将单行长胶囊重塑为两层采集状态控件：首行显示状态，次行显示采集时间与刷新节奏。
- 开启状态使用成功色状态点并保留明确文字；关闭状态使用中性状态点，不依赖颜色表达状态。
- 移除静止状态的卡片阴影，使用设计系统中的平面表面、规则线和 8px 圆角。
- 桌面开关保持紧凑；移动端保持至少 `44 × 44` 的触控区域。
- 同一共享样式覆盖应用、容器和 Kubernetes 监控页面。

## 2. 实际部署

| 项目 | 结果 |
| --- | --- |
| 地址 | `http://127.0.0.1:18827` |
| 进程 | PID `21284`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-action-menu-top-layer-facbdc5` |
| State Root | `.scratch/local-deploy-action-menu-top-layer-facbdc5/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root |
| 标准错误 | 0 字节 |
| 审计校验 | 13 个事件；哈希链和签名 checkpoint 均有效 |

## 3. 外部 Chromium 验证

| 测试条目 | 结果 |
| --- | --- |
| 中文开启状态 | 通过 |
| 中文关闭状态 | 通过 |
| 状态、时间、刷新节奏两行分层且无碰撞 | 通过 |
| 开关与文字保持明确间距 | 通过 |
| 键盘焦点可见 | 通过 |
| 桌面控件无阴影、使用 8px 圆角 | 通过 |
| 应用页 `390 × 844` 无横向溢出 | 通过 |
| 容器页 `390 × 844` 无横向溢出 | 通过 |
| 移动端开关 `52 × 44` | 通过 |
| 浏览器 page error | 0 |

空白测试 State Root 未配置 Kubernetes 连接，因此实际部署中不渲染工作负载刷新控件；完整浏览器 fixture 已覆盖其共享控件和响应式布局。

保留证据：

- `.scratch/local-deploy-action-menu-top-layer-facbdc5/refresh-control-probe.cjs`
- `.scratch/local-deploy-action-menu-top-layer-facbdc5/refresh-control-probe.json`
- `.scratch/local-deploy-action-menu-top-layer-facbdc5/refresh-control-on.png`
- `.scratch/local-deploy-action-menu-top-layer-facbdc5/refresh-control-off.png`
- `.scratch/local-deploy-action-menu-top-layer-facbdc5/refresh-control-mobile.png`

## 4. 自动化回归

- `npm test`：通过完整 Chromium browser gate，包括应用、容器、Kubernetes 的桌面和移动端检查。
- `go test ./internal/web -run Applications -count=1`：通过。
- 新增的 Action Menu Top Layer 契约仍通过。

## 5. 保留状态

- 部署继续监听 `127.0.0.1:18827`，PID 为 `21284`。
- State Root、登录审计、探针脚本、JSON 结果和截图均保留。
- 测试使用外部 Playwright Chromium 与 HTTP 客户端，没有使用应用内浏览器。
