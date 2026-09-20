# 待合并工作整合验收（2026-09-20）

基于 dev 的 7bdcf168，整合 review-v253-fixes、password-auth-modal、editable-items-inventory、registry-list-details、registry-ux-flow、registry-access-mode、dashboard-actions 的有效变更。notifications-alerts-plan 按要求删除，不包含通知中心。

## 功能与修复

| 范围 | 完成内容 |
|---|---|
| 指标与认证 | 主机指标按采样权重统计；多平台镜像仅在数据完整时汇总压缩大小；独立二次认证页面支持原生表单提交 |
| Registry | 保留现有工作区、命名空间与分页；补齐连接测试、只读/可写、重复端点校验、镜像详情与平台信息、批量清理预览、恢复与操作人历史 |
| 私有备注 | 18 类可编辑记录支持最多 500 字备注；适配现有字段和编辑入口；公开面板不暴露备注 |
| 面板操作 | 四种可见性、访问 Key 管理、快捷执行按钮、访客浏览器请求；站内确认框；公开结果轮询校验 Key、动作、运行及触发人归属 |
| 流程 | YAML DAG、参数化快捷执行、内联脚本、Node.js、内置节点、节点级及全局 post、导入导出、历史与移动端布局 |
| 执行可靠性 | 内置任务经 Runner 传输与摘要校验；Git 子进程遵循 Runner 资源约束和进程树终止；工作区权限、路径边界、并发历史与关闭行为修复 |

数据库依次使用 schema 73（备注）、74（面板可见性与流程）、75（快捷执行参数）。修复旧版本重建表丢失备注的问题；保留现有内存额度、Registry 只读配置等功能。同步 README、中英文使用说明、配置格式和 ADR。

## 验证结果

- 完整 go test ./... 两轮通过；最终一轮 Web 包耗时约 205 秒。最后的 Runner 子进程策略变更另行通过 flowbuiltin、runmanager、runnerhost、customdashboard 回归；新增真实 Git 受管执行与取消测试通过。
- go vet ./... 通过；最后修改的执行相关包再次 vet 通过。Windows 构建与 Linux amd64 交叉构建通过。
- 外部 Chromium 静态契约检查通过；浏览器完整运行门禁通过。首次运行发现快捷执行脚本定位器歧义，修正后完整重跑通过。
- 实际本地部署 27 项 HTTP/浏览器验收通过：基本页面访问、备注、参数、混合流程、Runner 记录、节点/全局 post、访问 Key、公开状态最小化、备注隔离、导出和移动端。
- 另 12 项实际 UI/Registry 验收通过：确认弹窗与自动轮询、明文 Registry、默认只读、重复端点、20+5 分页、平台详情、显式可写、空清理预览及窄屏布局。
- 最终二进制重新部署后，3 项复验通过：历史保留与 TLS 风险提示、完整混合流程、操作按钮保留。桌面与移动端截图已检查。
- 自动化覆盖明文 HTTP、校验 TLS、显式跳过证书验证；升级覆盖 schema 20、36、43、64、72、73。

## 保留的验收环境

- 应用：http://127.0.0.1:11165 ，管理员 admin，测试密码见本机验收目录 ids.json。
- Registry 测试服务：http://127.0.0.1:11166 ，含 25 个镜像仓库。
- 状态、日志、截图及测试脚本：D:/Github/ScriptBoard/.scratch/acceptance-20260920/ 。保留测试数据和两个服务。
- 完整测试日志：D:/Github/ScriptBoard/.scratch/finish-pending-20260920/ 。
- 原始 review：D:/Github/ScriptBoard/.scratch/worktree-review-2026-09-20/REVIEW.md 。

本次使用 Windows 本机便携部署；Linux 为交叉编译，未执行 Linux 原生部署、Windows SCM 服务安装和 race detector。真实生产 Registry 的删除/恢复仍取决于服务端能力；本机使用测试服务与自动化夹具验证。

原有功能 worktree 保留作为来源备份，尤其 dashboard-actions 的未提交内容；本次临时整合 worktree 在合并后清理。主工作区原有 AGENTS.md、DASHBOARD-ACTIONS-PLAN.md 和 registry-management.html 的内容以合并前后哈希核对，不纳入本次提交。
