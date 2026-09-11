# 镜像仓库管理本地测试

日期：2026-09-09。工作树：`D:\Github\worktrees\ScriptBoard\registry-management`。

## 验证范围

- 基础访问：匿名保护、登录、未知路由、CSRF。
- 连接：新增、编辑、切换、重启后保留；匿名模式清除旧用户名。
- 连接安全：HTTP、受信 HTTPS、不受信证书拒绝、显式跳过证书验证。
- 查看：真实 Registry V2 目录、namespace 筛选、tag 与 digest 详情、tag 分页。
- 清理：名称前缀、tag 通配符、保护 tag、同 digest 关联影响、确认文本、过期/变化/重复执行拒绝、逐项失败记录。
- UI：连接/详情/规则/删除确认使用现有任务抽屉；桌面及 390px 移动端。

## 结果

| 检查 | 结果 |
| --- | --- |
| 新增 Registry 客户端、连接服务、Broker 权限测试 | 通过 |
| 新增 Web HTTP 完整流程及 CSRF 测试 | 通过 |
| Go 全量回归与 go vet | 同步最新 dev 后全部通过 |
| 原有浏览器回归 | 同步后全部通过，含 Chromium desktop gate |
| 真实 Registry + 外部 Microsoft Edge | 通过，浏览器无 pageerror |
| HTTP / HTTPS / 显式跳过证书验证 | 通过；未启用跳过时拒绝自签名证书 |
| tag 分页、Bearer delete scope | 通过 |
| 共享 digest 的关联 tag | `team-a/api:v1` 与 `latest` 同时出现在删除预览中 |
| namespace 删除边界 | 删除 `team-a/api`、`team-a/jobs/worker` 的 manifest；`team-ab/api:stable` 保留 |
| 删除后 Registry API 验证 | 两个目标仓库 tags 为 null；相近前缀仓库仍返回 stable |
| 版本/连接变化及重复执行 | 拒绝陈旧或已消费的计划；确认失败不执行删除 |
| 预览过期与存储预算 | 过期计划不可执行；历史超限时裁剪并保持连接可读，管理连接不进入卡片存储 |
| 部分删除失败 | 成功和失败逐项记录，不将部分失败显示为全部成功 |
| 规则通配符 | `sandbox/` + `dev-*` 匹配 `sandbox/web:dev-101` |
| 抽屉连续操作 | 详情 → 预览、规则 → 预览均保留后台页面；执行结果留在抽屉中 |
| 移动端 | 390px 下内容完整，无页面横向溢出 |

原有浏览器套件首次运行在设置导航断言失败：起始版本的期望遗漏嵌入配置页。最新 dev 已修复此断言，同步后重新运行；本任务未额外修改该断言。

## 保留部署

| 项目 | 值 |
| --- | --- |
| ScriptBoard | http://127.0.0.1:19743/resources/registries |
| 用户 / 密码 | `admin` / `RegistryLocal-2026-Test!`（仅此隔离测试实例） |
| PID | 80356 |
| 二进制 | `.scratch/registry-management-local/scriptboard-verified.exe` |
| SHA-256 | `60955FD49DA98FAD3127877B8048D50616B395B82A85971178D1D6AD22228AD6` |
| State Root | `.scratch/registry-management-local/state` |
| Registry | http://127.0.0.1:18944 |
| Docker 容器 | `scriptboard-registry-management-test`，Registry 2.8.3，启用删除 |
| 日志 | `.scratch/registry-management-local/stdout-verified.log`、`stderr-verified.log` |
| 浏览器结果 | `.scratch/registry-management-local/browser-results.json` |
| 截图 | 同目录 `desktop.png`、`deletion-drawer.png`、`mobile.png` |

当前部署、Registry 和测试数据保留；运行中的测试部署位于工作树，因此工作树随部署保留。管理连接、预览及历史使用独立加密存储，不复用监控卡片事务文件。清理按 digest 删除已列出的镜像引用；不触发远端 GC，也不声称清除 V2 tag 接口无法枚举的无 tag 内容。保留规则当前手动执行，时间条件使用镜像创建时间。
