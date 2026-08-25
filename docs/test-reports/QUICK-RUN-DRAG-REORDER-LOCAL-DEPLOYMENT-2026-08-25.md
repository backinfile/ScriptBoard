# 快捷执行拖动排序本地部署测试报告

测试时间：2026-08-25（Asia/Shanghai）

测试分支：`codex/copy-tooltip-layer`

基线提交：`231712b fix(web): simplify login page copy`

结论：**通过**。快捷执行页已删除分组和卡片菜单中的“上移/下移”，改为从页头进入“调整顺序”模式；该模式支持拖动分组、拖动组内快捷执行卡片，也支持聚焦手柄后使用方向键调整。完成排序后两层顺序均持久化。

## 1. 测试条目

1. 登录页基础访问、CSP 与未登录路由保护。
2. 管理员登录和快捷执行页基础访问。
3. 普通页仅保留“调整顺序”入口，不再包含旧 `/move` 表单和 `direction` 按钮。
4. 排序模式显示分组与卡片专用拖动手柄，并隐藏普通操作菜单。
5. 鼠标拖动调整两个分组的顺序并保存。
6. 鼠标拖动调整同一分组内两张快捷执行卡片的顺序并保存。
7. 聚焦手柄后通过方向键调整分组和卡片顺序并保存。
8. 390px 移动端布局无横向溢出。
9. 浏览器运行时无页面错误。

额外覆盖：服务端 CSRF、完整清单校验、缺项冲突回滚、只读权限边界、助手 UI 动作目录和 PJAX 延迟加载壳。

## 2. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18834` |
| 进程 | PID `588`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-quick-run-drag-reorder-231712b` |
| State Root | `.scratch/local-deploy-quick-run-drag-reorder-231712b/state` |
| 管理员 | `admin`；一次性密码仅保留在 State Root 的 `secrets/initial-admin-password` |
| 标准错误 | 0 字节 |
| 审计校验 | 14 个事件；哈希链和外部签名 checkpoint 均有效 |

部署、State Root、测试数据、浏览器 JSON 结果和截图均保留；报告生成后未停止进程。

## 3. 外部 Chromium 部署验证（9/9）

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 1 | 基础访问与 CSP | 通过；`GET /login` → 200 且包含 CSP |
| 2 | 未登录保护 | 通过；快捷执行页返回 303 并跳转登录页 |
| 3 | 管理员登录 | 通过；`admin` 成功进入主机概览 |
| 4 | 保留测试数据 | 通过；通过部署后的 UI 创建 2 个分组和 3 个快捷执行 |
| 5 | 排序模式 | 通过；旧上移/下移入口不存在，2 个分组手柄和 3 个卡片手柄可用 |
| 6 | 鼠标拖动持久化 | 通过；分组与组内卡片顺序保存后保持 |
| 7 | 键盘排序持久化 | 通过；方向键调整并保存成功 |
| 8 | 移动端布局 | 通过；390px 视口横向溢出 0px |
| 9 | 浏览器运行时 | 通过；无 page error |

保留证据：

- `.scratch/local-deploy-quick-run-drag-reorder-231712b/deployment-quick-run-reorder-probe.cjs`
- `.scratch/local-deploy-quick-run-drag-reorder-231712b/deployment-quick-run-reorder-probe.json`
- `.scratch/local-deploy-quick-run-drag-reorder-231712b/quick-run-reorder-dragged.png`
- `.scratch/local-deploy-quick-run-drag-reorder-231712b/quick-run-reorder-mobile.png`

## 4. 自动化回归

| 项目 | 结果 |
| --- | --- |
| 快捷执行排序聚焦测试 | 通过；覆盖批量保存、缺项回滚、旧入口删除和排序页契约 |
| `go test ./...` | 通过 |
| `npm test`（完整 Chromium 门禁） | 通过 |
| `node --check internal/web/ui/assets/app.js` | 通过 |
| `git diff --check` | 通过 |

## 5. 保留状态

- 新部署继续监听 `127.0.0.1:18834`，PID 为 `588`。
- 测试使用仓库安装的外部 Playwright Chromium，没有使用应用内浏览器。
- 测试数据 `Reorder Alpha`、`Reorder Beta`、`Reorder First`、`Reorder Second`、`Reorder Third` 保留在部署 State Root 中。
- 初始管理员密码未写入报告、日志或版本库，仅保留在部署 State Root 中。
