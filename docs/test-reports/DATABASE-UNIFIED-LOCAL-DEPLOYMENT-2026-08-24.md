# 数据库融合工作台本地部署测试报告

测试时间：2026-08-24 17:44（Asia/Shanghai）

## 部署信息

| 项目 | 结果 |
| --- | --- |
| 分支 | `codex/database-unified`，基于 `dev` 的 `e07bfe8` |
| Worktree | `D:\Github\worktrees\ScriptBoard\database-unified` |
| 构建 | `ScriptBoard development`，数据库 schema 54 |
| 地址 | `http://127.0.0.1:18788` |
| 管理员 | `admin`；一次性密码保留在 State Root 私有文件中，未写入报告或版本库 |
| State Root | `D:\Github\worktrees\ScriptBoard\database-unified\.scratch\database-unified-deployment\state` |
| 当前进程 | PID 37256，保持运行 |

## 测试结果

| 序号 | 测试项 | 方式 | 结果 |
| --- | --- | --- | --- |
| 1 | 登录页基础访问 | 外部 Chrome | 通过；页面标题、用户名、密码和登录控件正常 |
| 2 | 管理员登录与数据库入口 | HTTP 会话 | 通过；登录后进入 `/monitor`，数据库页面返回 200 |
| 3 | 单一数据库工作台 | HTTP 页面检查 | 通过；页面同时包含 MySQL / MariaDB 与 Redis 两个连接分组，不再显示旧页面级引擎切换 |
| 4 | 单一新增连接入口 | HTTP 页面检查 | 通过；“添加数据库连接”抽屉同时提供 MySQL 与 Redis 类型选择 |
| 5 | MySQL 安全连接配置 | HTTP 表单 | 通过；保存 `verify_identity` 测试连接并保留 CA 路径 |
| 6 | MySQL 明文连接配置 | HTTP 表单 | 通过；保存 `disabled` 测试连接，表单明确提示明文风险 |
| 7 | Redis 安全连接配置 | HTTP 表单 | 通过；保存 `verify_identity` 测试连接 |
| 8 | Redis 明文连接配置 | HTTP 表单 | 通过；保存 `disabled` 测试连接 |
| 9 | Redis 跳过证书验证 | HTTP 表单 | 通过；保存 `insecure_skip_verify` 测试连接，表单明确提示中间人攻击风险 |
| 10 | 两类连接选择 | HTTP 页面检查 | 通过；从同一连接栏分别进入 MySQL 与 Redis 操作区，连接栏保持融合状态 |
| 11 | 不可达连接状态 | HTTP 页面检查 | 通过；测试端口拒绝连接时页面保留连接元数据并呈现受控错误，不泄露测试密码 |
| 12 | 响应式与键盘契约 | 自动化测试与样式审查 | 通过；窄屏连接类型选择重排为单列，既有抽屉、焦点和局部刷新契约通过 |
| 13 | 相关 Web 回归 | `go test ./internal/web` 定向用例 | 通过 |

## 保留的测试数据

- `QA MySQL TLS`：`127.0.0.1:33306`，证书身份验证。
- `QA MySQL plaintext`：`127.0.0.1:33307`，明文模式。
- `QA Redis TLS`：`127.0.0.1:36379`，证书验证。
- `QA Redis plaintext`：`127.0.0.1:36380`，明文模式。
- `QA Redis skip verify`：`127.0.0.1:36381`，跳过证书验证。

这些端口故意未部署数据库服务，用于验证保存、选择和失败状态，不会连接真实业务数据库。测试数据和当前部署均按要求保留。
