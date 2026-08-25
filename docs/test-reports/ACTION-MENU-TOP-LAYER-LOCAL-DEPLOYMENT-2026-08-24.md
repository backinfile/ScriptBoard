# “更多操作”菜单顶层显示本地部署测试报告

测试时间：2026-08-24（Asia/Shanghai）

测试分支：`codex/action-menu-top-layer`

基线提交：`facbdc5 docs: update agent workspace guidance`

结论：**通过（28/28）**

## 1. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18827` |
| 进程 | PID `21284`，报告更新时仍在监听 |
| 部署目录 | `.scratch/local-deploy-action-menu-top-layer-facbdc5` |
| State Root | `.scratch/local-deploy-action-menu-top-layer-facbdc5/state` |
| 管理员 | `admin`；一次性密码保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 13 个事件；哈希链和签名 checkpoint 均有效 |

部署、State Root、登录审计、浏览器探针、JSON 结果和截图均按要求保留。报告生成后没有停止进程或清理数据。

## 2. 基础访问与安全响应（7/7）

| # | 测试条目 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 登录页基础访问 | 通过 | `GET /login` → 200 |
| 2 | 未登录路由保护 | 通过 | `GET /monitor` → 303 `/login` |
| 3 | 应用 JavaScript 资源 | 通过 | 部署页面引用的 `app*.js` → 200 |
| 4 | Content Security Policy | 通过 | 响应包含 CSP |
| 5 | MIME 嗅探防护 | 通过 | 响应包含 `X-Content-Type-Options` |
| 6 | Frame 防护 | 通过 | 响应包含 `X-Frame-Options` |
| 7 | 服务标准错误 | 通过 | `stderr.log` 为 0 字节 |

## 3. 相关页面访问（10/10）

外部 Chromium 使用实际部署生成的一次性管理员密码登录后，以下页面均返回 200：

1. `/monitor`
2. `/monitor/applications`
3. `/monitor/containers`
4. `/monitor/kubernetes`
5. `/resources/files`
6. `/config/quick-runs`
7. `/config/schedules`
8. `/resources/variables`
9. `/resources/databases`

## 4. “更多操作”菜单部署态验证（7/7）

浏览器探针使用部署产物中的真实 `app.css` 和 `app.js`，把共享 `.action-menu` 放入同时具有 `overflow: hidden`、独立层叠上下文的祖先中，并让一个 `z-index: 999999` 的覆盖层与菜单重叠。

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 菜单进入浏览器 Top Layer | 通过 |
| 2 | 菜单操作项位于高层覆盖 UI 之上且可命中 | 通过 |
| 3 | 菜单内容保持在视口安全边距内 | 通过 |
| 4 | 打开第二个菜单会关闭第一个菜单 | 通过 |
| 5 | 视口缩至 `390 × 844` 后重新定位且不横向溢出 | 通过 |
| 6 | Escape 同时关闭菜单和 Top Layer popover | 通过 |
| 7 | 全程无浏览器 page error | 通过 |

保留证据：

- `.scratch/local-deploy-action-menu-top-layer-facbdc5/deployment-menu-probe.cjs`
- `.scratch/local-deploy-action-menu-top-layer-facbdc5/deployment-menu-probe.json`
- `.scratch/local-deploy-action-menu-top-layer-facbdc5/action-menu-top-layer.png`

## 5. 自动化回归门禁（4/4）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 新增共享菜单层级浏览器契约 | 通过；修复前稳定失败，修复后通过 |
| 2 | 完整 Chromium desktop gate | 通过 |
| 3 | 全仓 Go 测试 | 通过；`go test ./... -count=1` |
| 4 | 部署 State Root 审计完整性 | 通过；13 个事件，签名 checkpoint 有效 |

## 6. 保留状态

- 部署继续监听 `127.0.0.1:18827`，PID 为 `21284`。
- 测试使用外部 Playwright Chromium 与 HTTP 客户端，没有使用应用内浏览器。
- 初始管理员密码未写入本报告或版本库，仅保留在部署 State Root 中。
- 浏览器门禁运行期间生成的非功能性截图差异已撤销；部署探针截图单独保留在部署目录。
