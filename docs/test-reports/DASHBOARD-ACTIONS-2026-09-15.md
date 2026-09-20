# 面板操作卡片 + 四档可见性 + 流程图卡片 + Node.js 脚本类型 本地部署验收报告

- 日期：2026-09-15
- 实例：`http://127.0.0.1:11155`（保持运行中）
- 程序：`.scratch/deploy-local-dashboard-actions/scriptboard.exe`，state root：同目录 `state/`
- 分支 worktree：`D:/Github/worktrees/ScriptBoard/dashboard-actions`
- 账号（本地测试实例，按 AGENTS.md 明文记录）：
  - 管理员：`admin` / `SbTest!2026a-ok!`
    - 说明：任务指定的 `SbTest!2026a` 仅 11 字符，不满足产品密码策略（≥15 字符，`internal/web/password_policy.go`），setup 页面返回密码策略错误（证据 `shot-02b-setup-error.png`）。改用合规密码 `SbTest!2026a-ok!`（16 字符）。
  - 执行员：`operator1` / `S-P4f4GxxBWp53Kh6j-q1BPQj0EaEFJq`
  - 查看员：`viewer1` / `OZt2vV_2-tuSddlTTZXW4YAfFaTarjkB`
  - 面板「验收面板」当前访问 Key（轮换两次后的有效值）：`9O-Htsqj58CudlRULnq0hDLiDFTDEP7JCTO0DsfRw-w`
- 证据目录：`.scratch/deploy-local-dashboard-actions/`（截图 `shot-*.png`、HTML/JSON 快照 `evidence-*`、驱动脚本 `t0*.mjs`）

## 测试清单与结果

| # | 条目 | 结果 | 关键证据 |
|---|------|------|----------|
| 1 | 未登录访问 / 跳转；setup；登录；主导航 | 通过 | 见下 |
| 2 | 配置 → 自定义面板页可访问 | 通过 | `shot-05-config-dashboards.png` |
| 3 | 一次性执行 Node.js `console.log('hello-js')` | 通过 | Run `yP60Nlve8P6qFpqLTxiKYm4w` |
| 4 | .js 主机脚本发布为快捷执行并运行 | 通过 | Run `eWTiEYCCNL-Mhu5qtF7-Lnd4` |
| 5 | 创建面板 + 数值卡片 | 通过（取数受限） | 见「已知限制」 |
| 6 | quick_run 操作按钮：监控页触发 → runId → 成功；权限抽验 | 通过 | Run `q--6hCptpBoFf0H3sAYo_xJs` |
| 7 | public_read：匿名可见、零按钮零端点 | 通过 | `evidence-07-public-read.html` |
| 8 | public_operate：一次性 Key、按钮渲染、404/404/200 | 通过 | 见下 |
| 9 | anonymous_operate：browser_http 浏览器直连；服务端端点恒 404 | 通过 | `shot-09-anonymous-click.png` |
| 10 | Key 轮换后旧 Key 失效 | 通过 | 见下 |
| 11 | flow 卡片 YAML 解析保存、节点图显示 | 通过 | `evidence-11-dashboard-with-flow.html` |
| 12 | 流程成功路径：逐节点推进、SSE、成功横幅、always post | 通过 | `evidence-12-flow-success-events.json` |
| 13 | 失败路径：失败节点、下游 skipped、on_failure/always post | 通过 | `evidence-13-flow-fail-events.json` |
| 14 | 依赖环 YAML 保存返回 422，错误可读 | 通过 | `evidence-14-cycle-422.html` |
| 15 | 导出 → 新建面板导入，actions/flow 完整落库可运行 | 通过 | `evidence-15-export.json` |

## 逐项记录

### 基础访问（1、2）

- setup 前访问 `/` → 303 到 `/setup`（`shot-01-root-redirect.png`）。
- 一次性设置链接完成 setup：填写 token + admin 凭据，提交后进入 `/monitor`（`shot-03-after-setup.png`）。
- 匿名访问 `/` → 303 到 `/login`；凭 admin 重新登录成功（`shot-04-relogin.png`）。
- 主导航完整（监控/资源/配置/历史/设置各分组链接齐全）。
- `GET /config/dashboards` → 200，标题「自定义面板 · ScriptBoard」。

### C. Node.js 脚本类型（3、4）

- 一次性执行页语言列表含 `Node.js (.js)`；提交 `language=nodejs` + `console.log('hello-js'); console.log('node=' + process.version);` → 303 到 Run `yP60Nlve8P6qFpqLTxiKYm4w`，输出 `hello-js` 与 `node=v24.14.0`，状态 Succeeded（`evidence-run-history-oneTime.json`、`evidence-03-onetime-run.html`）。
- 主机脚本 `hello-quick.js`（`console.log('hello-js-quickrun')`）发布为快捷执行「验收-JS快捷执行」（ID `LXvHknRKHk26k6OhNrJfzD-p`），start → Run `eWTiEYCCNL-Mhu5qtF7-Lnd4` 输出正确（`evidence-run-history-quickRun.json`）。
- 说明：实例主机文件根限定在用户目录（默认工作目录 `C:\Users\17575`），`.scratch` 下的 scripts 目录被边界检查拒绝（一次性执行 400 / 快捷执行发布 403），故脚本放在 `C:\Users\17575\scriptboard-acceptance\`，并在 `.scratch/deploy-local-dashboard-actions/scripts/` 留了归档副本。

### A. 操作卡片与四档可见性（5–10）

- 面板「验收面板」（ID `25WnKZMZHA-nAPmXG4CJH-SU`，slug `acceptance`），数值卡片「GitHub速率限制」（ID `2qJMXr1XTbM5Aor2_4WwVHfp`，源 `https://api.github.com/rate_limit`，取值 `resources.core.limit`）。
- 卡片配置 `act_run_js`（quick_run → 验收-JS快捷执行，primary，publicAllowed=true）。
- 监控页登录触发：`POST /config/dashboard-cards/.../trigger` → 200 `{"runId":"q--6hCptpBoFf0H3sAYo_xJs"}`，Run 输出 `hello-js-quickrun`。
- 权限抽验：operator1 触发 → 200（Run `7_PiCEtWlzD_T9BdEIJLb6fT`）；viewer1 触发 → 403。符合权限矩阵（execute 需要 PermissionExecute）。
- public_read：匿名 `GET /public/dashboard/acceptance` → 200；HTML 含卡片名与值区域，不含 `data-dashboard-action-trigger`、不含 `/trigger` 字符串、不含按钮文案（`evidence-07-public-read.html`）。
- public_operate：切换响应直接渲染一次性 Key 横幅（`data-dashboard-onetime-key`，尾号 hint `dk_66d4ba`，`evidence-08-public-operate-key.html`）；公开页出现 `act_run_js` 按钮（`data-kind="quick_run"`）；无 Key → 404，错 Key → 404，正确 Key → 200 `{"runId":"ejFDFoG3vEfywcddsxAXbIa1"}`，Run 输出正确。
- anonymous_operate：追加 `act_self_check`（browser_http，GET `http://127.0.0.1:11155/`，publicAllowed=true）。公开页仅渲染该按钮且带 `data-url`，quick_run 按钮不出现；Playwright 匿名点击 → 状态行「请求已发送（200）」（浏览器直连，`shot-09-anonymous-click.png`）。该档位下服务端触发端点对 quick_run 与 browser_http 均恒 404。
- Key 轮换：切换离开 public_operate 后旧 Key 立即 404；重新生成 KeyA → 200 可用；step-up 再认证后 `POST .../access-key/rotate` → 一次性显示 KeyB，KeyA → 404，KeyB → 200。

### B. 流程图卡片（11–14）

- 创建 7 个 .js 快捷执行项（流程-构建/测试/部署/验证/清理/通知/失败）。
- flow 卡片「发布流程」（ID `MaWaTfhSYymQ6m2BLjiWqdsN`）：粘贴计划 4.3 结构 YAML（build→test→deploy→verify + post 清理 always、通知 on_failure），保存 303 成功，管理页显示卡片与节点徽标（`evidence-11-dashboard-with-flow.html`）。
- 成功路径：`POST .../flow/run` → 200 `flow-MaWaTfhSYymQ6m2BLjiWqdsN-1`；SSE `GET .../flow/events` 推送 6 个快照，可见 queued→running→succeeded 逐格推进；最终 4 节点全部 succeeded（各带 runId 与耗时），post 仅「清理临时产物（always)」执行，「失败通知（on_failure)」正确跳过；整体 succeeded。监控页截图 `shot-12-monitor-flow-success.png`（绿色节点、运行流程按钮、成功横幅「流程执行成功」）。各节点 Run 输出分别为 flow-build-ok / flow-test-ok / flow-deploy-ok / flow-verify-ok / flow-cleanup-ran。
- 失败路径：deploy 节点改绑「流程-失败」（`process.exit(1)`）后运行：build:succeeded、test:succeeded、deploy:failed、verify:skipped；post「清理临时产物（always)」与「失败通知（on_failure)」均执行成功（Run 输出 flow-cleanup-ran / flow-notify-ran）；整体 failed。监控页截图 `shot-13-monitor-flow-failed.png`。
- 非法 YAML（a↔b 依赖环）保存 → HTTP 422，技术细节「流程节点依赖存在循环」（`evidence-14-cycle-422.html`）。
- 验收后已将流程恢复为成功配置并重跑一次（`flow-...-4`），实例保留健康状态。

### 导入导出（15）

- 导出：`GET /config/dashboards/{id}/export?selection=...&selection=...` → 200，附件 `scriptboard-dashboard-nodes-20260915-174910.json`；含 2 节点，number 卡带完整 actions（quick_run 带本机 `quickRunId` + 名称注释，browser_http 带 method/url），flow 卡含 4 节点 + 2 post；导出内容不含访问 Key（keyLeaked=false）（`evidence-15-export.json`）。
- 导入：新建面板「导入验收」（slug `import-accept`，ID `kmcwDOF1awZCikYdWfeYq6J-`），multipart 导入 → 303 `imported=1`；2 张卡片落库，名称、actions、flow 完整（`evidence-15-imported-dashboard.html`）。
- 导入的 flow 卡片直接运行成功（`flow-uyw1zc_5D5riJhqCTAcB7itt-5`：4 节点 succeeded + always post 执行），证明 quickRunId 本机保留有效。截图 `shot-15-imported-monitor.png`。

## 已知限制

- **数值卡片实时取数受限（环境因素，非产品缺陷）**：本机 VPN 采用 fake-ip 模式，所有域名 DNS 解析到 `198.18.0.0/15`，该段被出站策略（`internal/outboundpolicy`，`isAlwaysBlocked`）按设计拒绝；面板抓取仅允许公网 80/443，不允许 loopback/私网，故无法用本地 JSON 服务替代。卡片创建、刷新链路、监控页/公开页渲染均验证通过，数值区显示「暂时无法获取数据」占位与错误徽标。在直连公网的正常网络环境中取数预期正常。
- `browser_http` 的「访客凭据」字段未单独覆盖（清单未列）；confirm 确认弹窗交互未逐项点击（confirm 字段在配置校验中已接受）。
- 公开页状态轮询（3–5 秒快照）未做计时精度验证。
- 10 次失败 Key 锁定 15 分钟未做完整爆破验证（错 Key 单次 404 已验证）。
- 测试数据按要求全部保留，实例保持运行。
